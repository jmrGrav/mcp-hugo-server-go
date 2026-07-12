package write_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/security"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools/write"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type expectedPage struct {
	Title      string
	Body       string
	Tags       []string
	Categories []string
}

type scenarioOp struct {
	Kind       string
	Slug       string
	Title      string
	Body       string
	Tags       []string
	Categories []string
}

func TestWriteScenarioProperty(t *testing.T) {
	t.Parallel()

	runWriteScenarioProperty(t, 12)
}

func runWriteScenarioProperty(t *testing.T, seeds int) {
	t.Helper()

	for seed := 0; seed < seeds; seed++ {
		t.Run(fmt.Sprintf("seed_%02d", seed), func(t *testing.T) {
			contentRoot := t.TempDir()
			session, idx, done := newPropertyTestServer(t, contentRoot)
			defer done()

			rng := rand.New(rand.NewSource(int64(seed + 1)))
			trace := make([]scenarioOp, 0, 16)
			model := map[string]expectedPage{}
			deleteCount := 0

			for step := 0; step < 16; step++ {
				op := nextScenarioOp(rng, model, deleteCount)
				trace = append(trace, op)
				applyScenarioOp(t, session, contentRoot, model, op, &deleteCount, trace)
				assertScenarioState(t, idx, contentRoot, model, trace)
			}
		})
	}
}

// TestWriteErrorPaths verifies that the tools return errors for invalid
// operations: empty/reserved slug, update/delete on missing slug.
// Note: create_page on an existing slug is intentionally an upsert (not an error).
func TestWriteErrorPaths(t *testing.T) {
	t.Parallel()

	contentRoot := t.TempDir()
	session, _, done := newPropertyTestServer(t, contentRoot)
	defer done()

	mustError := func(t *testing.T, res *mcp.CallToolResult, desc string) {
		t.Helper()
		if !res.IsError {
			raw, _ := json.Marshal(res.Content)
			t.Fatalf("%s: expected error, got success: %s", desc, raw)
		}
	}

	// Empty slug must fail.
	mustError(t, callTool(t, session, "create_page", map[string]any{
		"slug": "", "title": "T", "body": "B", "tags": []any{}, "categories": []any{},
	}), "create_page with empty slug")

	// Reserved slug must fail.
	mustError(t, callTool(t, session, "create_page", map[string]any{
		"slug": "_index", "title": "T", "body": "B", "tags": []any{}, "categories": []any{},
	}), "create_page with reserved slug _index")

	// update on missing slug must fail (not_found).
	mustError(t, callTool(t, session, "update_page", map[string]any{
		"slug": "err/missing", "title": "No Such Page", "body": "body",
	}), "update_page on missing slug")

	// delete_page is idempotent (os.RemoveAll on missing dir succeeds);
	// verify empty slug still errors.
	mustError(t, callTool(t, session, "delete_page", map[string]any{
		"slug": "",
	}), "delete_page with empty slug")
}

func newPropertyTestServer(t *testing.T, contentRoot string) (*mcp.ClientSession, *hugosite.SourceIndex, func()) {
	t.Helper()

	pg, err := security.New(contentRoot, true)
	if err != nil {
		t.Fatalf("security.New: %v", err)
	}
	idx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatalf("hugosite.NewSourceIndex: %v", err)
	}
	cfg := config.Default()
	cfg.ContentRoot = contentRoot

	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1"}, nil)
	write.Register(s, pg, idx, cfg, nil)

	ctx := context.Background()
	t1, t2 := mcp.NewInMemoryTransports()
	if _, err := s.Connect(ctx, t1, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.1"}, nil)
	session, err := client.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	return session, idx, func() { _ = session.Close() }
}

func nextScenarioOp(rng *rand.Rand, model map[string]expectedPage, deleteCount int) scenarioOp {
	slug := []string{"prop/a", "prop/b", "prop/c"}[rng.Intn(3)]
	existing, exists := model[slug]
	if !exists {
		if rng.Intn(2) == 0 {
			return scenarioOp{
				Kind:       "create",
				Slug:       slug,
				Title:      randomText(rng, "Title"),
				Body:       randomText(rng, "Body"),
				Tags:       randomTerms(rng, "tag", 2),
				Categories: randomTerms(rng, "cat", 2),
			}
		}
		return scenarioOp{
			Kind:       "dry_create",
			Slug:       slug,
			Title:      randomText(rng, "DryTitle"),
			Body:       randomText(rng, "DryBody"),
			Tags:       randomTerms(rng, "dry-tag", 2),
			Categories: randomTerms(rng, "dry-cat", 2),
		}
	}

	switch {
	case deleteCount < 4 && rng.Intn(5) == 0:
		return scenarioOp{Kind: "delete", Slug: slug}
	case rng.Intn(3) == 0:
		return scenarioOp{
			Kind:       "dry_update",
			Slug:       slug,
			Title:      existing.Title + " Preview",
			Body:       existing.Body + "\npreview",
			Tags:       randomTerms(rng, "preview-tag", 2),
			Categories: randomTerms(rng, "preview-cat", 2),
		}
	default:
		return scenarioOp{
			Kind:       "update",
			Slug:       slug,
			Title:      existing.Title + " Updated",
			Body:       existing.Body + "\nupdated",
			Tags:       randomTerms(rng, "upd-tag", 2),
			Categories: randomTerms(rng, "upd-cat", 2),
		}
	}
}

func applyScenarioOp(t *testing.T, session *mcp.ClientSession, contentRoot string, model map[string]expectedPage, op scenarioOp, deleteCount *int, trace []scenarioOp) {
	t.Helper()

	switch op.Kind {
	case "create":
		res := callTool(t, session, "create_page", map[string]any{
			"slug":       op.Slug,
			"title":      op.Title,
			"body":       op.Body,
			"tags":       toAnySlice(op.Tags),
			"categories": toAnySlice(op.Categories),
		})
		mustToolSucceed(t, res, op, trace)
		model[op.Slug] = expectedPage{Title: op.Title, Body: op.Body, Tags: slices.Clone(op.Tags), Categories: slices.Clone(op.Categories)}
	case "dry_create":
		before := snapshotFile(contentRoot, op.Slug)
		res := callTool(t, session, "create_page", map[string]any{
			"slug":       op.Slug,
			"title":      op.Title,
			"body":       op.Body,
			"tags":       toAnySlice(op.Tags),
			"categories": toAnySlice(op.Categories),
			"dry_run":    true,
		})
		mustToolSucceed(t, res, op, trace)
		after := snapshotFile(contentRoot, op.Slug)
		if before != after {
			t.Fatalf("dry_create mutated disk for %q\ntrace=%s", op.Slug, formatTrace(trace))
		}
		if _, ok := model[op.Slug]; ok {
			t.Fatalf("dry_create unexpectedly mutated model for %q\ntrace=%s", op.Slug, formatTrace(trace))
		}
	case "update":
		res := callTool(t, session, "update_page", map[string]any{
			"slug":       op.Slug,
			"title":      op.Title,
			"body":       op.Body,
			"tags":       toAnySlice(op.Tags),
			"categories": toAnySlice(op.Categories),
		})
		mustToolSucceed(t, res, op, trace)
		model[op.Slug] = expectedPage{Title: op.Title, Body: op.Body, Tags: slices.Clone(op.Tags), Categories: slices.Clone(op.Categories)}
	case "dry_update":
		before := snapshotFile(contentRoot, op.Slug)
		res := callTool(t, session, "update_page", map[string]any{
			"slug":       op.Slug,
			"title":      op.Title,
			"body":       op.Body,
			"tags":       toAnySlice(op.Tags),
			"categories": toAnySlice(op.Categories),
			"dry_run":    true,
		})
		mustToolSucceed(t, res, op, trace)
		after := snapshotFile(contentRoot, op.Slug)
		if before != after {
			t.Fatalf("dry_update mutated disk for %q\ntrace=%s", op.Slug, formatTrace(trace))
		}
	case "delete":
		res := callTool(t, session, "delete_page", map[string]any{
			"slug": op.Slug,
		})
		mustToolSucceed(t, res, op, trace)
		delete(model, op.Slug)
		*deleteCount++
	default:
		t.Fatalf("unknown op %q", op.Kind)
	}
}

func mustToolSucceed(t *testing.T, res *mcp.CallToolResult, op scenarioOp, trace []scenarioOp) {
	t.Helper()
	if !res.IsError {
		return
	}
	raw, _ := json.Marshal(res.Content)
	t.Fatalf("%s(%q) returned error: %s\ntrace=%s", op.Kind, op.Slug, raw, formatTrace(trace))
}

func assertScenarioState(t *testing.T, liveIdx *hugosite.SourceIndex, contentRoot string, model map[string]expectedPage, trace []scenarioOp) {
	t.Helper()

	assertIndexState(t, liveIdx, model, trace, "live")

	diskIdx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatalf("NewSourceIndex() error = %v\ntrace=%s", err, formatTrace(trace))
	}
	assertIndexState(t, diskIdx, model, trace, "disk")

	for slug, want := range model {
		path := filepath.Join(contentRoot, filepath.FromSlash(slug), "index.md")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(%q) error = %v\ntrace=%s", path, err, formatTrace(trace))
		}
		raw := string(data)
		if !strings.Contains(raw, "title: "+want.Title+"\n") {
			t.Fatalf("file missing title YAML field for %q: want %q\nraw=%q\ntrace=%s", slug, want.Title, raw, formatTrace(trace))
		}
		if !strings.Contains(raw, want.Body) {
			t.Fatalf("file missing body for %q: want %q\nraw=%q\ntrace=%s", slug, want.Body, raw, formatTrace(trace))
		}
	}

	for _, slug := range []string{"prop/a", "prop/b", "prop/c"} {
		if _, ok := model[slug]; ok {
			continue
		}
		path := filepath.Join(contentRoot, filepath.FromSlash(slug), "index.md")
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("unexpected file still present for %q (err=%v)\ntrace=%s", slug, err, formatTrace(trace))
		}
	}
}

func assertIndexState(t *testing.T, idx *hugosite.SourceIndex, model map[string]expectedPage, trace []scenarioOp, label string) {
	t.Helper()

	wantSlugs := mapsKeysSorted(model)
	gotSlugs := idx.AllSlugs()
	slices.Sort(gotSlugs)
	if !reflect.DeepEqual(gotSlugs, wantSlugs) {
		t.Fatalf("%s slugs mismatch\ngot=%v\nwant=%v\ntrace=%s", label, gotSlugs, wantSlugs, formatTrace(trace))
	}

	for slug, want := range model {
		page, ok := idx.GetBySlug(slug)
		if !ok {
			t.Fatalf("%s GetBySlug(%q) missing expected page\ntrace=%s", label, slug, formatTrace(trace))
		}
		if page.Title != want.Title {
			t.Fatalf("%s title mismatch for %q: got=%q want=%q\ntrace=%s", label, slug, page.Title, want.Title, formatTrace(trace))
		}
		if page.Body != want.Body {
			t.Fatalf("%s body mismatch for %q: got=%q want=%q\ntrace=%s", label, slug, page.Body, want.Body, formatTrace(trace))
		}
		if !reflect.DeepEqual(page.Tags, want.Tags) {
			t.Fatalf("%s tags mismatch for %q: got=%v want=%v\ntrace=%s", label, slug, page.Tags, want.Tags, formatTrace(trace))
		}
		if !reflect.DeepEqual(page.Categories, want.Categories) {
			t.Fatalf("%s categories mismatch for %q: got=%v want=%v\ntrace=%s", label, slug, page.Categories, want.Categories, formatTrace(trace))
		}
	}
}

func mapsKeysSorted(m map[string]expectedPage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func snapshotFile(root, slug string) string {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(slug), "index.md"))
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		return "ERR:" + err.Error()
	}
	return string(data)
}

func randomText(rng *rand.Rand, prefix string) string {
	return fmt.Sprintf("%s-%03d", prefix, rng.Intn(1000))
}

func randomTerms(rng *rand.Rand, prefix string, max int) []string {
	n := rng.Intn(max) + 1
	values := make([]string, 0, n)
	for i := 0; i < n; i++ {
		values = append(values, fmt.Sprintf("%s-%d", prefix, rng.Intn(5)))
	}
	return slices.Compact(values)
}

func toAnySlice(values []string) []any {
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = v
	}
	return out
}

func formatTrace(trace []scenarioOp) string {
	lines := make([]string, 0, len(trace))
	for i, op := range trace {
		lines = append(lines, fmt.Sprintf("%02d %s slug=%q title=%q tags=%v categories=%v", i, op.Kind, op.Slug, op.Title, op.Tags, op.Categories))
	}
	return strings.Join(lines, "\n")
}

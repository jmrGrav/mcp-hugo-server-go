package soak_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/db"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/security"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
	admin "github.com/jmrGrav/mcp-hugo-server-go/internal/tools/admin"
	anonymous "github.com/jmrGrav/mcp-hugo-server-go/internal/tools/anonymous"
	readtools "github.com/jmrGrav/mcp-hugo-server-go/internal/tools/read"
	writetools "github.com/jmrGrav/mcp-hugo-server-go/internal/tools/write"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMutationBuildSoak(t *testing.T) {
	if os.Getenv("SOAK") != "1" {
		t.Skip("set SOAK=1 to run soak harness")
	}

	duration := envDuration("SOAK_DURATION", 20*time.Second)
	concurrency := envInt("SOAK_CONCURRENCY", 4)
	withDB := os.Getenv("SOAK_WITH_DB") == "1"
	summaryPath := os.Getenv("SOAK_SUMMARY_PATH")

	h := newSoakHarness(t, withDB)
	defer h.Close()

	session := h.newSession(t)
	if res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "build_site", Arguments: map[string]any{},
	}); err != nil || res.IsError {
		t.Fatalf("initial build failed: err=%v result=%s", err, resultJSON(res))
	}
	_ = session.Close()
	h.checkSourceInvariant(t)
	h.checkPublicInvariant(t)

	sum := h.run(t, duration, concurrency)
	if summaryPath != "" {
		data, err := json.MarshalIndent(sum, "", "  ")
		if err != nil {
			t.Fatalf("marshal summary: %v", err)
		}
		if err := os.WriteFile(summaryPath, data, 0o644); err != nil {
			t.Fatalf("write summary: %v", err)
		}
	}

	if len(sum.InvariantFailures) > 0 {
		t.Fatalf("soak invariant failures:\n%s", strings.Join(sum.InvariantFailures, "\n"))
	}
}

type soakHarness struct {
	cfg         config.Config
	server      *mcp.Server
	sourceIdx   *hugosite.SourceIndex
	siteIdx     *site.Index
	siteDB      *db.DB
	contentRoot string
	siteRoot    string
	hugoRoot    string
	pathGuard   *security.PathGuard
	pathEnvOld  string
	checkMu     sync.Mutex
	slugSeq     atomic.Int64
}

type soakSummary struct {
	StartedAt         time.Time          `json:"started_at"`
	DurationSeconds   int                `json:"duration_seconds"`
	Concurrency       int                `json:"concurrency"`
	WithDB            bool               `json:"with_db"`
	GoroutinesStart   int                `json:"goroutines_start"`
	GoroutinesEnd     int                `json:"goroutines_end"`
	HeapAllocStart    uint64             `json:"heap_alloc_start"`
	HeapAllocEnd      uint64             `json:"heap_alloc_end"`
	SuccessCounts     map[string]int     `json:"success_counts"`
	ErrorCounts       map[string]int     `json:"error_counts"`
	LatencyMsByTool   map[string][]int64 `json:"latency_ms_by_tool"`
	InvariantFailures []string           `json:"invariant_failures,omitempty"`
}

func newSoakHarness(t *testing.T, withDB bool) *soakHarness {
	t.Helper()

	hugoRoot := t.TempDir()
	contentRoot := filepath.Join(hugoRoot, "content")
	siteRoot := filepath.Join(hugoRoot, "public")
	if err := os.MkdirAll(contentRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll content: %v", err)
	}
	if err := os.MkdirAll(siteRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll public: %v", err)
	}

	writeMockHugo(t, hugoRoot)

	cfg := config.Default()
	cfg.HugoRoot = hugoRoot
	cfg.ContentRoot = contentRoot
	cfg.SiteRoot = siteRoot
	cfg.SiteURL = "https://example.test"
	cfg.SiteName = "soak"

	pg, err := security.New(contentRoot, true)
	if err != nil {
		t.Fatalf("security.New: %v", err)
	}
	srcIdx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatalf("NewSourceIndex: %v", err)
	}
	siteIdx, err := site.NewIndex(cfg)
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}

	var siteDB *db.DB
	if withDB {
		siteDB, err = db.Open(filepath.Join(hugoRoot, "soak.db"))
		if err != nil {
			t.Fatalf("db.Open: %v", err)
		}
		if err := siteDB.StartupSync(siteIdx, srcIdx); err != nil {
			t.Fatalf("siteDB.StartupSync: %v", err)
		}
	}

	server := mcp.NewServer(&mcp.Implementation{Name: "soak", Version: "0.1"}, nil)
	anonymous.Register(server, siteIdx, cfg, srcIdx)
	readtools.Register(server, siteIdx, cfg, srcIdx)
	writetools.Register(server, pg, srcIdx, cfg, siteDB, siteIdx)
	admin.Register(server, cfg, func() error {
		if err := siteIdx.Reload(cfg); err != nil {
			return err
		}
		if siteDB != nil {
			if err := siteDB.PostBuildSync(siteIdx); err != nil {
				return err
			}
			if err := siteDB.SnapshotSiteHealth(); err != nil {
				return err
			}
		}
		return nil
	})

	return &soakHarness{
		cfg:         cfg,
		server:      server,
		sourceIdx:   srcIdx,
		siteIdx:     siteIdx,
		siteDB:      siteDB,
		contentRoot: contentRoot,
		siteRoot:    siteRoot,
		hugoRoot:    hugoRoot,
		pathGuard:   pg,
		pathEnvOld:  os.Getenv("PATH"),
	}
}

func writeMockHugo(t *testing.T, hugoRoot string) {
	t.Helper()
	mockDir := t.TempDir()
	script := filepath.Join(mockDir, "hugo")
	content := fmt.Sprintf(`#!/bin/sh
set -eu
ROOT=%q
CONTENT="$ROOT/content"
PUBLIC="$ROOT/public"
RESOURCES="$ROOT/resources"
mkdir -p "$RESOURCES"
touch "$RESOURCES/.soak-build"
preview=0
for arg in "$@"; do
  if [ "$arg" = "--renderToMemory" ]; then
    preview=1
  fi
done
if [ "$preview" -eq 1 ]; then
  exit 0
fi
rm -rf "$PUBLIC"
mkdir -p "$PUBLIC"
find "$CONTENT" -type f | while read -r f; do
  base=$(basename "$f")
  case "$base" in
    index.md|index.*.md)
      rel=${f#"$CONTENT"/}
      slug=$(dirname "$rel")
      mkdir -p "$PUBLIC/$slug"
      printf '<html><body><h1>%%s</h1></body></html>\n' "$slug" > "$PUBLIC/$slug/index.html"
      ;;
  esac
done
`, hugoRoot)
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatalf("WriteFile mock hugo: %v", err)
	}
	if err := os.Setenv("PATH", mockDir+string(os.PathListSeparator)+os.Getenv("PATH")); err != nil {
		t.Fatalf("Setenv PATH: %v", err)
	}
}

func (h *soakHarness) Close() {
	_ = os.Setenv("PATH", h.pathEnvOld)
	if h.siteDB != nil {
		_ = h.siteDB.Close()
	}
}

func (h *soakHarness) newSession(t *testing.T) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	t1, t2 := mcp.NewInMemoryTransports()
	if _, err := h.server.Connect(ctx, t1, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "soak-client", Version: "0.1"}, nil)
	session, err := client.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	return session
}

func (h *soakHarness) run(t *testing.T, duration time.Duration, concurrency int) soakSummary {
	t.Helper()

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	sum := soakSummary{
		StartedAt:       time.Now().UTC(),
		DurationSeconds: int(duration / time.Second),
		Concurrency:     concurrency,
		WithDB:          h.siteDB != nil,
		GoroutinesStart: runtime.NumGoroutine(),
		HeapAllocStart:  ms.HeapAlloc,
		SuccessCounts:   map[string]int{},
		ErrorCounts:     map[string]int{},
		LatencyMsByTool: map[string][]int64{},
	}

	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()

	var sumMu sync.Mutex
	var wg sync.WaitGroup
	for worker := 0; worker < concurrency; worker++ {
		worker := worker
		wg.Add(1)
		go func() {
			defer wg.Done()
			session := h.newSession(t)
			defer func() { _ = session.Close() }()
			rng := rand.New(rand.NewSource(int64(worker + 1)))

			for ctx.Err() == nil {
				op, args := h.nextOp(rng)
				start := time.Now()
				res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: op, Arguments: args})
				elapsed := time.Since(start).Milliseconds()

				sumMu.Lock()
				sum.LatencyMsByTool[op] = append(sum.LatencyMsByTool[op], elapsed)
				if err != nil {
					sum.ErrorCounts["transport_error:"+op]++
					sum.InvariantFailures = append(sum.InvariantFailures, fmt.Sprintf("transport error on %s: %v", op, err))
					sumMu.Unlock()
					continue
				}
				if res.IsError {
					sum.ErrorCounts[classifyToolError(op, resultJSON(res))]++
					sumMu.Unlock()
					continue
				}
				sum.SuccessCounts[op]++
				sumMu.Unlock()

				switch op {
				case "create_page", "update_page", "delete_page":
					if err := h.checkSourceInvariantErr(); err != nil {
						sumMu.Lock()
						sum.InvariantFailures = append(sum.InvariantFailures, fmt.Sprintf("%s invariant: %v", op, err))
						sumMu.Unlock()
					}
				case "build_site":
					if err := h.checkSourceInvariantErr(); err != nil {
						sumMu.Lock()
						sum.InvariantFailures = append(sum.InvariantFailures, fmt.Sprintf("build_site source invariant: %v", err))
						sumMu.Unlock()
					}
					if err := h.checkPublicInvariantErr(); err != nil {
						sumMu.Lock()
						sum.InvariantFailures = append(sum.InvariantFailures, fmt.Sprintf("build_site public invariant: %v", err))
						sumMu.Unlock()
					}
				}
			}
		}()
	}
	wg.Wait()

	runtime.ReadMemStats(&ms)
	sum.GoroutinesEnd = runtime.NumGoroutine()
	sum.HeapAllocEnd = ms.HeapAlloc
	return sum
}

func (h *soakHarness) nextOp(rng *rand.Rand) (string, map[string]any) {
	slugs := h.currentSlugs()
	choice := rng.Intn(100)
	switch {
	case choice < 25:
		id := h.slugSeq.Add(1)
		slug := fmt.Sprintf("soak/page-%03d", id)
		return "create_page", map[string]any{
			"slug":       slug,
			"title":      fmt.Sprintf("Title %03d", id),
			"body":       fmt.Sprintf("Body %03d", id),
			"tags":       []any{"soak", fmt.Sprintf("worker-%d", rng.Intn(4))},
			"categories": []any{"testing"},
		}
	case choice < 50 && len(slugs) > 0:
		slug := slugs[rng.Intn(len(slugs))]
		return "update_page", map[string]any{
			"slug":       slug,
			"title":      fmt.Sprintf("Updated %d", rng.Intn(1000)),
			"body":       fmt.Sprintf("Updated body %d", rng.Intn(1000)),
			"tags":       []any{"soak", "updated"},
			"categories": []any{"testing"},
		}
	case choice < 60 && len(slugs) > 0:
		slug := slugs[rng.Intn(len(slugs))]
		return "delete_page", map[string]any{"slug": slug}
	case choice < 72:
		return "build_site", map[string]any{}
	case choice < 84:
		return "preview_build", map[string]any{}
	case choice < 92 && len(slugs) > 0:
		slug := slugs[rng.Intn(len(slugs))]
		return "get_page", map[string]any{"slug": slug, "allow_source_fallback": true, "content_only": true}
	default:
		return "list_pages", map[string]any{"limit": 20, "offset": 0}
	}
}

func (h *soakHarness) currentSlugs() []string {
	h.checkMu.Lock()
	defer h.checkMu.Unlock()
	hugosite.ContentMu.RLock()
	defer hugosite.ContentMu.RUnlock()
	idx, err := hugosite.NewSourceIndex(h.contentRoot)
	if err != nil {
		return nil
	}
	slugs := idx.AllSlugs()
	slices.Sort(slugs)
	return slugs
}

func (h *soakHarness) checkSourceInvariant(t *testing.T) {
	t.Helper()
	if err := h.checkSourceInvariantErr(); err != nil {
		t.Fatal(err)
	}
}

func (h *soakHarness) checkSourceInvariantErr() error {
	h.checkMu.Lock()
	defer h.checkMu.Unlock()
	hugosite.ContentMu.RLock()
	defer hugosite.ContentMu.RUnlock()

	diskIdx, err := hugosite.NewSourceIndex(h.contentRoot)
	if err != nil {
		return fmt.Errorf("NewSourceIndex: %w", err)
	}
	live := h.sourceIdx.AllSlugs()
	disk := diskIdx.AllSlugs()
	slices.Sort(live)
	slices.Sort(disk)
	if !slices.Equal(live, disk) {
		return fmt.Errorf("source slug mismatch live=%v disk=%v", live, disk)
	}
	return nil
}

func (h *soakHarness) checkPublicInvariant(t *testing.T) {
	t.Helper()
	if err := h.checkPublicInvariantErr(); err != nil {
		t.Fatal(err)
	}
}

func (h *soakHarness) checkPublicInvariantErr() error {
	h.checkMu.Lock()
	defer h.checkMu.Unlock()
	hugosite.ContentMu.RLock()
	defer hugosite.ContentMu.RUnlock()

	// Source can be ahead of public between builds (create_page updates source
	// but not public; only build_site promotes source→public). Assert public ⊆ source
	// only: every publicly visible slug must exist in source (zombie-page check).
	srcSlugs := h.sourceIdx.AllSlugs()
	srcSet := make(map[string]struct{}, len(srcSlugs))
	for _, slug := range srcSlugs {
		srcSet[site.NormalizeSlug(slug)] = struct{}{}
	}

	gotPages := h.siteIdx.Sitemap()
	for _, p := range gotPages {
		if _, ok := srcSet[p.Slug]; !ok {
			return fmt.Errorf("public slug %q has no source entry (zombie page): public=%v",
				p.Slug, gotPages)
		}
	}
	return nil
}

func classifyToolError(tool, raw string) string {
	switch {
	case strings.Contains(raw, "rate_limit_exceeded"):
		return "rate_limit_exceeded:" + tool
	case strings.Contains(raw, "build_in_progress"):
		return "build_in_progress:" + tool
	case strings.Contains(raw, "not_found"):
		return "not_found:" + tool
	default:
		return "tool_error:" + tool
	}
}

func resultJSON(res *mcp.CallToolResult) string {
	if res == nil {
		return ""
	}
	b, _ := json.Marshal(res.Content)
	return string(b)
}

func envInt(name string, def int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return def
	}
	var v int
	if _, err := fmt.Sscanf(raw, "%d", &v); err != nil || v <= 0 {
		return def
	}
	return v
}

func envDuration(name string, def time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return def
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return def
	}
	return d
}

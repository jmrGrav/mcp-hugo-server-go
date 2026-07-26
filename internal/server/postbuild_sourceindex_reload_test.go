package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools/admin"
	readtools "github.com/jmrGrav/mcp-hugo-server-go/internal/tools/read"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestPostBuildCallbacksReloadsSourceIndexForTaxonomyHealth(t *testing.T) {
	contentRoot := t.TempDir()
	writeSourceBundle(t, contentRoot, "posts/debug-one", "Debug")
	writeSourceBundle(t, contentRoot, "posts/debug-two", "debug")

	cfg := config.Default()
	cfg.ContentRoot = contentRoot
	cfg.SiteRoot = filepath.Join("..", "..", "testdata", "fixtures", "public", "minimal")

	idx, err := site.NewIndex(cfg)
	if err != nil {
		t.Fatalf("site.NewIndex() error = %v", err)
	}
	srcIdx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatalf("hugosite.NewSourceIndex() error = %v", err)
	}

	session, done := newReadHealthSession(t, idx, cfg, srcIdx)
	defer done()

	before := healthDetailsFromTool(t, session)
	if !hasCasingVariant(before, "Debug", "debug") {
		t.Fatalf("expected initial taxonomy drift, got %#v", before)
	}

	replaceInFile(t, filepath.Join(contentRoot, "posts", "debug-two", "index.md"), "  - debug", "  - Debug")

	callback := findPostBuildCallback(t, postBuildCallbacks("build_site", slog.Default(), cfg, idx, srcIdx, nil), "index_reload")
	if err := callback.Fn(); err != nil {
		t.Fatalf("index_reload callback error = %v", err)
	}

	after := healthDetailsFromTool(t, session)
	if hasCasingVariant(after, "Debug", "debug") {
		t.Fatalf("taxonomy drift remained after source reload callback: %#v", after)
	}
}

func newReadHealthSession(t *testing.T, idx *site.Index, cfg config.Config, srcIdx *hugosite.SourceIndex) (*mcp.ClientSession, func()) {
	t.Helper()
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1"}, nil)
	readtools.Register(s, idx, cfg, srcIdx)
	readtools.RegisterWithSourceIndex(s, idx, srcIdx, cfg)

	ctx := context.Background()
	t1, t2 := mcp.NewInMemoryTransports()
	if _, err := s.Connect(ctx, t1, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	c := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.1"}, nil)
	session, err := c.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	return session, func() { _ = session.Close() }
}

func healthDetailsFromTool(t *testing.T, session *mcp.ClientSession) []map[string]any {
	t.Helper()
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "get_site_health",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("CallTool(get_site_health): %v", err)
	}
	if res.IsError {
		t.Fatalf("get_site_health returned error: %#v", res.Content)
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	var envelope map[string]any
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("unmarshal structured content: %v", err)
	}
	data, ok := envelope["data"].(map[string]any)
	if !ok {
		t.Fatalf("data type = %T, want map[string]any", envelope["data"])
	}
	items, ok := data["taxonomy_inconsistency_details"].([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("taxonomy detail type = %T, want map[string]any", item)
		}
		out = append(out, m)
	}
	return out
}

func writeSourceBundle(t *testing.T, contentRoot, slug, tag string) {
	t.Helper()
	dir := filepath.Join(contentRoot, filepath.FromSlash(slug))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", dir, err)
	}
	content := strings.Join([]string{
		"---",
		"title: " + filepath.Base(slug),
		"date: 2026-07-26T00:00:00Z",
		"tags:",
		"  - " + tag,
		"categories:",
		"  - Docs",
		"draft: false",
		"---",
		"",
		"Body.",
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(dir, "index.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", filepath.Join(dir, "index.md"), err)
	}
}

func replaceInFile(t *testing.T, path, old, new string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", path, err)
	}
	updated := strings.Replace(string(raw), old, new, 1)
	if updated == string(raw) {
		t.Fatalf("replaceInFile(%q): %q not found", path, old)
	}
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}

func findPostBuildCallback(t *testing.T, callbacks []admin.PostBuildCallback, name string) admin.PostBuildCallback {
	t.Helper()
	for _, cb := range callbacks {
		if cb.Name == name {
			return cb
		}
	}
	t.Fatalf("post-build callback %q not found", name)
	return admin.PostBuildCallback{}
}

func hasCasingVariant(details []map[string]any, termA, termB string) bool {
	for _, detail := range details {
		if detail["kind"] == "casing_variant" && detail["term_a"] == termA && detail["term_b"] == termB {
			return true
		}
	}
	return false
}

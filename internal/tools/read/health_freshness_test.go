package read_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/contentmodel"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/security"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools/read"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools/write"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func newReadWriteTestClient(t *testing.T, contentRoot string, srcIdx *hugosite.SourceIndex) (*mcp.ClientSession, func()) {
	t.Helper()

	pg, err := security.New(contentRoot, true)
	if err != nil {
		t.Fatalf("security.New: %v", err)
	}

	cfg := config.Default()
	cfg.ContentRoot = contentRoot

	siteIdx := &site.Index{}
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1"}, nil)
	read.Register(s, siteIdx, cfg, srcIdx)
	read.RegisterWithSourceIndex(s, siteIdx, srcIdx, cfg)
	write.Register(s, pg, srcIdx, cfg, nil, nil, siteIdx)

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
	return session, func() { _ = session.Close() }
}

func writeTaggedPage(t *testing.T, contentRoot, slug, title, tag string) string {
	t.Helper()

	dir := filepath.Join(contentRoot, slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", dir, err)
	}
	path := filepath.Join(dir, "index.md")
	raw := "---\n" +
		"title: " + title + "\n" +
		"tags:\n" +
		"  - " + tag + "\n" +
		"---\n\nBody.\n"
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
	return path
}

func hasTaxonomyPair(t *testing.T, details any, a, b string) bool {
	t.Helper()
	if details == nil {
		return false
	}
	items, ok := details.([]any)
	if !ok {
		t.Fatalf("taxonomy_inconsistency_details type = %T, want []any", details)
	}
	for _, item := range items {
		detail, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("taxonomy_inconsistency_details entry type = %T, want map", item)
		}
		if detail["term_a"] == a && detail["term_b"] == b {
			return true
		}
	}
	return false
}

func TestGetSiteHealthReflectsTaxonomyFixAfterUpdatePage(t *testing.T) {
	contentRoot := t.TempDir()
	writeTaggedPage(t, contentRoot, filepath.Join("posts", "one"), "One", "Debug")
	secondPath := writeTaggedPage(t, contentRoot, filepath.Join("posts", "two"), "Two", "debug")

	srcIdx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatalf("NewSourceIndex: %v", err)
	}
	session, done := newReadWriteTestClient(t, contentRoot, srcIdx)
	defer done()

	before := callTool(t, session, "get_site_health", map[string]any{})
	if before.IsError {
		t.Fatalf("get_site_health before update returned error: %v", before.Content)
	}
	beforeData := decodeContent(t, before)
	if !hasTaxonomyPair(t, beforeData["taxonomy_inconsistency_details"], "Debug", "debug") {
		t.Fatalf("expected Debug/debug inconsistency before update, got %#v", beforeData["taxonomy_inconsistency_details"])
	}

	revision, err := contentmodel.SourceRevision(secondPath)
	if err != nil {
		t.Fatalf("SourceRevision(%s): %v", secondPath, err)
	}
	update := callTool(t, session, "update_page", map[string]any{
		"slug":              "posts/two",
		"expected_revision": revision,
		"tags":              []any{"Debug"},
	})
	if update.IsError {
		t.Fatalf("update_page returned error: %v", update.Content)
	}

	after := callTool(t, session, "get_site_health", map[string]any{})
	if after.IsError {
		t.Fatalf("get_site_health after update returned error: %v", after.Content)
	}
	afterData := decodeContent(t, after)
	if hasTaxonomyPair(t, afterData["taxonomy_inconsistency_details"], "Debug", "debug") {
		t.Fatalf("Debug/debug inconsistency still reported after update_page fixed it: %#v", afterData["taxonomy_inconsistency_details"])
	}
}

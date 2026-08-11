package server_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/server"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestAllToolsHaveAnnotations is the enforcement mechanism for #782 Phase
// 3's Directory-submission checklist item ("every tool sets readOnlyHint
// and/or destructiveHint"). It lists every tool registered on the stdio
// server (the write-scope superset — stdio grants write unconditionally,
// see NewStdio) via a real in-memory MCP client/server round trip, so it
// exercises the same wire path a Directory reviewer's client would, not
// just the Go struct literals. Any future tool added without an
// Annotations block, or with the wrong ReadOnlyHint/DestructiveHint for
// its name pattern, fails this test — turning an easy-to-miss submission
// requirement into an enforced invariant instead of a one-time audit.
func TestAllToolsHaveAnnotations(t *testing.T) {
	cfg := config.Default()
	cfg.SiteRoot = copyServerFixtureTree(t, filepath.Join("..", "..", "testdata", "fixtures", "public", "minimal"))
	cfg.HugoRoot = t.TempDir()
	cfg.ContentRoot = filepath.Join("..", "..", "testdata", "fixtures", "content")
	cfg.OAuth.Enabled = false

	idx, err := site.NewIndex(cfg)
	if err != nil {
		t.Fatalf("NewIndex() error = %v", err)
	}
	srv, err := server.NewStdio(cfg, idx)
	if err != nil {
		t.Fatalf("NewStdio() error = %v", err)
	}

	serverTransport, clientTransport := mcp.NewInMemoryTransports()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serverSession, err := srv.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server Connect() error = %v", err)
	}
	defer serverSession.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "annotations-audit", Version: "test"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client Connect() error = %v", err)
	}
	defer clientSession.Close()

	res, err := clientSession.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	if len(res.Tools) == 0 {
		t.Fatal("ListTools() returned no tools at all — test fixture problem, not a pass")
	}

	// delete_page/delete_page_asset are the only tools sharing the
	// "destructive" rate-limit bucket (see rate_limits.go); every other
	// mutation tool, including rollback_change, shares the non-destructive
	// create_update_upload bucket and is annotated DestructiveHint=false
	// accordingly — restoring a prior revision is recoverable, not
	// data-destroying, in this server's own model.
	knownDestructive := map[string]bool{
		"delete_page":       true,
		"delete_page_asset": true,
		"delete_bundle":     true,
	}

	for _, tool := range res.Tools {
		if tool.Annotations == nil {
			t.Errorf("tool %q has no Annotations block", tool.Name)
			continue
		}

		isReadNamed := strings.HasPrefix(tool.Name, "get_") ||
			strings.HasPrefix(tool.Name, "list_") ||
			strings.HasPrefix(tool.Name, "search_") ||
			strings.HasPrefix(tool.Name, "validate_") ||
			strings.HasPrefix(tool.Name, "explain_") ||
			strings.HasPrefix(tool.Name, "diff_") ||
			strings.HasPrefix(tool.Name, "check_") ||
			strings.HasPrefix(tool.Name, "inspect_") ||
			strings.HasPrefix(tool.Name, "suggest_")

		if isReadNamed && !tool.Annotations.ReadOnlyHint {
			t.Errorf("tool %q looks read-only by name but ReadOnlyHint=false", tool.Name)
		}

		wantDestructive := knownDestructive[tool.Name]
		gotDestructive := tool.Annotations.DestructiveHint != nil && *tool.Annotations.DestructiveHint
		if gotDestructive != wantDestructive {
			t.Errorf("tool %q DestructiveHint = %v, want %v", tool.Name, gotDestructive, wantDestructive)
		}
	}
}

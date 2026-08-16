package write_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/previewstore"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/security"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools/admin"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools/write"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestOrphanedGeneratedAssetRecommendedActionIsDirectlyExecutable is the
// end-to-end regression #1022 asked for: seed an orphaned generated hero
// image, call get_storage_health and read the recommended_action back out
// of the finding, dry-run delete_page_asset with those exact arguments,
// delete for real, then call get_storage_health again and confirm the
// finding is gone. It runs get_storage_health and delete_page_asset against
// the same server/source index (write_test's newTestServer only wires up
// write.Register, so this test registers admin.RegisterStorageHealth on the
// same *mcp.Server too) to prove the recommended action round-trips without
// any extra lookups the caller wouldn't otherwise have.
func TestOrphanedGeneratedAssetRecommendedActionIsDirectlyExecutable(t *testing.T) {
	contentRoot := t.TempDir()
	hugoRoot := t.TempDir()

	pg, err := security.New(contentRoot, true)
	if err != nil {
		t.Fatalf("security.New: %v", err)
	}
	idx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatalf("NewSourceIndex: %v", err)
	}
	cfg := config.Default()
	cfg.ContentRoot = contentRoot
	cfg.HugoRoot = hugoRoot

	heroPath := filepath.Join(hugoRoot, "static", "images", "posts", "orphan-e2e-featured.jpg")
	if err := os.MkdirAll(filepath.Dir(heroPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(heroPath, []byte("hero-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1"}, nil)
	write.Register(s, pg, idx, cfg, nil, nil, nil)
	admin.RegisterStorageHealth(s, cfg, idx, previewstore.New())

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
	defer func() { _ = session.Close() }()

	scan := callTool(t, session, "get_storage_health", map[string]any{})
	if scan.IsError {
		t.Fatalf("get_storage_health failed: %s", marshalContent(t, scan))
	}
	findings := decodeWriteData(t, scan)["findings"].([]any)
	var action map[string]any
	for _, f := range findings {
		fm := f.(map[string]any)
		if fm["code"] == "orphaned_generated_asset" && fm["slug"] == "posts/orphan-e2e" {
			action = fm["recommended_action"].(map[string]any)
		}
	}
	if action == nil {
		t.Fatalf("expected orphaned_generated_asset finding for posts/orphan-e2e; findings=%v", findings)
	}
	args := action["arguments"].(map[string]any)
	if args["expected_sha256"] == "" || args["expected_sha256"] == nil {
		t.Fatalf("recommended_action.arguments missing expected_sha256, not directly executable non-dry-run: %v", args)
	}

	callArgs := map[string]any{
		"scope":           args["scope"],
		"slug":            args["slug"],
		"filename":        args["filename"],
		"expected_sha256": args["expected_sha256"],
	}
	dryRunArgs := map[string]any{}
	for k, v := range callArgs {
		dryRunArgs[k] = v
	}
	dryRunArgs["dry_run"] = true
	dryRun := callTool(t, session, action["recommended_tool"].(string), dryRunArgs)
	if dryRun.IsError {
		t.Fatalf("dry-run delete_page_asset with recommended action failed: %s", marshalContent(t, dryRun))
	}
	if _, err := os.Stat(heroPath); err != nil {
		t.Fatalf("dry_run must not delete the asset: %v", err)
	}

	del := callTool(t, session, action["recommended_tool"].(string), callArgs)
	if del.IsError {
		t.Fatalf("delete_page_asset with recommended action failed: %s", marshalContent(t, del))
	}
	if _, err := os.Stat(heroPath); !os.IsNotExist(err) {
		t.Fatalf("delete_page_asset with recommended action must remove the file: %v", err)
	}

	rescan := callTool(t, session, "get_storage_health", map[string]any{})
	if rescan.IsError {
		t.Fatalf("get_storage_health rescan failed: %s", marshalContent(t, rescan))
	}
	for _, f := range decodeWriteData(t, rescan)["findings"].([]any) {
		fm := f.(map[string]any)
		if fm["code"] == "orphaned_generated_asset" && fm["slug"] == "posts/orphan-e2e" {
			t.Fatalf("orphan finding still present after remediation: %v", fm)
		}
	}
}

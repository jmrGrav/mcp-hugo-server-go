package admin_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/previewstore"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools/admin"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func newStorageHealthServer(t *testing.T, cfg config.Config, srcIdx *hugosite.SourceIndex, store *previewstore.Store) (*mcp.ClientSession, func()) {
	t.Helper()
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1"}, nil)
	admin.RegisterStorageHealth(s, cfg, srcIdx, store)

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

// TestStorageHealthCatchesOrphanedGeneratedAssetAndPreviewResidue is the #861
// regression test: after a synthetic residue scenario (a generated hero image
// whose page was deleted, plus a preview directory with no live backing),
// get_storage_health must surface both as machine-readable findings, must not
// flag the generated asset that still has an owning page, and must never
// delete anything or expose an absolute host path.
func TestStorageHealthCatchesOrphanedGeneratedAssetAndPreviewResidue(t *testing.T) {
	hugoRoot := t.TempDir()
	imagesRoot := filepath.Join(hugoRoot, "static", "images", "posts")
	if err := os.MkdirAll(imagesRoot, 0o755); err != nil {
		t.Fatalf("mkdir images: %v", err)
	}
	// posts/kept: has an owning page → must NOT be flagged.
	// posts/orphan: no owning page → must be flagged.
	for _, slug := range []string{"kept", "orphan"} {
		if err := os.WriteFile(filepath.Join(imagesRoot, slug+admin.HeroImageSuffix), []byte("jpeg"), 0o644); err != nil {
			t.Fatalf("write %s image: %v", slug, err)
		}
	}

	srcIdx, err := hugosite.NewSourceIndex(t.TempDir())
	if err != nil {
		t.Fatalf("NewSourceIndex: %v", err)
	}
	srcIdx.Upsert(hugosite.SourcePage{Slug: "posts/kept", Title: "Kept"})

	// Synthetic preview residue: a mcp-preview-* dir under os.TempDir() with no
	// live store entry. Unique-named so we assert on our own dir, not others.
	store := previewstore.New()
	residueDir, err := os.MkdirTemp(os.TempDir(), "mcp-preview-storagehealthtest-*")
	if err != nil {
		t.Fatalf("mkdir residue: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(residueDir) })

	cfg := config.Default()
	cfg.HugoRoot = hugoRoot

	session, done := newStorageHealthServer(t, cfg, srcIdx, store)
	defer done()

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "get_storage_health", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("CallTool error: %v", err)
	}
	if res.IsError {
		t.Fatalf("get_storage_health returned error")
	}

	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var env map[string]any
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	data, ok := env["data"].(map[string]any)
	if !ok {
		t.Fatalf("data missing: %s", raw)
	}
	if data["auto_delete"] != false {
		t.Errorf("auto_delete = %v, want false (advisory-only)", data["auto_delete"])
	}

	findings, _ := data["findings"].([]any)
	var orphanFound, keptFlagged, residueFound bool
	residueRef := filepath.Base(residueDir)
	for _, f := range findings {
		fm, _ := f.(map[string]any)
		switch fm["code"] {
		case "orphaned_generated_asset":
			if fm["slug"] == "posts/orphan" {
				orphanFound = true
			}
			if fm["slug"] == "posts/kept" {
				keptFlagged = true
			}
			if lp, _ := fm["logical_path"].(string); filepath.IsAbs(lp) {
				t.Errorf("orphaned asset finding leaked absolute path %q", lp)
			}
		case "expired_preview_residue":
			if fm["ref"] == residueRef {
				residueFound = true
			}
			if ref, _ := fm["ref"].(string); filepath.IsAbs(ref) {
				t.Errorf("preview residue finding leaked absolute path %q", ref)
			}
		}
	}
	if !orphanFound {
		t.Errorf("expected orphaned_generated_asset finding for posts/orphan; findings=%v", findings)
	}
	if keptFlagged {
		t.Errorf("posts/kept has an owning page and must not be flagged as orphaned")
	}
	if !residueFound {
		t.Errorf("expected expired_preview_residue finding for %q; findings=%v", residueRef, findings)
	}
}

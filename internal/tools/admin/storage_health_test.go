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

// TestStorageHealthDoesNotFlagLegacyFlatHeroImageAsOrphaned is a regression
// test for a false-positive found while auditing production storage_health
// output: generate_hero_image originally wrote flat filenames keyed on the
// bare post slug (static/images/{slug}-featured.jpg), before it started
// nesting section pages under a subdirectory (static/images/{section}/{slug}
// -featured.jpg, e.g. static/images/posts/...). The orphan scanner derived a
// bare slug from the flat legacy filename and looked it up with the exact,
// section-qualified GetBySlug key, so every pre-existing flat hero image for
// a page under a section (i.e. nearly every real post) was misreported as
// orphaned residue — while a hero image with no owning page at all, flat or
// nested, must still be flagged.
func TestStorageHealthDoesNotFlagLegacyFlatHeroImageAsOrphaned(t *testing.T) {
	hugoRoot := t.TempDir()
	imagesRoot := filepath.Join(hugoRoot, "static", "images")
	if err := os.MkdirAll(imagesRoot, 0o755); err != nil {
		t.Fatalf("mkdir images: %v", err)
	}
	// legacy-live: flat filename for a page that exists under content/posts/
	// → must NOT be flagged, even though its bare slug isn't the index key.
	// legacy-gone: flat filename with no owning page anywhere → must still
	// be flagged as genuinely orphaned.
	for _, slug := range []string{"legacy-live", "legacy-gone"} {
		if err := os.WriteFile(filepath.Join(imagesRoot, slug+admin.HeroImageSuffix), []byte("jpeg"), 0o644); err != nil {
			t.Fatalf("write %s image: %v", slug, err)
		}
	}

	srcIdx, err := hugosite.NewSourceIndex(t.TempDir())
	if err != nil {
		t.Fatalf("NewSourceIndex: %v", err)
	}
	srcIdx.Upsert(hugosite.SourcePage{Slug: "posts/legacy-live", Title: "Legacy live"})

	cfg := config.Default()
	cfg.HugoRoot = hugoRoot

	session, done := newStorageHealthServer(t, cfg, srcIdx, previewstore.New())
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
	data, _ := env["data"].(map[string]any)
	findings, _ := data["findings"].([]any)

	var liveFlagged, goneFound bool
	for _, f := range findings {
		fm, _ := f.(map[string]any)
		if fm["code"] != "orphaned_generated_asset" {
			continue
		}
		switch fm["slug"] {
		case "legacy-live":
			liveFlagged = true
		case "legacy-gone":
			goneFound = true
		}
	}
	if liveFlagged {
		t.Errorf("flat legacy hero image for an existing section page must not be flagged as orphaned; findings=%v", findings)
	}
	if !goneFound {
		t.Errorf("expected orphaned_generated_asset finding for legacy-gone; findings=%v", findings)
	}
}

// Regression for #912's remaining scope: a hero file whose basename no longer
// matches its owning slug (historical rename / manual asset attachment) must
// still be treated as live when a page explicitly references it via
// featuredImage, and png variants must be scanned by the same logic.
func TestStorageHealthHonorsExplicitFrontmatterHeroReferenceIncludingPNG(t *testing.T) {
	hugoRoot := t.TempDir()
	imagesRoot := filepath.Join(hugoRoot, "static", "images")
	if err := os.MkdirAll(imagesRoot, 0o755); err != nil {
		t.Fatalf("mkdir images: %v", err)
	}
	if err := os.WriteFile(filepath.Join(imagesRoot, "chatgpt-job-featured.png"), []byte("png"), 0o644); err != nil {
		t.Fatalf("write image: %v", err)
	}

	srcIdx, err := hugosite.NewSourceIndex(t.TempDir())
	if err != nil {
		t.Fatalf("NewSourceIndex: %v", err)
	}
	srcIdx.Upsert(hugosite.SourcePage{
		Slug:     "posts/chatgpt-took-the-lead",
		Title:    "ChatGPT Took The Lead",
		FilePath: filepath.Join(t.TempDir(), "unused.md"),
		FrontmatterRaw: map[string]any{
			"featuredImage": "/images/chatgpt-job-featured.png",
		},
	})

	cfg := config.Default()
	cfg.HugoRoot = hugoRoot
	session, done := newStorageHealthServer(t, cfg, srcIdx, previewstore.New())
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
	data, _ := env["data"].(map[string]any)
	findings, _ := data["findings"].([]any)
	for _, f := range findings {
		fm, _ := f.(map[string]any)
		if fm["logical_path"] == "static/images/chatgpt-job-featured.png" {
			t.Fatalf("explicitly referenced png hero image must not be flagged orphaned; findings=%v", findings)
		}
	}
}

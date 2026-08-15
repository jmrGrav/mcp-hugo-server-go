package admin_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/buildinfo"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/buildstatus"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/db"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools/admin"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func runGitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestGetRuntimeStatusReportsHugoAndGitAvailability(t *testing.T) {
	buildstatus.ResetForTest()
	t.Cleanup(buildstatus.ResetForTest)
	origChannel := buildinfo.BuildChannel
	buildinfo.BuildChannel = "main"
	t.Cleanup(func() {
		buildinfo.BuildChannel = origChannel
	})

	hugoDir := writeMockHugo(t, "#!/bin/sh\necho 'hugo v0.150.0+extended linux/amd64 BuildDate=2026-07-01T00:00:00Z VendorInfo=gohugoio'\n")
	t.Setenv("PATH", hugoDir+":"+os.Getenv("PATH"))

	root := t.TempDir()
	contentRoot := filepath.Join(root, "content")
	if err := os.MkdirAll(contentRoot, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	runGitCmd(t, root, "init")
	runGitCmd(t, root, "config", "user.email", "test@example.test")
	runGitCmd(t, root, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(contentRoot, ".gitkeep"), []byte(""), 0o644); err != nil {
		t.Fatalf("write seed: %v", err)
	}
	runGitCmd(t, root, "add", ".")
	runGitCmd(t, root, "commit", "-m", "initial")

	cfg := config.Default()
	cfg.ContentRoot = contentRoot
	cfg.SiteRoot = t.TempDir()
	cfg.HugoRoot = hugoDir

	session, done := newTestServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "get_runtime_status", map[string]any{"include_revisions": true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error: %s", resultText(res))
	}
	out := decodeStructuredResult(t, res)
	data, ok := out["data"].(map[string]any)
	if !ok {
		t.Fatalf("data type = %T", out["data"])
	}

	hugo, ok := data["hugo"].(map[string]any)
	if !ok {
		t.Fatalf("hugo field type = %T", data["hugo"])
	}
	if hugo["available"] != true {
		t.Fatalf("hugo.available = %v, want true", hugo["available"])
	}
	if hugo["version"] != "0.150.0" {
		t.Fatalf("hugo.version = %v, want 0.150.0", hugo["version"])
	}
	if hugo["extended"] != true {
		t.Fatalf("hugo.extended = %v, want true", hugo["extended"])
	}

	git, ok := data["git"].(map[string]any)
	if !ok {
		t.Fatalf("git field type = %T", data["git"])
	}
	if git["available"] != true {
		t.Fatalf("git.available = %v, want true", git["available"])
	}
	if git["baseline_mode"] != "auto" {
		t.Fatalf("git.baseline_mode = %v, want auto", git["baseline_mode"])
	}
	if got, ok := git["head_commit"].(string); !ok || got == "" {
		t.Fatalf("git.head_commit = %v, want non-empty", git["head_commit"])
	}
	if git["dirty"] != false {
		t.Fatalf("git.dirty = %v, want false", git["dirty"])
	}
	// Absolute host paths must never be exposed.
	if _, present := git["root"]; present {
		t.Fatal("git.root must not be exposed (would leak host filesystem layout)")
	}

	site, ok := data["site"].(map[string]any)
	if !ok {
		t.Fatalf("site field type = %T", data["site"])
	}
	if site["content_root_configured"] != true {
		t.Fatalf("site.content_root_configured = %v, want true", site["content_root_configured"])
	}
	if got, ok := site["source_revision"].(string); !ok || got == "" {
		t.Fatalf("site.source_revision = %v, want non-empty", site["source_revision"])
	}
	if got, ok := data["process_started_at"].(string); !ok || got == "" {
		t.Fatalf("process_started_at = %v, want RFC3339 timestamp", data["process_started_at"])
	}
	if data["last_build_persistence"] != "process_memory" {
		t.Fatalf("last_build_persistence = %v, want process_memory", data["last_build_persistence"])
	}
	if data["source_ahead_of_public"] != false {
		t.Fatalf("source_ahead_of_public = %v, want false for clean source", data["source_ahead_of_public"])
	}
	if data["source_ahead_reason"] != "none" {
		t.Fatalf("source_ahead_reason = %v, want none for clean source", data["source_ahead_reason"])
	}
	if data["publication_state"] != "clean" {
		t.Fatalf("publication_state = %v, want clean for clean source", data["publication_state"])
	}
	if data["unpublished_changes_count"] != float64(0) {
		t.Fatalf("unpublished_changes_count = %v, want 0", data["unpublished_changes_count"])
	}

	if degraded, present := out["data"].(map[string]any)["degraded"]; present {
		t.Fatalf("expected no degraded surfaces when hugo+git are both available, got %v", degraded)
	}
	if got, ok := data["release_version"].(string); !ok || got == "" {
		t.Fatalf("release_version = %v, want non-empty (#563)", data["release_version"])
	}
	if got := data["build_channel"]; got != "main" {
		t.Fatalf("build_channel = %v, want main", got)
	}
}

func TestGetRuntimeStatusUsesPersistedPublicationManifestAfterRestart(t *testing.T) {
	buildstatus.ResetForTest()
	t.Cleanup(buildstatus.ResetForTest)
	d, err := db.Open(filepath.Join(t.TempDir(), "site.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	observed := time.Date(2026, time.August, 14, 10, 11, 12, 0, time.UTC)
	if err := d.RecordPublicationManifest(db.PublicationManifest{
		BuildID:        "20260814-101112-abcd",
		SourceRevision: "sha256:source",
		OutputRevision: "sha256:public",
		HugoVersion:    "hugo v0.164.0+extended",
		Status:         "ok",
		ObservedAt:     observed,
	}); err != nil {
		t.Fatalf("RecordPublicationManifest: %v", err)
	}

	cfg := config.Default()
	cfg.HugoRoot = t.TempDir()
	cfg.SiteRoot = t.TempDir()
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1"}, nil)
	admin.RegisterRuntimeStatusWithDB(s, cfg, nil, d)
	t1, t2 := mcp.NewInMemoryTransports()
	if _, err := s.Connect(context.Background(), t1, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.1"}, nil)
	session, err := client.Connect(context.Background(), t2, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer session.Close()

	res, err := callTool(t, session, "get_runtime_status", map[string]any{})
	if err != nil || res.IsError {
		t.Fatalf("get_runtime_status error = %v, result = %s", err, resultText(res))
	}
	data := decodeStructuredResult(t, res)["data"].(map[string]any)
	if data["last_build_persistence"] != "sqlite_manifest" {
		t.Fatalf("last_build_persistence = %v, want sqlite_manifest", data["last_build_persistence"])
	}
	lastBuild, ok := data["last_build"].(map[string]any)
	if !ok {
		t.Fatalf("last_build = %T, want persisted manifest", data["last_build"])
	}
	if lastBuild["build_id"] != "20260814-101112-abcd" || lastBuild["status"] != "ok" || lastBuild["at"] != observed.Format(time.RFC3339) {
		t.Fatalf("last_build = %#v, want persisted build fact", lastBuild)
	}
}

func TestGetRuntimeStatusExposesMutationJournalRetentionFacts(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "site.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	now := time.Now().UTC().Truncate(time.Second)
	if err := d.RememberMutation(db.MutationJournalEntry{CallerKey: "caller", Tool: "create_page", Key: "live", RequestHash: "hash", ResultJSON: []byte(`{}`), CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.PruneMutationJournal(time.Hour, now); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.HugoRoot, cfg.SiteRoot = t.TempDir(), t.TempDir()
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1"}, nil)
	admin.RegisterRuntimeStatusWithDB(s, cfg, nil, d)
	t1, t2 := mcp.NewInMemoryTransports()
	if _, err := s.Connect(context.Background(), t1, nil); err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.1"}, nil)
	session, err := client.Connect(context.Background(), t2, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	res, err := callTool(t, session, "get_runtime_status", map[string]any{})
	if err != nil || res.IsError {
		t.Fatalf("get_runtime_status error = %v, result = %s", err, resultText(res))
	}
	journal, ok := decodeStructuredResult(t, res)["data"].(map[string]any)["mutation_journal"].(map[string]any)
	if !ok || journal["active_entries"] != float64(1) || journal["last_pruned_at"] != now.Format(time.RFC3339) || journal["last_pruned_entries"] != float64(0) {
		t.Fatalf("mutation_journal = %#v", journal)
	}
}

func TestGetRuntimeStatusOmitsRevisionsByDefault(t *testing.T) {
	buildstatus.ResetForTest()
	t.Cleanup(buildstatus.ResetForTest)

	hugoDir := writeMockHugo(t, "#!/bin/sh\necho 'hugo v0.150.0 linux/amd64'\n")
	t.Setenv("PATH", hugoDir+":"+os.Getenv("PATH"))

	root := t.TempDir()
	contentRoot := filepath.Join(root, "content")
	if err := os.MkdirAll(contentRoot, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(contentRoot, "page.md"), []byte("content"), 0o644); err != nil {
		t.Fatalf("write page: %v", err)
	}

	cfg := config.Default()
	cfg.ContentRoot = contentRoot
	cfg.SiteRoot = t.TempDir()
	cfg.HugoRoot = hugoDir

	session, done := newTestServer(t, cfg)
	defer done()

	// No include_revisions arg at all: hashing the full content/public
	// trees on every poll would make this "compact status" tool expensive,
	// so it must be opt-in.
	res, err := callTool(t, session, "get_runtime_status", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error: %s", resultText(res))
	}
	out := decodeStructuredResult(t, res)
	data := out["data"].(map[string]any)
	site := data["site"].(map[string]any)
	if _, present := site["source_revision"]; present {
		t.Fatalf("source_revision must be omitted unless include_revisions is set, got %v", site["source_revision"])
	}
	if _, present := site["public_revision"]; present {
		t.Fatalf("public_revision must be omitted unless include_revisions is set, got %v", site["public_revision"])
	}
}

// TestGetRuntimeStatusStateMatrixDirtySourceNoBuildStaysInterpretable is the
// #1030/#1009 state-matrix regression: build_dirty:false, git.dirty:true,
// modified source files, divergent source/public revisions, and no
// last_build, all simultaneously. Before source_ahead_of_public/
// unpublished_changes_count/process_started_at/last_build_persistence
// existed, this combination looked self-contradictory (a "clean" binary
// build report next to a dirty git tree and no build history, with no field
// explaining why). This asserts the new fields resolve that ambiguity.
func TestGetRuntimeStatusStateMatrixDirtySourceNoBuildStaysInterpretable(t *testing.T) {
	buildstatus.ResetForTest()
	t.Cleanup(buildstatus.ResetForTest)
	origDirty := buildinfo.Dirty
	buildinfo.Dirty = false
	t.Cleanup(func() { buildinfo.Dirty = origDirty })

	hugoDir := writeMockHugo(t, "#!/bin/sh\necho 'hugo v0.150.0 linux/amd64'\n")
	t.Setenv("PATH", hugoDir+":"+os.Getenv("PATH"))

	root := t.TempDir()
	contentRoot := filepath.Join(root, "content")
	if err := os.MkdirAll(contentRoot, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	pagePath := filepath.Join(contentRoot, "posts", "x", "index.md")
	if err := os.MkdirAll(filepath.Dir(pagePath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(pagePath, []byte("---\ntitle: X\n---\noriginal\n"), 0o644); err != nil {
		t.Fatalf("write page: %v", err)
	}
	runGitCmd(t, root, "init")
	runGitCmd(t, root, "config", "user.email", "test@example.test")
	runGitCmd(t, root, "config", "user.name", "Test User")
	runGitCmd(t, root, "add", ".")
	runGitCmd(t, root, "commit", "-m", "initial")

	// Modified source file left uncommitted: git.dirty:true, classified
	// content_source, with no build_site call ever made (build_dirty stays
	// the binary's own git-describe cleanliness, unrelated to this content
	// edit, and last_build stays omitted).
	if err := os.WriteFile(pagePath, []byte("---\ntitle: X\n---\nedited, unpublished\n"), 0o644); err != nil {
		t.Fatalf("edit page: %v", err)
	}

	siteRoot := t.TempDir()
	// Divergent source/public revisions: public output exists but predates
	// the source edit above.
	if err := os.WriteFile(filepath.Join(siteRoot, "index.html"), []byte("stale public output"), 0o644); err != nil {
		t.Fatalf("write stale public output: %v", err)
	}

	cfg := config.Default()
	cfg.ContentRoot = contentRoot
	cfg.SiteRoot = siteRoot
	cfg.HugoRoot = hugoDir

	session, done := newTestServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "get_runtime_status", map[string]any{"include_revisions": true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error: %s", resultText(res))
	}
	out := decodeStructuredResult(t, res)
	data := out["data"].(map[string]any)

	if data["build_dirty"] != false {
		t.Fatalf("build_dirty = %v, want false (binary cleanliness, independent of the content edit)", data["build_dirty"])
	}
	git := data["git"].(map[string]any)
	if git["dirty"] != true {
		t.Fatalf("git.dirty = %v, want true (uncommitted content edit)", git["dirty"])
	}
	if _, present := data["last_build"]; present {
		t.Fatalf("last_build = %v, want omitted (build_site never called)", data["last_build"])
	}
	site := data["site"].(map[string]any)
	sourceRev, _ := site["source_revision"].(string)
	publicRev, _ := site["public_revision"].(string)
	if sourceRev == "" || publicRev == "" || sourceRev == publicRev {
		t.Fatalf("source_revision=%q public_revision=%q, want non-empty and divergent", sourceRev, publicRev)
	}

	// Despite build_dirty:false and no last_build looking like "nothing to
	// see here", source_ahead_of_public must say otherwise.
	if data["source_ahead_of_public"] != true {
		t.Fatalf("source_ahead_of_public = %v, want true — dirty content_source changes are pending publication", data["source_ahead_of_public"])
	}
	if data["source_ahead_reason"] != "out_of_band_source_drift" {
		t.Fatalf("source_ahead_reason = %v, want out_of_band_source_drift", data["source_ahead_reason"])
	}
	if data["publication_state"] != "source_drift_only" {
		t.Fatalf("publication_state = %v, want source_drift_only", data["publication_state"])
	}
	if got, ok := data["process_started_at"].(string); !ok || got == "" {
		t.Fatalf("process_started_at = %v, want non-empty timestamp explaining process vs. build-history state", data["process_started_at"])
	}
	if data["last_build_persistence"] != "process_memory" {
		t.Fatalf("last_build_persistence = %v, want process_memory (explains why last_build is omitted rather than looking broken)", data["last_build_persistence"])
	}
}

// TestGetRuntimeStatusStateMatrixGeneratedAssetDriftOnly is the fourth
// #1036 state-matrix case: a generated_hero_image asset changed but
// content/ itself is clean. source_ahead_of_public must stay false (no
// actual source content is pending publication — matching its pre-#1036
// definition exactly, since it only checks the content_source dirty
// class), while the new fields still surface the drift instead of
// reporting a flat "none"/"clean" that would hide it entirely.
func TestGetRuntimeStatusStateMatrixGeneratedAssetDriftOnly(t *testing.T) {
	buildstatus.ResetForTest()
	t.Cleanup(buildstatus.ResetForTest)
	origDirty := buildinfo.Dirty
	buildinfo.Dirty = false
	t.Cleanup(func() { buildinfo.Dirty = origDirty })

	hugoDir := writeMockHugo(t, "#!/bin/sh\necho 'hugo v0.150.0 linux/amd64'\n")
	t.Setenv("PATH", hugoDir+":"+os.Getenv("PATH"))

	root := t.TempDir()
	contentRoot := filepath.Join(root, "content")
	pagePath := filepath.Join(contentRoot, "posts", "x", "index.md")
	if err := os.MkdirAll(filepath.Dir(pagePath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(pagePath, []byte("---\ntitle: X\n---\noriginal\n"), 0o644); err != nil {
		t.Fatalf("write page: %v", err)
	}
	// static/images/ is part of the committed baseline (a real Hugo site
	// scaffold would already have this directory) so that the new hero
	// image below shows up as its own untracked file in git status
	// --porcelain — an entirely new, never-before-seen directory is
	// instead collapsed by git into a single "?? static/" line, which
	// would misclassify as external_unknown and defeat this test's setup.
	placeholderPath := filepath.Join(root, "static", "images", ".gitkeep")
	if err := os.MkdirAll(filepath.Dir(placeholderPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(placeholderPath, nil, 0o644); err != nil {
		t.Fatalf("write placeholder: %v", err)
	}
	runGitCmd(t, root, "init")
	runGitCmd(t, root, "config", "user.email", "test@example.test")
	runGitCmd(t, root, "config", "user.name", "Test User")
	runGitCmd(t, root, "add", ".")
	runGitCmd(t, root, "commit", "-m", "initial")

	// Only a generated hero image changes, uncommitted — content/ itself
	// stays untouched, so this must classify as generated_asset, not
	// content_source.
	imagePath := filepath.Join(root, "static", "images", "posts-x-featured.jpg")
	if err := os.WriteFile(imagePath, []byte("fake jpeg bytes"), 0o644); err != nil {
		t.Fatalf("write generated image: %v", err)
	}

	cfg := config.Default()
	cfg.ContentRoot = contentRoot
	cfg.SiteRoot = t.TempDir()
	cfg.HugoRoot = hugoDir

	session, done := newTestServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "get_runtime_status", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error: %s", resultText(res))
	}
	out := decodeStructuredResult(t, res)
	data := out["data"].(map[string]any)

	git := data["git"].(map[string]any)
	if git["dirty"] != true {
		t.Fatalf("git.dirty = %v, want true (uncommitted generated asset)", git["dirty"])
	}
	dirtyClasses, _ := git["dirty_classes"].([]any)
	if len(dirtyClasses) != 1 || dirtyClasses[0] != "generated_asset" {
		t.Fatalf("git.dirty_classes = %v, want [generated_asset]", dirtyClasses)
	}
	if data["source_ahead_of_public"] != false {
		t.Fatalf("source_ahead_of_public = %v, want false — generated-asset-only drift is not source content pending publication", data["source_ahead_of_public"])
	}
	if data["source_ahead_reason"] != "generated_asset_drift" {
		t.Fatalf("source_ahead_reason = %v, want generated_asset_drift", data["source_ahead_reason"])
	}
	if data["publication_state"] != "generated_asset_drift" {
		t.Fatalf("publication_state = %v, want generated_asset_drift", data["publication_state"])
	}
}

func TestGetRuntimeStatusReportsDegradedSurfacesWhenHugoAndGitUnavailable(t *testing.T) {
	buildstatus.ResetForTest()
	t.Cleanup(buildstatus.ResetForTest)

	emptyPathDir := t.TempDir() // no hugo binary here
	t.Setenv("PATH", emptyPathDir)

	root := t.TempDir()
	contentRoot := filepath.Join(root, "content") // no .git anywhere
	if err := os.MkdirAll(contentRoot, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	cfg := config.Default()
	cfg.ContentRoot = contentRoot
	cfg.SiteRoot = t.TempDir()
	cfg.HugoRoot = t.TempDir()

	session, done := newTestServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "get_runtime_status", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error: %s", resultText(res))
	}
	out := decodeStructuredResult(t, res)
	data := out["data"].(map[string]any)

	hugo := data["hugo"].(map[string]any)
	if hugo["available"] != false {
		t.Fatalf("hugo.available = %v, want false", hugo["available"])
	}
	if got, _ := hugo["error"].(string); got == "" {
		t.Fatal("expected hugo.error to explain why hugo is unavailable")
	}

	git := data["git"].(map[string]any)
	if git["available"] != false {
		t.Fatalf("git.available = %v, want false", git["available"])
	}

	degraded, ok := data["degraded"].([]any)
	if !ok || len(degraded) != 2 {
		t.Fatalf("degraded = %#v, want two explanatory entries", data["degraded"])
	}
}

func TestGetRuntimeStatusRespectsGitBaselineDisabledMode(t *testing.T) {
	buildstatus.ResetForTest()
	t.Cleanup(buildstatus.ResetForTest)

	hugoDir := writeMockHugo(t, "#!/bin/sh\necho 'hugo v0.150.0 linux/amd64'\n")
	t.Setenv("PATH", hugoDir+":"+os.Getenv("PATH"))

	root := t.TempDir()
	contentRoot := filepath.Join(root, "content")
	if err := os.MkdirAll(contentRoot, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	runGitCmd(t, root, "init")
	runGitCmd(t, root, "config", "user.email", "test@example.test")
	runGitCmd(t, root, "config", "user.name", "Test User")
	runGitCmd(t, root, "add", ".")
	runGitCmd(t, root, "commit", "--allow-empty", "-m", "initial")

	cfg := config.Default()
	cfg.ContentRoot = contentRoot
	cfg.SiteRoot = t.TempDir()
	cfg.HugoRoot = hugoDir
	cfg.GitBaseline.Mode = "disabled"

	session, done := newTestServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "get_runtime_status", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := decodeStructuredResult(t, res)
	data := out["data"].(map[string]any)
	git := data["git"].(map[string]any)
	if git["available"] != false {
		t.Fatalf("git.available = %v, want false when baseline disabled", git["available"])
	}
	if git["baseline_mode"] != "disabled" {
		t.Fatalf("git.baseline_mode = %v, want disabled", git["baseline_mode"])
	}
	errText, _ := git["error"].(string)
	if errText == "" {
		t.Fatal("expected git.error explaining baseline is disabled")
	}
}

// #467: get_runtime_status surfaces the outcome of the most recent
// build_site attempt via last_build, without requiring the caller to invoke
// build_site itself to discover a broken publish pipeline.
func TestGetRuntimeStatusOmitsLastBuildBeforeAnyBuildAttempt(t *testing.T) {
	buildstatus.ResetForTest()
	t.Cleanup(buildstatus.ResetForTest)

	cfg := config.Default()
	cfg.ContentRoot = t.TempDir()
	cfg.SiteRoot = t.TempDir()
	cfg.HugoRoot = t.TempDir()
	cfg.GitBaseline.Mode = "disabled"

	session, done := newTestServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "get_runtime_status", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := decodeStructuredResult(t, res)
	data := out["data"].(map[string]any)
	if _, present := data["last_build"]; present {
		t.Fatalf("last_build = %v, want omitted before any build_site attempt", data["last_build"])
	}
}

func TestGetRuntimeStatusReportsLastBuildFailure(t *testing.T) {
	buildstatus.ResetForTest()
	t.Cleanup(buildstatus.ResetForTest)
	at := time.Now().UTC()
	buildstatus.RecordFailure("permission_denied", at)

	cfg := config.Default()
	cfg.ContentRoot = t.TempDir()
	cfg.SiteRoot = t.TempDir()
	cfg.HugoRoot = t.TempDir()
	cfg.GitBaseline.Mode = "disabled"

	session, done := newTestServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "get_runtime_status", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := decodeStructuredResult(t, res)
	data := out["data"].(map[string]any)
	lastBuild, ok := data["last_build"].(map[string]any)
	if !ok {
		t.Fatalf("last_build type = %T, want present after a recorded failure", data["last_build"])
	}
	if lastBuild["status"] != "failed" {
		t.Fatalf("last_build.status = %v, want failed", lastBuild["status"])
	}
	if lastBuild["error_class"] != "permission_denied" {
		t.Fatalf("last_build.error_class = %v, want permission_denied", lastBuild["error_class"])
	}
	degraded, _ := data["degraded"].([]any)
	found := false
	for _, d := range degraded {
		if s, _ := d.(string); s != "" && s == "build_site: last attempt failed (permission_denied) at "+lastBuild["at"].(string) {
			found = true
		}
	}
	if !found {
		t.Fatalf("degraded = %v, want an entry about the failed last build attempt", degraded)
	}
}

func writeRuntimeStatusTestContentPage(t *testing.T, contentRoot, slug string, expiresAt time.Time, owner string) {
	t.Helper()
	full := filepath.Join(contentRoot, slug, "index.md")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", full, err)
	}
	fm := strings.Join([]string{
		"---",
		"title: Test content",
		"date: 2026-07-26",
		"draft: true",
		"test_content: true",
		"test_content_owner: " + owner,
		"test_content_expires_at: " + expiresAt.UTC().Format(time.RFC3339),
		"---",
		"Body.",
		"",
	}, "\n")
	if err := os.WriteFile(full, []byte(fm), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", full, err)
	}
}

func TestGetRuntimeStatusOmitsOverdueTestContentWhenNoneExpired(t *testing.T) {
	buildstatus.ResetForTest()
	t.Cleanup(buildstatus.ResetForTest)

	contentRoot := t.TempDir()
	writeRuntimeStatusTestContentPage(t, contentRoot, "posts/audit-run-fresh", time.Now().Add(2*time.Hour), "audit-session-42")

	cfg := config.Default()
	cfg.ContentRoot = contentRoot
	cfg.SiteRoot = t.TempDir()
	cfg.HugoRoot = t.TempDir()
	cfg.GitBaseline.Mode = "disabled"

	session, done := newTestServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "get_runtime_status", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := decodeStructuredResult(t, res)
	data := out["data"].(map[string]any)
	site := data["site"].(map[string]any)
	if _, present := site["overdue_test_content"]; present {
		t.Fatalf("site.overdue_test_content = %v, want omitted when no test content is overdue", site["overdue_test_content"])
	}
}

func TestGetRuntimeStatusReportsOverdueTestContent(t *testing.T) {
	buildstatus.ResetForTest()
	t.Cleanup(buildstatus.ResetForTest)

	contentRoot := t.TempDir()
	expiresAt := time.Now().Add(-2 * time.Hour)
	writeRuntimeStatusTestContentPage(t, contentRoot, "posts/audit-run-expired", expiresAt, "audit-session-42")

	cfg := config.Default()
	cfg.ContentRoot = contentRoot
	cfg.SiteRoot = t.TempDir()
	cfg.HugoRoot = t.TempDir()
	cfg.GitBaseline.Mode = "disabled"

	session, done := newTestServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "get_runtime_status", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := decodeStructuredResult(t, res)
	data := out["data"].(map[string]any)
	site := data["site"].(map[string]any)
	overdue, ok := site["overdue_test_content"].([]any)
	if !ok || len(overdue) != 1 {
		t.Fatalf("site.overdue_test_content = %#v, want exactly 1 overdue entry", site["overdue_test_content"])
	}
	entry, ok := overdue[0].(map[string]any)
	if !ok {
		t.Fatalf("overdue entry type = %T", overdue[0])
	}
	if entry["slug"] != "/posts/audit-run-expired/" {
		t.Fatalf("overdue slug = %v, want /posts/audit-run-expired/", entry["slug"])
	}
	if entry["owner"] != "audit-session-42" {
		t.Fatalf("overdue owner = %v, want audit-session-42", entry["owner"])
	}
	if got, _ := entry["expires_at"].(string); got == "" {
		t.Fatalf("overdue expires_at = %v, want non-empty RFC3339 string", entry["expires_at"])
	}
	if got, ok := entry["overdue_seconds"].(float64); !ok || got < 3600 {
		t.Fatalf("overdue_seconds = %v, want >= 3600", entry["overdue_seconds"])
	}
	if entry["reason"] != "test_content_expired" {
		t.Fatalf("overdue reason = %v, want test_content_expired", entry["reason"])
	}
}

func TestGetRuntimeStatusReportsChangedFilesCountWhenClean(t *testing.T) {
	buildstatus.ResetForTest()
	t.Cleanup(buildstatus.ResetForTest)

	hugoDir := writeMockHugo(t, "#!/bin/sh\necho 'hugo v0.150.0 linux/amd64'\n")
	t.Setenv("PATH", hugoDir+":"+os.Getenv("PATH"))

	root := t.TempDir()
	contentRoot := filepath.Join(root, "content")
	if err := os.MkdirAll(contentRoot, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	runGitCmd(t, root, "init")
	runGitCmd(t, root, "config", "user.email", "test@example.test")
	runGitCmd(t, root, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(contentRoot, ".gitkeep"), []byte(""), 0o644); err != nil {
		t.Fatalf("write seed: %v", err)
	}
	runGitCmd(t, root, "add", ".")
	runGitCmd(t, root, "commit", "-m", "initial")

	cfg := config.Default()
	cfg.ContentRoot = contentRoot
	cfg.SiteRoot = t.TempDir()
	cfg.HugoRoot = hugoDir

	session, done := newTestServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "get_runtime_status", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error: %s", resultText(res))
	}
	out := decodeStructuredResult(t, res)
	data := out["data"].(map[string]any)

	git, ok := data["git"].(map[string]any)
	if !ok {
		t.Fatalf("git field type = %T", data["git"])
	}
	if git["dirty"] != false {
		t.Fatalf("git.dirty = %v, want false for clean repo", git["dirty"])
	}
	// For a clean repo, changed_files_count should be omitted (via omitempty when 0)
	if _, present := git["changed_files_count"]; present {
		t.Fatalf("git.changed_files_count = %v, want omitted for clean repo", git["changed_files_count"])
	}
	if data["site_worktree_dirty"] != false {
		t.Fatalf("site_worktree_dirty = %v, want false for clean repo", data["site_worktree_dirty"])
	}
	if data["binary_build_dirty"] != data["build_dirty"] {
		t.Fatalf("binary_build_dirty = %v must mirror build_dirty = %v", data["binary_build_dirty"], data["build_dirty"])
	}
}

func TestGetRuntimeStatusReportsChangedFilesCountWhenDirty(t *testing.T) {
	buildstatus.ResetForTest()
	t.Cleanup(buildstatus.ResetForTest)

	hugoDir := writeMockHugo(t, "#!/bin/sh\necho 'hugo v0.150.0 linux/amd64'\n")
	t.Setenv("PATH", hugoDir+":"+os.Getenv("PATH"))

	root := t.TempDir()
	contentRoot := filepath.Join(root, "content")
	if err := os.MkdirAll(contentRoot, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	runGitCmd(t, root, "init")
	runGitCmd(t, root, "config", "user.email", "test@example.test")
	runGitCmd(t, root, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(contentRoot, ".gitkeep"), []byte(""), 0o644); err != nil {
		t.Fatalf("write seed: %v", err)
	}
	runGitCmd(t, root, "add", ".")
	runGitCmd(t, root, "commit", "-m", "initial")

	// Create 3 modified files to make repo dirty
	if err := os.WriteFile(filepath.Join(contentRoot, "file1.md"), []byte("content 1"), 0o644); err != nil {
		t.Fatalf("write file1: %v", err)
	}
	runGitCmd(t, root, "add", "content/file1.md")
	runGitCmd(t, root, "commit", "-m", "add file1")

	// Now modify it and add two more untracked files
	if err := os.WriteFile(filepath.Join(contentRoot, "file1.md"), []byte("modified content 1"), 0o644); err != nil {
		t.Fatalf("modify file1: %v", err)
	}
	if err := os.WriteFile(filepath.Join(contentRoot, "file2.md"), []byte("content 2"), 0o644); err != nil {
		t.Fatalf("write file2: %v", err)
	}
	if err := os.WriteFile(filepath.Join(contentRoot, "file3.md"), []byte("content 3"), 0o644); err != nil {
		t.Fatalf("write file3: %v", err)
	}

	cfg := config.Default()
	cfg.ContentRoot = contentRoot
	cfg.SiteRoot = t.TempDir()
	cfg.HugoRoot = hugoDir

	session, done := newTestServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "get_runtime_status", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error: %s", resultText(res))
	}
	out := decodeStructuredResult(t, res)
	data := out["data"].(map[string]any)

	git, ok := data["git"].(map[string]any)
	if !ok {
		t.Fatalf("git field type = %T", data["git"])
	}
	if git["dirty"] != true {
		t.Fatalf("git.dirty = %v, want true for dirty repo", git["dirty"])
	}
	// For a dirty repo with 3 changed files, changed_files_count should be 3
	count, ok := git["changed_files_count"].(float64)
	if !ok {
		t.Fatalf("git.changed_files_count type = %T, want float64", git["changed_files_count"])
	}
	if int(count) != 3 {
		t.Fatalf("git.changed_files_count = %v, want 3", int(count))
	}
	if data["site_worktree_dirty"] != true {
		t.Fatalf("site_worktree_dirty = %v, want true for dirty repo", data["site_worktree_dirty"])
	}
}

package db_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/db"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
)

func writeBuildSource(t *testing.T, root, name, lang, title string) string {
	t.Helper()
	dir := filepath.Join(root, "posts", "demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	body := "---\ntitle: " + title + "\nlang: " + lang + "\ndraft: false\n---\nBody " + title + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestBuildRunsDetectExternalMultilingualChangesAcrossRestart(t *testing.T) {
	contentRoot := t.TempDir()
	frPath := writeBuildSource(t, contentRoot, "index.fr.md", "fr", "FR A")
	enPath := writeBuildSource(t, contentRoot, "index.en.md", "en", "EN A")
	dbPath := filepath.Join(t.TempDir(), "state.sqlite")

	idx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	changes, err := d.BeginBuildRun("build-a", nil, idx, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 2 {
		t.Fatalf("initial changes = %#v, want two translations", changes)
	}
	if err := d.CompleteBuildRun(db.PublicationManifest{BuildID: "build-a", SourceRevision: "source-a", OutputRevision: "public-a", Status: "ok", ObservedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}

	// Both changes happen outside MCP while the service is down.
	writeBuildSource(t, contentRoot, "index.fr.md", "fr", "FR B")
	if err := os.Remove(enPath); err != nil {
		t.Fatal(err)
	}
	idx, err = hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatal(err)
	}
	d, err = db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	reconciled, err := d.ReconcileLatestBuild(nil, idx)
	if err != nil {
		t.Fatal(err)
	}
	if reconciled == nil || reconciled.SourceDriftCount != 2 {
		t.Fatalf("restart reconciliation = %#v, want two source drifts", reconciled)
	}
	changes, err = d.BeginBuildRun("build-b", nil, idx, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 2 {
		t.Fatalf("post-restart changes = %#v, want edit plus deletion", changes)
	}
	var edited, deleted bool
	for _, change := range changes {
		if change.Lang == "fr" && !change.Deleted {
			edited = true
		}
		if change.Lang == "en" && change.Deleted {
			deleted = true
		}
	}
	if !edited || !deleted {
		t.Fatalf("post-restart changes = %#v, want FR edit and EN deletion", changes)
	}
	if err := d.CompleteBuildRun(db.PublicationManifest{BuildID: "build-b", SourceRevision: "source-b", OutputRevision: "public-b", Status: "ok", ObservedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	reconciled, err = d.ReconcileLatestBuild(nil, idx)
	if err != nil {
		t.Fatal(err)
	}
	if reconciled.SourceDriftCount != 0 {
		t.Fatalf("completed build source drift = %d, want 0", reconciled.SourceDriftCount)
	}
	if _, err := os.Stat(frPath); err != nil {
		t.Fatal(err)
	}
}

func TestBuildRunFailureIsDurableAndDoesNotReplaceLatestCompletedRun(t *testing.T) {
	contentRoot := t.TempDir()
	writeBuildSource(t, contentRoot, "index.fr.md", "fr", "FR")
	idx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.BeginBuildRun("ok-run", nil, idx, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := d.CompleteBuildRun(db.PublicationManifest{BuildID: "ok-run", SourceRevision: "s", OutputRevision: "p", Status: "ok", ObservedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.BeginBuildRun("failed-run", nil, idx, time.Now().UTC().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := d.FailBuildRun("failed-run", "failed:hugo", time.Now().UTC().Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	latest, err := d.LatestBuildRun()
	if err != nil {
		t.Fatal(err)
	}
	if latest == nil || latest.BuildID != "ok-run" {
		t.Fatalf("latest completed run = %#v, want ok-run", latest)
	}
}

func TestBuildRunRetriesUnchangedSourceWhenPublicOutputIsMissing(t *testing.T) {
	contentRoot := t.TempDir()
	writeBuildSource(t, contentRoot, "index.fr.md", "fr", "FR")
	idx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.BeginBuildRun("missing-a", nil, idx, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := d.CompleteBuildRun(db.PublicationManifest{BuildID: "missing-a", SourceRevision: "s", OutputRevision: "p", Status: "ok", ObservedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	changes, err := d.BeginBuildRun("missing-b", nil, idx, time.Now().UTC().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].SourceKey != "posts/demo" || changes[0].Lang != "fr" || changes[0].Deleted {
		t.Fatalf("missing-public repair changes = %#v, want unchanged FR source included", changes)
	}
}

func TestBuildRunIncludesIdenticalSourceRestoredAfterCompletedDeletion(t *testing.T) {
	contentRoot := t.TempDir()
	path := writeBuildSource(t, contentRoot, "index.fr.md", "fr", "FR")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	idx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.BeginBuildRun("restore-a", nil, idx, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := d.CompleteBuildRun(db.PublicationManifest{BuildID: "restore-a", SourceRevision: "s-a", OutputRevision: "p-a", Status: "ok", ObservedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	idx, err = hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatal(err)
	}
	changes, err := d.BeginBuildRun("restore-delete", nil, idx, time.Now().UTC().Add(time.Second))
	if err != nil || len(changes) != 1 || !changes[0].Deleted {
		t.Fatalf("delete changes=%#v err=%v", changes, err)
	}
	if err := d.CompleteBuildRun(db.PublicationManifest{BuildID: "restore-delete", SourceRevision: "s-delete", OutputRevision: "p-delete", Status: "ok", ObservedAt: time.Now().UTC().Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}
	idx, err = hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatal(err)
	}
	changes, err = d.BeginBuildRun("restore-b", nil, idx, time.Now().UTC().Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Deleted || changes[0].SourceKey != "posts/demo" || changes[0].Lang != "fr" {
		t.Fatalf("identical restored-source changes=%#v, want FR recreation", changes)
	}
}

func TestBuildRunReconciliationDetectsPublicFingerprintDrift(t *testing.T) {
	contentRoot := t.TempDir()
	path := writeBuildSource(t, contentRoot, "index.fr.md", "fr", "FR")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw = []byte(strings.Replace(string(raw), "draft: false", "draft: false\nurl: /fr/posts/demo/", 1))
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	srcIdx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.DefaultLanguage = "en"
	publicIdx, err := site.NewIndex(cfg)
	if err != nil {
		t.Fatal(err)
	}
	publicIdx.UpsertPage(site.Page{Slug: "/fr/posts/demo/", Lang: "fr", Title: "FR", RawHTML: "<p>published A</p>"})
	d, err := db.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.BeginBuildRun("public-a", publicIdx, srcIdx, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := d.StartupSync(publicIdx, srcIdx); err != nil {
		t.Fatal(err)
	}
	if err := d.CompleteBuildRun(db.PublicationManifest{BuildID: "public-a", SourceRevision: "s", OutputRevision: "p", HugoVersion: "0.164.0+extended", Status: "ok", ObservedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	clean, err := d.ReconcileLatestBuild(publicIdx, srcIdx)
	if err != nil || clean == nil || clean.SourceDriftCount != 0 || clean.PublicDriftCount != 0 {
		t.Fatalf("clean reconciliation=%+v err=%v", clean, err)
	}
	publicIdx.UpsertPage(site.Page{Slug: "/fr/posts/demo/", Lang: "fr", Title: "FR", RawHTML: "<p>published B out of band</p>"})
	drifted, err := d.ReconcileLatestBuild(publicIdx, srcIdx)
	if err != nil || drifted == nil || drifted.SourceDriftCount != 0 || drifted.PublicDriftCount != 1 {
		t.Fatalf("public drift reconciliation=%+v err=%v", drifted, err)
	}
}

// TestBuildRunReconciliationResolvesRootIndexPublicRepresentation is
// #1174's regression coverage: a root "_index.<lang>.md" source (the
// homepage) must reconcile as published, not report PublicDriftCount>0
// forever. hugosite.SlugFromRel gives "_index.en.md" the literal source
// slug "_index.en" (deliberate, load-bearing elsewhere), but Hugo renders
// it as "/" — before #1174's fix, resolvePublicSource in content_shadow.go
// could never map that literal "_index.en" source slug to the real
// homepage Page (Slug: "/"), so the homepage's public representation
// permanently fell into the unresolved bucket and reconciliation kept
// reporting it missing, no matter how many times the site was rebuilt.
// This is exactly what made get_runtime_status.publication_safety.
// safe_to_publish stay false forever on a real, fully-published site.
func TestBuildRunReconciliationResolvesRootIndexPublicRepresentation(t *testing.T) {
	contentRoot := t.TempDir()
	body := "---\ntitle: Home\nlang: en\ndraft: false\n---\nHome body\n"
	if err := os.WriteFile(filepath.Join(contentRoot, "_index.en.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	srcIdx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.DefaultLanguage = "en"
	publicIdx, err := site.NewIndex(cfg)
	if err != nil {
		t.Fatal(err)
	}
	publicIdx.UpsertPage(site.Page{Slug: "/", Lang: "en", Title: "Home", RawHTML: "<p>Home body</p>"})
	d, err := db.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.BeginBuildRun("home-a", publicIdx, srcIdx, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := d.StartupSync(publicIdx, srcIdx); err != nil {
		t.Fatal(err)
	}
	if err := d.CompleteBuildRun(db.PublicationManifest{BuildID: "home-a", SourceRevision: "s", OutputRevision: "p", HugoVersion: "0.164.0+extended", Status: "ok", ObservedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	reconciled, err := d.ReconcileLatestBuild(publicIdx, srcIdx)
	if err != nil {
		t.Fatal(err)
	}
	if reconciled == nil || reconciled.PublicDriftCount != 0 {
		t.Fatalf("homepage reconciliation = %+v, want PublicDriftCount 0", reconciled)
	}
}

func TestBuildRunValidationAndNoCompletedRun(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if run, err := d.LatestBuildRun(); err != nil || run != nil {
		t.Fatalf("LatestBuildRun empty=%+v err=%v", run, err)
	}
	if run, err := d.ReconcileLatestBuild(nil, nil); err != nil || run != nil {
		t.Fatalf("ReconcileLatestBuild empty=%+v err=%v", run, err)
	}
	if _, err := d.BeginBuildRun("", nil, nil, time.Time{}); err == nil {
		t.Fatal("BeginBuildRun accepted empty build ID")
	}
	if err := d.CompleteBuildRun(db.PublicationManifest{}); err == nil {
		t.Fatal("CompleteBuildRun accepted empty build ID")
	}
	if err := d.CompleteBuildRun(db.PublicationManifest{BuildID: "missing", Status: "ok"}); err == nil {
		t.Fatal("CompleteBuildRun accepted unknown build ID")
	}
	if err := d.FailBuildRun("", "", time.Time{}); err == nil {
		t.Fatal("FailBuildRun accepted empty build ID")
	}
}

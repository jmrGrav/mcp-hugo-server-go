package db

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
)

func buildRunInternalSource(t *testing.T) *hugosite.SourceIndex {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "posts", "atomic")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.en.md"), []byte("---\ntitle: Atomic\nlang: en\ndraft: false\n---\nBody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	idx, err := hugosite.NewSourceIndex(root)
	if err != nil {
		t.Fatal(err)
	}
	return idx
}

func TestBeginBuildRunRollsBackRunWhenPageSnapshotFails(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.db.Exec(`CREATE TRIGGER reject_build_page BEFORE INSERT ON build_pages BEGIN SELECT RAISE(ABORT, 'injected page snapshot failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.BeginBuildRun("atomic-begin", nil, buildRunInternalSource(t), time.Time{}); err == nil {
		t.Fatal("BeginBuildRun succeeded despite injected build_pages failure")
	}
	var count int
	if err := d.db.QueryRow(`SELECT COUNT(*) FROM build_runs WHERE build_id='atomic-begin'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial build run survived rollback: count=%d err=%v", count, err)
	}
}

func TestCompleteBuildRunRollsBackGlobalStateWhenPageFinalizeFails(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	idx := buildRunInternalSource(t)
	if _, err := d.BeginBuildRun("atomic-complete", nil, idx, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.db.Exec(`CREATE TRIGGER reject_build_page_update BEFORE UPDATE ON build_pages BEGIN SELECT RAISE(ABORT, 'injected page finalize failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := d.CompleteBuildRun(PublicationManifest{BuildID: "atomic-complete", SourceRevision: "s", OutputRevision: "p", Status: "ok"}); err == nil {
		t.Fatal("CompleteBuildRun succeeded despite injected page finalize failure")
	}
	var state string
	if err := d.db.QueryRow(`SELECT state FROM build_runs WHERE build_id='atomic-complete'`).Scan(&state); err != nil || state != "in_progress" {
		t.Fatalf("global run update escaped rollback: state=%q err=%v", state, err)
	}
}

func TestBuildRunClosedDatabaseFailuresRemainObservable(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	idx := buildRunInternalSource(t)
	if _, err := d.BeginBuildRun("closed", nil, idx, time.Time{}); err == nil {
		t.Fatal("BeginBuildRun hid closed database")
	}
	if err := d.CompleteBuildRun(PublicationManifest{BuildID: "closed", Status: "ok"}); err == nil {
		t.Fatal("CompleteBuildRun hid closed database")
	}
	if err := d.FailBuildRun("closed", "", time.Time{}); err == nil {
		t.Fatal("FailBuildRun hid closed database")
	}
	if _, err := d.ReconcileLatestBuild(nil, idx); err == nil {
		t.Fatal("ReconcileLatestBuild hid closed database")
	}
	if _, err := d.LatestBuildRun(); err == nil {
		t.Fatal("LatestBuildRun hid closed database")
	}
}

func TestBuildRunFrontmatterTruthinessAndNilSourceFacts(t *testing.T) {
	for _, tc := range []struct {
		value any
		want  bool
	}{{true, true}, {false, false}, {" TRUE ", true}, {"false", false}, {1, false}, {nil, false}} {
		if got := truthyBuildFrontmatter(tc.value); got != tc.want {
			t.Fatalf("truthyBuildFrontmatter(%#v)=%v want %v", tc.value, got, tc.want)
		}
	}
	if facts := sourceBuildFacts(nil, nil); len(facts) != 0 {
		t.Fatalf("nil source facts=%#v", facts)
	}
}

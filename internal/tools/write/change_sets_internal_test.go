package write

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/db"
)

// TestDefaultChangeSetIDIsUsedWhenOmitted asserts resolve()'s exact
// fallback identity by calling defaultChangeSetID directly, rather than
// hardcoding its "default:<principal>" format as a string literal in a
// black-box test — a literal would silently decouple from this function
// if either mutationCallerKey's "unknown" fallback or this prefix ever
// changed.
func TestDefaultChangeSetIDIsUsedWhenOmitted(t *testing.T) {
	r := newChangeSetRegistry(nil)
	ctx := context.Background()
	now := time.Now().UTC()

	got, err := r.resolve(ctx, "", now)
	if err != nil {
		t.Fatalf("resolve(\"\") error = %v", err)
	}
	want := defaultChangeSetID(mutationCallerKey(ctx))
	if got != want {
		t.Fatalf("resolve(\"\") = %q, want %q", got, want)
	}

	r.recordMutation(got, mutationCallerKey(ctx), "create_page", "posts/legacy-caller", "create", now)
	mutations := r.mutationsFor(got)
	if len(mutations) != 1 || mutations[0].SourceKey != "posts/legacy-caller" {
		t.Fatalf("mutationsFor(%q) = %#v, want exactly one create_page on posts/legacy-caller", got, mutations)
	}
}

// TestRecordMutationWorksWithoutPersistence is the direct regression for
// the review finding that recordMutation used to silently no-op when
// db_path isn't configured (persistent == nil) — #1140's foreign-drift
// computation must have something to read on every deployment shape, not
// only db_path-configured ones.
func TestRecordMutationWorksWithoutPersistence(t *testing.T) {
	r := newChangeSetRegistry(nil)
	now := time.Now().UTC()

	r.recordMutation("cs_test", "principal-a", "create_page", "posts/x", "create", now)

	got := r.mutationsFor("cs_test")
	if len(got) != 1 || got[0].SourceKey != "posts/x" || got[0].Tool != "create_page" {
		t.Fatalf("mutationsFor(\"cs_test\") without db_path = %#v, want exactly one create_page on posts/x", got)
	}
}

// TestResolveRehydratesOwnershipAfterRestart is the direct regression for
// the asymmetry documented on changeSetRegistry: ownership (unlike the
// mutation list) is expected to survive a process restart by rehydrating
// from SQLite on a cache miss. A second, independently-constructed
// registry over the same DB simulates the restart — a fresh process has
// an empty in-memory owners map but the same persistent DB.
func TestResolveRehydratesOwnershipAfterRestart(t *testing.T) {
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()

	before := newChangeSetRegistry(sqlDB)
	ctx := context.Background()
	now := time.Now().UTC()

	id, err := before.create(mutationCallerKey(ctx), now)
	if err != nil {
		t.Fatalf("create() error = %v", err)
	}

	after := newChangeSetRegistry(sqlDB)
	got, err := after.resolve(ctx, id, now)
	if err != nil {
		t.Fatalf("resolve(%q) on a fresh registry over the same DB failed: %v", id, err)
	}
	if got != id {
		t.Fatalf("resolve(%q) after simulated restart = %q, want %q", id, got, id)
	}
}

// TestCreateReturnsIDEvenIfPersistenceFails is the direct regression for
// the review finding that create() returned an error (losing the id) when
// SQLite persistence failed, even though the in-memory registry already
// held the entry — an inconsistent half-persisted, half-not state. A
// closed DB is used to force a persistence failure deterministically.
func TestCreateReturnsIDEvenIfPersistenceFails(t *testing.T) {
	closedDB, err := db.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if err := closedDB.Close(); err != nil {
		t.Fatal(err)
	}
	r := newChangeSetRegistry(closedDB)

	id, err := r.create("principal-a", time.Now().UTC())
	if err != nil {
		t.Fatalf("create() with a failing persistence layer returned an error, want the id anyway: %v", err)
	}
	if id == "" {
		t.Fatal("create() returned an empty id")
	}
}

package changeset

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/caller"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/db"
)

// TestDefaultIDIsUsedWhenOmitted asserts Resolve()'s exact fallback
// identity by calling DefaultID directly, rather than hardcoding its
// "default:<principal>" format as a string literal in a black-box test — a
// literal would silently decouple from this function if either
// caller.MutationKey's "unknown" fallback or this prefix ever changed. Also
// confirms a mutation recorded against that default id actually lands in
// its bucket (#1135 review finding: a prior version of this test only
// checked the id, not that recording against it worked).
func TestDefaultIDIsUsedWhenOmitted(t *testing.T) {
	r := NewRegistry(nil)
	ctx := context.Background()
	now := time.Now().UTC()

	got, err := r.Resolve(ctx, "", now)
	if err != nil {
		t.Fatalf("Resolve(\"\") error = %v", err)
	}
	want := DefaultID(caller.MutationKey(ctx))
	if got != want {
		t.Fatalf("Resolve(\"\") = %q, want %q", got, want)
	}

	r.RecordMutation(got, caller.MutationKey(ctx), "create_page", "posts/legacy-caller", "create", now)
	mutations := r.MutationsFor(got)
	if len(mutations) != 1 || mutations[0].SourceKey != "posts/legacy-caller" {
		t.Fatalf("MutationsFor(%q) = %#v, want exactly one create_page on posts/legacy-caller", got, mutations)
	}
}

// TestRecordMutationWorksWithoutPersistence is the direct regression for
// the #1135 review finding that RecordMutation used to silently no-op when
// db_path isn't configured (persistent == nil) — #1140's foreign-change-set
// lookup must have something to read on every deployment shape, not only
// db_path-configured ones.
func TestRecordMutationWorksWithoutPersistence(t *testing.T) {
	r := NewRegistry(nil)
	now := time.Now().UTC()

	r.RecordMutation("cs_test", "principal-a", "create_page", "posts/x", "create", now)

	got := r.MutationsFor("cs_test")
	if len(got) != 1 || got[0].SourceKey != "posts/x" || got[0].Tool != "create_page" {
		t.Fatalf("MutationsFor(\"cs_test\") without db_path = %#v, want exactly one create_page on posts/x", got)
	}
}

// TestCreateReturnsIDEvenIfPersistenceFails is the direct regression for
// the #1135 review finding that Create() returned an error (losing the id)
// when SQLite persistence failed, even though the in-memory registry
// already held the entry — an inconsistent half-persisted, half-not state.
// A closed DB is used to force a persistence failure deterministically.
func TestCreateReturnsIDEvenIfPersistenceFails(t *testing.T) {
	closedDB, err := db.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if err := closedDB.Close(); err != nil {
		t.Fatal(err)
	}
	r := NewRegistry(closedDB)

	id, err := r.Create("principal-a", time.Now().UTC())
	if err != nil {
		t.Fatalf("Create() with a failing persistence layer returned an error, want the id anyway: %v", err)
	}
	if id == "" {
		t.Fatal("Create() returned an empty id")
	}
}

// TestResolveRehydratesOwnershipAfterRestart is the direct regression for
// the asymmetry documented on Registry: ownership (unlike the mutation
// list) is expected to survive a process restart by rehydrating from
// SQLite on a cache miss. A second, independently-constructed registry over
// the same DB simulates the restart — a fresh process has an empty
// in-memory owners map but the same persistent DB.
func TestResolveRehydratesOwnershipAfterRestart(t *testing.T) {
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()

	before := NewRegistry(sqlDB)
	ctx := context.Background()
	now := time.Now().UTC()

	id, err := before.Create(caller.MutationKey(ctx), now)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	after := NewRegistry(sqlDB)
	got, err := after.Resolve(ctx, id, now)
	if err != nil {
		t.Fatalf("Resolve(%q) on a fresh registry over the same DB failed: %v", id, err)
	}
	if got != id {
		t.Fatalf("Resolve(%q) after simulated restart = %q, want %q", id, got, id)
	}
}

// TestOwnerOfSourceKeyReturnsMostRecentMutation is #1140's direct unit
// coverage for the lookup the foreign-change-set guard relies on: given two
// change-sets that both touched the same source key, the most recently
// recorded mutation's change-set wins, and a source key nothing ever
// touched reports ok=false.
func TestOwnerOfSourceKeyReturnsMostRecentMutation(t *testing.T) {
	r := NewRegistry(nil)
	base := time.Now().UTC()

	r.RecordMutation("cs_a", "principal-a", "create_page", "posts/shared", "create", base)
	r.RecordMutation("cs_b", "principal-a", "update_page", "posts/shared", "update", base.Add(time.Second))

	got, ok := r.OwnerOfSourceKey("posts/shared")
	if !ok || got != "cs_b" {
		t.Fatalf("OwnerOfSourceKey(\"posts/shared\") = (%q, %v), want (\"cs_b\", true) — the later mutation", got, ok)
	}

	if _, ok := r.OwnerOfSourceKey("posts/never-touched"); ok {
		t.Fatal("OwnerOfSourceKey(\"posts/never-touched\") = ok, want false — no mutation ever recorded")
	}
}

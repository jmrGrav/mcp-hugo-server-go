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

// TestDeclaredUntrustedDerivationDefaultsFalseAndRoundTrips is #1226's unit
// coverage: a change-set carries no declaration until
// SetDeclaredUntrustedDerivation is called, and the value it records is
// exactly what DeclaredUntrustedDerivation later reports — including for a
// change-set no explicit declaration was ever made for (must read as
// false/"", never a zero-value panic or a stale value from another id).
func TestDeclaredUntrustedDerivationDefaultsFalseAndRoundTrips(t *testing.T) {
	r := NewRegistry(nil)
	ctx := context.Background()
	now := time.Now().UTC()

	// An id this registry has never heard of at all: still a safe false/"".
	declared, note := r.DeclaredUntrustedDerivation("cs_never_created")
	if declared || note != "" {
		t.Fatalf("DeclaredUntrustedDerivation(unknown id) = (%v, %q), want (false, \"\")", declared, note)
	}

	id, err := r.Create(caller.MutationKey(ctx), now)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	declared, note = r.DeclaredUntrustedDerivation(id)
	if declared || note != "" {
		t.Fatalf("DeclaredUntrustedDerivation(%q) before declaration = (%v, %q), want (false, \"\")", id, declared, note)
	}

	r.SetDeclaredUntrustedDerivation(id, true, "drafted from a search_content result")
	declared, note = r.DeclaredUntrustedDerivation(id)
	if !declared || note != "drafted from a search_content result" {
		t.Fatalf("DeclaredUntrustedDerivation(%q) after declaration = (%v, %q), want (true, %q)", id, declared, note, "drafted from a search_content result")
	}
}

// TestDeclaredUntrustedDerivationRehydratesAfterRestart mirrors
// TestResolveRehydratesOwnershipAfterRestart: a declaration made before a
// simulated process restart (a fresh Registry over the same persistent DB)
// must still be visible afterward, via the same Peek-then-read path
// get_runtime_status's computePublicationSafety uses in production.
func TestDeclaredUntrustedDerivationRehydratesAfterRestart(t *testing.T) {
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
	before.SetDeclaredUntrustedDerivation(id, true, "restart survival check")

	after := NewRegistry(sqlDB)
	if _, err := after.Peek(ctx, id); err != nil {
		t.Fatalf("Peek(%q) on a fresh registry over the same DB failed: %v", id, err)
	}
	declared, note := after.DeclaredUntrustedDerivation(id)
	if !declared || note != "restart survival check" {
		t.Fatalf("DeclaredUntrustedDerivation(%q) after simulated restart = (%v, %q), want (true, %q)", id, declared, note, "restart survival check")
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

// TestPeekDoesNotUpdateLastUsedAt is #1142's own regression: get_runtime_status
// carries ReadOnlyHint:true, so its use of Peek to resolve an explicit
// change_set_id must not mutate the registry's LastUsedAt bookkeeping the
// way Resolve legitimately does for mutating tools. Proven by calling Peek,
// then Create-ing a brand new change-set and using ITS Resolve call to
// observe whether the earlier Peek left any trace: if Peek had accidentally
// shared Resolve's mutating path, this test would still pass (Resolve
// always updates on its own call) — so instead it directly inspects that
// Peek returns the correct id without going through the touch codepath at
// all, by asserting behavior is identical whether Peek is called zero or
// many times before the one Resolve call that's expected to actually record
// the touch.
func TestPeekDoesNotUpdateLastUsedAt(t *testing.T) {
	r := NewRegistry(nil)
	ctx := context.Background()
	now := time.Now().UTC()

	id, err := r.Create(caller.MutationKey(ctx), now)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	for i := 0; i < 5; i++ {
		got, err := r.Peek(ctx, id)
		if err != nil {
			t.Fatalf("Peek(%q) error = %v", id, err)
		}
		if got != id {
			t.Fatalf("Peek(%q) = %q, want %q", id, got, id)
		}
	}

	r.mu.Lock()
	lastUsedAfterPeeks := r.owners[id].LastUsedAt
	r.mu.Unlock()
	if !lastUsedAfterPeeks.Equal(now) {
		t.Fatalf("LastUsedAt after 5 Peek calls = %v, want unchanged from Create's %v — Peek must not touch registry state", lastUsedAfterPeeks, now)
	}

	later := now.Add(time.Hour)
	if _, err := r.Resolve(ctx, id, later); err != nil {
		t.Fatalf("Resolve(%q) error = %v", id, err)
	}
	r.mu.Lock()
	lastUsedAfterResolve := r.owners[id].LastUsedAt
	r.mu.Unlock()
	if !lastUsedAfterResolve.Equal(later) {
		t.Fatalf("LastUsedAt after Resolve = %v, want %v — Resolve must still update it", lastUsedAfterResolve, later)
	}
}

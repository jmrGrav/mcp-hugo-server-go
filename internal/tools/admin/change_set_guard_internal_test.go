package admin

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/changeset"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/oauth"
)

// guardForeignChangeSet is #1140's regression guard for the 2026-08-14
// incident (two agents sharing one OAuth principal, one corrupting the
// other's in-flight edit by building over it) — its own doc comment calls
// this out directly. Previously only exercised incidentally through
// build_site/publish_changes' own end-to-end tests, which — given the
// number of independent branches here (nil skip, multi-id acknowledgment,
// Resolve failure, unowned-vs-foreign-vs-acknowledged pending pages) — left
// most of them untested.

func guardTestCtx(principal string) context.Context {
	return context.WithValue(context.Background(), oauth.CtxPrincipal, principal)
}

func TestGuardForeignChangeSetSkipsWhenUnwired(t *testing.T) {
	if err := guardForeignChangeSet(context.Background(), nil, nil, "", nil); err != nil {
		t.Fatalf("expected nil-skip when changeSets/srcIdx are both nil, got %v", err)
	}
	srcIdx, err := hugosite.NewSourceIndex(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := guardForeignChangeSet(context.Background(), nil, srcIdx, "", nil); err != nil {
		t.Fatalf("expected nil-skip when changeSets is nil, got %v", err)
	}
	if err := guardForeignChangeSet(context.Background(), changeset.NewRegistry(nil), nil, "", nil); err != nil {
		t.Fatalf("expected nil-skip when srcIdx is nil, got %v", err)
	}
}

func TestGuardForeignChangeSetAllowsWhenNoPendingPages(t *testing.T) {
	srcIdx, err := hugosite.NewSourceIndex(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	changeSets := changeset.NewRegistry(nil)
	if err := guardForeignChangeSet(guardTestCtx("principal-a"), changeSets, srcIdx, "", nil); err != nil {
		t.Fatalf("expected no error with no pending pages, got %v", err)
	}
}

func TestGuardForeignChangeSetAllowsUnownedPendingPage(t *testing.T) {
	// A pending page no change-set has recorded a mutation for (the common
	// case right after a process restart) is never "foreign" — see the
	// guard's own doc comment on why this is deliberate, distinct from
	// computePublicationSafety's stricter treatment of the same case.
	srcIdx, err := hugosite.NewSourceIndex(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srcIdx.Upsert(hugosite.SourcePage{Slug: "posts/untracked", BuildPending: true})
	changeSets := changeset.NewRegistry(nil)
	if err := guardForeignChangeSet(guardTestCtx("principal-a"), changeSets, srcIdx, "", nil); err != nil {
		t.Fatalf("expected an unowned pending page to be allowed through, got %v", err)
	}
}

func TestGuardForeignChangeSetAllowsOwnAcknowledgedChangeSet(t *testing.T) {
	srcIdx, err := hugosite.NewSourceIndex(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srcIdx.Upsert(hugosite.SourcePage{Slug: "posts/mine", BuildPending: true})
	changeSets := changeset.NewRegistry(nil)
	now := time.Now().UTC()
	ctx := guardTestCtx("principal-a")
	id, err := changeSets.Create("principal-a", now)
	if err != nil {
		t.Fatal(err)
	}
	changeSets.RecordMutation(id, "principal-a", "update_page", "posts/mine", "update", now)

	if err := guardForeignChangeSet(ctx, changeSets, srcIdx, id, nil); err != nil {
		t.Fatalf("expected the caller's own acknowledged change-set to be allowed, got %v", err)
	}
}

func TestGuardForeignChangeSetBlocksForeignChangeSet(t *testing.T) {
	srcIdx, err := hugosite.NewSourceIndex(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srcIdx.Upsert(hugosite.SourcePage{Slug: "posts/theirs", BuildPending: true})
	changeSets := changeset.NewRegistry(nil)
	now := time.Now().UTC()

	theirID, err := changeSets.Create("principal-b", now)
	if err != nil {
		t.Fatal(err)
	}
	changeSets.RecordMutation(theirID, "principal-b", "update_page", "posts/theirs", "update", now)

	// principal-a builds with its own (different, empty/default) change-set
	// — posts/theirs belongs to principal-b's change-set, never acknowledged.
	err = guardForeignChangeSet(guardTestCtx("principal-a"), changeSets, srcIdx, "", nil)
	if err == nil {
		t.Fatal("expected foreign_change_set_present, got nil")
	}
	if !strings.Contains(err.Error(), "foreign_change_set_present") {
		t.Fatalf("error = %v, want foreign_change_set_present", err)
	}
	if !strings.Contains(err.Error(), "posts/theirs") {
		t.Fatalf("error should name the foreign pending slug, got %v", err)
	}
}

func TestGuardForeignChangeSetChangeSetIDsAcknowledgesMultiple(t *testing.T) {
	// The issue's own escape hatch: two change-sets both have pending work
	// at once (the normal state for two agents genuinely editing
	// concurrently) — passing both in change_set_ids must let both through
	// together, and change_set_ids must win over the singular change_set_id
	// when both are given (not unioned).
	srcIdx, err := hugosite.NewSourceIndex(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srcIdx.Upsert(hugosite.SourcePage{Slug: "posts/a", BuildPending: true})
	srcIdx.Upsert(hugosite.SourcePage{Slug: "posts/b", BuildPending: true})
	changeSets := changeset.NewRegistry(nil)
	now := time.Now().UTC()

	idA, err := changeSets.Create("principal-a", now)
	if err != nil {
		t.Fatal(err)
	}
	changeSets.RecordMutation(idA, "principal-a", "update_page", "posts/a", "update", now)
	idB, err := changeSets.Create("principal-a", now)
	if err != nil {
		t.Fatal(err)
	}
	changeSets.RecordMutation(idB, "principal-a", "update_page", "posts/b", "update", now)

	ctx := guardTestCtx("principal-a")
	// A singular change_set_id acknowledging only idA must still block on
	// posts/b (owned by idB, unacknowledged).
	if err := guardForeignChangeSet(ctx, changeSets, srcIdx, idA, nil); err == nil {
		t.Fatal("expected posts/b (owned by an unacknowledged change-set) to block the build")
	}
	// change_set_ids acknowledging both must let both through.
	if err := guardForeignChangeSet(ctx, changeSets, srcIdx, "", []string{idA, idB}); err != nil {
		t.Fatalf("expected both change-sets acknowledged via change_set_ids to be allowed, got %v", err)
	}
	// change_set_ids wins over change_set_id when both are given: idA alone
	// via change_set_id would normally block on posts/b, but change_set_ids
	// here explicitly only acknowledges idA — still blocks on posts/b,
	// proving change_set_id (idB, which would have also worked) was ignored.
	if err := guardForeignChangeSet(ctx, changeSets, srcIdx, idB, []string{idA}); err == nil {
		t.Fatal("expected change_set_ids to take precedence over change_set_id, still blocking on posts/b")
	}
}

func TestGuardForeignChangeSetPropagatesResolveError(t *testing.T) {
	srcIdx, err := hugosite.NewSourceIndex(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	changeSets := changeset.NewRegistry(nil)
	err = guardForeignChangeSet(guardTestCtx("principal-a"), changeSets, srcIdx, "not-a-real-change-set-id", nil)
	if err == nil {
		t.Fatal("expected an error resolving an unknown change_set_id")
	}
	if !strings.Contains(err.Error(), "invalid_params") {
		t.Fatalf("error = %v, want the invalid_params resolve failure to propagate", err)
	}
}

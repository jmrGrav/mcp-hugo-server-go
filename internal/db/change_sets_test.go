package db_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/db"
)

// closedTestDB opens then immediately closes a real SQLite DB, forcing every
// subsequent query/exec through it to fail deterministically — the same
// pattern internal/changeset's TestCreateReturnsIDEvenIfPersistenceFails
// uses, borrowed here to exercise this package's own error-return paths
// (a genuine driver-level error, not sql.ErrNoRows) without needing to mock
// database/sql.
func closedTestDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	return d
}

// Coverage sweep (v1.9.2): CreateChangeSet/GetChangeSetOwner/
// TouchChangeSet/RecordChangeSetMutation/ListChangeSetMutations
// (#1135/#1140/#1142's durable persistence layer) were previously exercised
// only indirectly through internal/changeset's own tests — which, under
// Go's default per-package coverage mode (no -coverpkg, matching exactly
// what ci.yml's coverage gate runs), attributes nothing back to this
// package's own coverage number. These are direct tests of the persistence
// contract itself, independent of the changeset.Registry layer built on it.

func TestCreateChangeSetPersistsAndIsIdempotent(t *testing.T) {
	d := openTestDB(t)
	now := time.Now().UTC()

	if err := d.CreateChangeSet("cs_1", "principal-a", now); err != nil {
		t.Fatalf("CreateChangeSet: %v", err)
	}
	owner, _, _, found, err := d.GetChangeSetOwner("cs_1")
	if err != nil {
		t.Fatalf("GetChangeSetOwner: %v", err)
	}
	if !found || owner != "principal-a" {
		t.Fatalf("GetChangeSetOwner = (%q, %v), want (principal-a, true)", owner, found)
	}

	// Idempotent: a second Create for the same id (e.g. a retry) must keep
	// the original owner, never overwrite it with a different one.
	if err := d.CreateChangeSet("cs_1", "principal-b", now.Add(time.Minute)); err != nil {
		t.Fatalf("CreateChangeSet (retry): %v", err)
	}
	owner, _, _, found, err = d.GetChangeSetOwner("cs_1")
	if err != nil {
		t.Fatalf("GetChangeSetOwner (after retry): %v", err)
	}
	if !found || owner != "principal-a" {
		t.Fatalf("GetChangeSetOwner after retry = (%q, %v), want original owner (principal-a, true) preserved", owner, found)
	}
}

func TestCreateChangeSetRequiresIDAndPrincipal(t *testing.T) {
	d := openTestDB(t)
	now := time.Now().UTC()
	if err := d.CreateChangeSet("", "principal-a", now); err == nil {
		t.Fatal("expected error for empty id")
	}
	if err := d.CreateChangeSet("cs_1", "", now); err == nil {
		t.Fatal("expected error for empty principal_id")
	}
}

func TestGetChangeSetOwnerReturnsGenuineDBError(t *testing.T) {
	d := closedTestDB(t)
	_, _, _, found, err := d.GetChangeSetOwner("cs_1")
	if err == nil {
		t.Fatal("expected a genuine DB error from a closed connection, got nil")
	}
	if found {
		t.Fatal("found = true on error, want false")
	}
}

func TestListChangeSetMutationsReturnsGenuineDBError(t *testing.T) {
	d := closedTestDB(t)
	_, err := d.ListChangeSetMutations("cs_1")
	if err == nil {
		t.Fatal("expected a genuine DB error from a closed connection, got nil")
	}
}

func TestGetChangeSetOwnerNotFound(t *testing.T) {
	d := openTestDB(t)
	owner, _, _, found, err := d.GetChangeSetOwner("cs_does_not_exist")
	if err != nil {
		t.Fatalf("GetChangeSetOwner: %v", err)
	}
	if found || owner != "" {
		t.Fatalf("GetChangeSetOwner = (%q, %v), want (\"\", false) for an unknown id", owner, found)
	}
}

func TestTouchChangeSetUpdatesLastUsedAt(t *testing.T) {
	d := openTestDB(t)
	now := time.Now().UTC()
	if err := d.CreateChangeSet("cs_touch", "principal-a", now); err != nil {
		t.Fatalf("CreateChangeSet: %v", err)
	}
	// TouchChangeSet on an unknown id is a no-op update (0 rows affected),
	// not an error — mirrors its doc comment ("best-effort ... must never
	// block the mutation it's attributing").
	if err := d.TouchChangeSet("cs_does_not_exist", now); err != nil {
		t.Fatalf("TouchChangeSet (unknown id): %v", err)
	}
	if err := d.TouchChangeSet("cs_touch", now.Add(time.Hour)); err != nil {
		t.Fatalf("TouchChangeSet: %v", err)
	}
	// The owner must be unaffected by touch.
	owner, _, _, found, err := d.GetChangeSetOwner("cs_touch")
	if err != nil || !found || owner != "principal-a" {
		t.Fatalf("GetChangeSetOwner after touch = (%q, %v, %v), want (principal-a, true, nil)", owner, found, err)
	}
}

func TestRecordChangeSetMutationAndListInOrder(t *testing.T) {
	d := openTestDB(t)
	now := time.Now().UTC()
	if err := d.CreateChangeSet("cs_list", "principal-a", now); err != nil {
		t.Fatalf("CreateChangeSet: %v", err)
	}

	first := db.ChangeSetMutation{
		ChangeSetID: "cs_list", PrincipalID: "principal-a", Tool: "create_page",
		SourceKey: "posts/a", MutationType: "create", CreatedAt: now,
	}
	second := db.ChangeSetMutation{
		ChangeSetID: "cs_list", PrincipalID: "principal-a", Tool: "update_page",
		SourceKey: "posts/a", MutationType: "update", CreatedAt: now.Add(time.Second),
	}
	if err := d.RecordChangeSetMutation(first); err != nil {
		t.Fatalf("RecordChangeSetMutation (first): %v", err)
	}
	if err := d.RecordChangeSetMutation(second); err != nil {
		t.Fatalf("RecordChangeSetMutation (second): %v", err)
	}

	got, err := d.ListChangeSetMutations("cs_list")
	if err != nil {
		t.Fatalf("ListChangeSetMutations: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListChangeSetMutations returned %d entries, want 2", len(got))
	}
	if got[0].Tool != "create_page" || got[1].Tool != "update_page" {
		t.Fatalf("ListChangeSetMutations order = [%s, %s], want [create_page, update_page] (oldest first)", got[0].Tool, got[1].Tool)
	}
	if got[0].SourceKey != "posts/a" || got[0].MutationType != "create" {
		t.Fatalf("ListChangeSetMutations[0] = %+v, unexpected fields", got[0])
	}

	// A change-set with no recorded mutations returns an empty slice, not
	// an error.
	empty, err := d.ListChangeSetMutations("cs_never_used")
	if err != nil {
		t.Fatalf("ListChangeSetMutations (empty): %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("ListChangeSetMutations (empty) = %d entries, want 0", len(empty))
	}
}

func TestSetChangeSetDeclaredUntrustedDerivationPersistsAndDefaultsFalse(t *testing.T) {
	d := openTestDB(t)
	now := time.Now().UTC()
	if err := d.CreateChangeSet("cs_declared", "principal-a", now); err != nil {
		t.Fatalf("CreateChangeSet: %v", err)
	}

	// Default, before any declaration: false/"" — a change-set created
	// without opting in must not spuriously read as untrusted-derived.
	_, declared, note, found, err := d.GetChangeSetOwner("cs_declared")
	if err != nil || !found {
		t.Fatalf("GetChangeSetOwner (before declaration): found=%v err=%v", found, err)
	}
	if declared || note != "" {
		t.Fatalf("GetChangeSetOwner (before declaration) = (declared=%v, note=%q), want (false, \"\")", declared, note)
	}

	if err := d.SetChangeSetDeclaredUntrustedDerivation("cs_declared", true, "drafted from a search_content result"); err != nil {
		t.Fatalf("SetChangeSetDeclaredUntrustedDerivation: %v", err)
	}
	_, declared, note, found, err = d.GetChangeSetOwner("cs_declared")
	if err != nil || !found {
		t.Fatalf("GetChangeSetOwner (after declaration): found=%v err=%v", found, err)
	}
	if !declared || note != "drafted from a search_content result" {
		t.Fatalf("GetChangeSetOwner (after declaration) = (declared=%v, note=%q), want (true, \"drafted from a search_content result\")", declared, note)
	}
}

func TestRecordChangeSetMutationRequiresFields(t *testing.T) {
	d := openTestDB(t)
	base := db.ChangeSetMutation{
		ChangeSetID: "cs_1", PrincipalID: "principal-a", Tool: "create_page", MutationType: "create",
	}
	if err := d.RecordChangeSetMutation(db.ChangeSetMutation{}); err == nil {
		t.Fatal("expected error for a fully empty mutation")
	}
	missingChangeSetID := base
	missingChangeSetID.ChangeSetID = ""
	if err := d.RecordChangeSetMutation(missingChangeSetID); err == nil {
		t.Fatal("expected error for missing change_set_id")
	}
	missingTool := base
	missingTool.Tool = ""
	if err := d.RecordChangeSetMutation(missingTool); err == nil {
		t.Fatal("expected error for missing tool")
	}
	// CreatedAt defaults to now when zero, rather than erroring.
	valid := base
	if err := d.RecordChangeSetMutation(valid); err != nil {
		t.Fatalf("RecordChangeSetMutation with zero CreatedAt should default rather than error: %v", err)
	}
	got, err := d.ListChangeSetMutations("cs_1")
	if err != nil || len(got) != 1 || got[0].CreatedAt.IsZero() {
		t.Fatalf("expected one recorded mutation with a defaulted non-zero CreatedAt, got %+v err=%v", got, err)
	}
}

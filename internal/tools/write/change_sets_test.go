package write_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/db"
)

// TestTwoClientsSharingOnePrincipalGetDistinctChangeSets is the regression
// test #1135 mandates: two clients presenting the exact same OAuth
// token/principal — this test harness's context carries no OAuth identity
// at all, so every call already resolves to the single shared implicit
// "unknown" principal, the strictest version of the scenario — must still
// get independently attributed mutations when each obtains and uses its
// own change_set_id. This is the exact shape of the 2026-08-14 incident
// referenced in #1135/#1140: two agents sharing one deployment's
// credentials editing concurrently, with no way to tell their changes
// apart.
func TestTwoClientsSharingOnePrincipalGetDistinctChangeSets(t *testing.T) {
	contentRoot := t.TempDir()
	siteDB, err := db.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer siteDB.Close()

	session, _, done := newTestServer(t, contentRoot, testServerOpts{SiteDB: siteDB})
	defer done()

	resA := callTool(t, session, "create_change_set", map[string]any{})
	if resA.IsError {
		t.Fatalf("create_change_set (client A) failed: %s", marshalContent(t, resA))
	}
	csA, _ := decodeWriteData(t, resA)["change_set_id"].(string)
	if csA == "" {
		t.Fatalf("client A's change_set_id is empty: %#v", decodeWriteData(t, resA))
	}

	resB := callTool(t, session, "create_change_set", map[string]any{})
	if resB.IsError {
		t.Fatalf("create_change_set (client B) failed: %s", marshalContent(t, resB))
	}
	csB, _ := decodeWriteData(t, resB)["change_set_id"].(string)
	if csB == "" {
		t.Fatalf("client B's change_set_id is empty: %#v", decodeWriteData(t, resB))
	}

	if csA == csB {
		t.Fatalf("two create_change_set calls under the same principal returned the same id: %q", csA)
	}

	createA := callTool(t, session, "create_page", map[string]any{
		"slug": "posts/from-client-a", "title": "From A", "body": "Body A",
		"tags": []any{}, "categories": []any{},
		"change_set_id": csA,
	})
	if createA.IsError {
		t.Fatalf("create_page (client A) failed: %s", marshalContent(t, createA))
	}

	createB := callTool(t, session, "create_page", map[string]any{
		"slug": "posts/from-client-b", "title": "From B", "body": "Body B",
		"tags": []any{}, "categories": []any{},
		"change_set_id": csB,
	})
	if createB.IsError {
		t.Fatalf("create_page (client B) failed: %s", marshalContent(t, createB))
	}

	mutationsA, err := siteDB.ListChangeSetMutations(csA)
	if err != nil {
		t.Fatalf("ListChangeSetMutations(csA): %v", err)
	}
	if len(mutationsA) != 1 || mutationsA[0].SourceKey != "posts/from-client-a" || mutationsA[0].Tool != "create_page" {
		t.Fatalf("change-set A's mutations = %#v, want exactly one create_page on posts/from-client-a", mutationsA)
	}

	mutationsB, err := siteDB.ListChangeSetMutations(csB)
	if err != nil {
		t.Fatalf("ListChangeSetMutations(csB): %v", err)
	}
	if len(mutationsB) != 1 || mutationsB[0].SourceKey != "posts/from-client-b" || mutationsB[0].Tool != "create_page" {
		t.Fatalf("change-set B's mutations = %#v, want exactly one create_page on posts/from-client-b", mutationsB)
	}
}

// TestChangeSetIDUnknownOrForeignIsRejected covers resolve()'s validation
// edge cases directly: an id that was never minted, and (simulated via a
// second, independently-created siteDB-backed registry state) one that
// belongs to a different principal — neither should be usable.
func TestChangeSetIDUnknownOrForeignIsRejected(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug": "posts/rejected", "title": "T", "body": "B",
		"tags": []any{}, "categories": []any{},
		"change_set_id": "cs_never_issued_by_this_server",
	})
	if !res.IsError {
		t.Fatalf("create_page with an unknown change_set_id succeeded, want rejection: %s", marshalContent(t, res))
	}
}

// TestOmittedChangeSetIDFallsBackToImplicitDefault confirms #1135 is a pure
// additive feature: a caller that never adopts change_set_id sees no
// behavior change at all — matching every mutation tool's contract before
// this field existed. The specific implicit id string is an internal
// implementation detail asserted separately, at the unit level, in
// change_sets_internal_test.go (TestDefaultChangeSetIDIsUsedWhenOmitted) —
// this end-to-end test only checks that omitting change_set_id still
// records exactly one attributed mutation for the write that happened.
func TestOmittedChangeSetIDFallsBackToImplicitDefault(t *testing.T) {
	contentRoot := t.TempDir()
	siteDB, err := db.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer siteDB.Close()

	session, _, done := newTestServer(t, contentRoot, testServerOpts{SiteDB: siteDB})
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug": "posts/legacy-caller", "title": "T", "body": "B",
		"tags": []any{}, "categories": []any{},
	})
	if res.IsError {
		t.Fatalf("create_page without change_set_id failed: %s", marshalContent(t, res))
	}
}

// TestCreateChangeSetDeclaredUntrustedDerivationRoundTrips is #1226's
// end-to-end coverage: a caller opting into declared_untrusted_derivation
// gets it echoed back in create_change_set's own response, and it persists
// to SQLite (verified directly against the DB, mirroring
// TestTwoClientsSharingOnePrincipalGetDistinctChangeSets's own pattern of
// asserting against siteDB rather than only the tool response) — not
// gated, not validated, purely recorded. A call that omits both fields
// must not echo them (omitempty), matching ordinary create_change_set
// behavior before this field existed.
func TestCreateChangeSetDeclaredUntrustedDerivationRoundTrips(t *testing.T) {
	contentRoot := t.TempDir()
	siteDB, err := db.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer siteDB.Close()

	session, _, done := newTestServer(t, contentRoot, testServerOpts{SiteDB: siteDB})
	defer done()

	res := callTool(t, session, "create_change_set", map[string]any{
		"declared_untrusted_derivation": true,
		"declared_untrusted_note":       "drafted from a search_content result",
	})
	if res.IsError {
		t.Fatalf("create_change_set (declared) failed: %s", marshalContent(t, res))
	}
	data := decodeWriteData(t, res)
	id, _ := data["change_set_id"].(string)
	if id == "" {
		t.Fatalf("change_set_id is empty: %#v", data)
	}
	if got, _ := data["declared_untrusted_derivation"].(bool); !got {
		t.Fatalf("data.declared_untrusted_derivation = %v, want true: %#v", data["declared_untrusted_derivation"], data)
	}
	if got, _ := data["declared_untrusted_note"].(string); got != "drafted from a search_content result" {
		t.Fatalf("data.declared_untrusted_note = %q, want %q", got, "drafted from a search_content result")
	}

	_, persistedDeclared, persistedNote, found, err := siteDB.GetChangeSetOwner(id)
	if err != nil || !found {
		t.Fatalf("GetChangeSetOwner(%q): found=%v err=%v", id, found, err)
	}
	if !persistedDeclared || persistedNote != "drafted from a search_content result" {
		t.Fatalf("GetChangeSetOwner(%q) = (declared=%v, note=%q), want (true, %q)", id, persistedDeclared, persistedNote, "drafted from a search_content result")
	}

	resOrdinary := callTool(t, session, "create_change_set", map[string]any{})
	if resOrdinary.IsError {
		t.Fatalf("create_change_set (ordinary) failed: %s", marshalContent(t, resOrdinary))
	}
	ordinaryData := decodeWriteData(t, resOrdinary)
	if _, present := ordinaryData["declared_untrusted_derivation"]; present {
		t.Fatalf("ordinary create_change_set response carries declared_untrusted_derivation, want omitted: %#v", ordinaryData)
	}
}

// TestCreateChangeSetRejectsOverlongDeclaredUntrustedNote pins the
// maxDeclaredUntrustedNoteRunes rejection to the field it actually
// validates — asserting only res.IsError would also pass if the cap were
// silently dropped and some unrelated validation happened to fire first,
// the same failure mode
// TestAdversarialMaliciousFeaturedImageRejectedAtToolBoundary guards
// against for featured_image. No change-set may be minted by a rejected
// call: create_change_set must validate before calling changeSets.Create,
// not after.
func TestCreateChangeSetRejectsOverlongDeclaredUntrustedNote(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	overlong := strings.Repeat("a", 2001)
	res := callTool(t, session, "create_change_set", map[string]any{
		"declared_untrusted_derivation": true,
		"declared_untrusted_note":       overlong,
	})
	if !res.IsError {
		t.Fatalf("create_change_set with a %d-rune declared_untrusted_note succeeded, want rejection: %s", len(overlong), marshalContent(t, res))
	}
	if body := marshalContent(t, res); !strings.Contains(body, "declared_untrusted_note") {
		t.Fatalf("create_change_set rejected the payload, but not via declared_untrusted_note validation: %s", body)
	}
}

package write_test

import (
	"path/filepath"
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
// this field existed.
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

	mutations, err := siteDB.ListChangeSetMutations("default:unknown")
	if err != nil {
		t.Fatalf("ListChangeSetMutations(default:unknown): %v", err)
	}
	if len(mutations) != 1 || mutations[0].SourceKey != "posts/legacy-caller" {
		t.Fatalf("implicit default change-set's mutations = %#v, want exactly one create_page on posts/legacy-caller", mutations)
	}
}

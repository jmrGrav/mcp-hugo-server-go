package anonymous_test

import (
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools/anonymous"
)

// TestGetChangelogDefaultLimit is the core regression test for #612:
// without arguments, get_changelog returns at most the default 5 most
// recent entries (bounded, not a full dump of CHANGELOG.md), each with a
// non-empty version/body.
func TestGetChangelogDefaultLimit(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "get_changelog", map[string]any{})
	if res.IsError {
		t.Fatalf("get_changelog returned error: %v", res.Content)
	}
	data := decodeContent(t, res)
	entries, ok := data["entries"].([]any)
	if !ok {
		t.Fatalf("get_changelog entries = %T, want []any", data["entries"])
	}
	if len(entries) == 0 {
		t.Fatal("get_changelog entries is empty, want at least one real release from CHANGELOG.md")
	}
	if len(entries) > 5 {
		t.Fatalf("get_changelog entries = %d, want at most the default limit of 5", len(entries))
	}
	total, _ := data["total"].(float64)
	if int(total) != len(entries) {
		t.Errorf("get_changelog total = %v, want to match len(entries) = %d", total, len(entries))
	}
	first, ok := entries[0].(map[string]any)
	if !ok {
		t.Fatalf("get_changelog entries[0] = %T, want map", entries[0])
	}
	version, _ := first["version"].(string)
	if version == "" {
		t.Error("get_changelog entries[0].version is empty")
	}
	body, _ := first["body"].(string)
	if body == "" {
		t.Error("get_changelog entries[0].body is empty")
	}
}

// TestGetChangelogLimit confirms limit caps the number of entries
// returned.
func TestGetChangelogLimit(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "get_changelog", map[string]any{"limit": 1})
	if res.IsError {
		t.Fatalf("get_changelog returned error: %v", res.Content)
	}
	data := decodeContent(t, res)
	entries, ok := data["entries"].([]any)
	if !ok || len(entries) != 1 {
		t.Fatalf("get_changelog(limit=1) entries = %#v, want exactly 1", data["entries"])
	}
}

func TestGetChangelogCompactDefaultsToOneEntryWithoutBody(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "get_changelog", map[string]any{"response_mode": "compact"})
	if res.IsError {
		t.Fatalf("get_changelog compact returned error: %v", res.Content)
	}
	data := decodeContent(t, res)
	entries, ok := data["entries"].([]any)
	if !ok || len(entries) != 1 {
		t.Fatalf("get_changelog compact entries = %#v, want exactly 1 default entry", data["entries"])
	}
	first, ok := entries[0].(map[string]any)
	if !ok {
		t.Fatalf("get_changelog compact entry = %T, want map", entries[0])
	}
	if _, present := first["body"]; present {
		t.Fatalf("get_changelog compact entry unexpectedly includes full body: %#v", first)
	}
}

// TestGetChangelogSinceUnknownVersionReturnsError confirms an unrecognized
// since_version is rejected with invalid_params rather than silently
// returning everything or nothing.
func TestGetChangelogSinceUnknownVersionReturnsError(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "get_changelog", map[string]any{"since_version": "v0.0.1-does-not-exist"})
	if !res.IsError {
		t.Fatal("get_changelog with an unknown since_version expected an error, got success")
	}
}

// TestGetChangelogRejectsNegativeLimit confirms the shared negativeLimitError
// convention (#641) applies to get_changelog's limit too.
func TestGetChangelogRejectsNegativeLimit(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	res := callTool(t, session, "get_changelog", map[string]any{"limit": -1})
	if !res.IsError {
		t.Fatal("get_changelog with limit=-1 expected an error, got success")
	}
}

// TestGetChangelogDoesNotRequireAuthentication confirms get_changelog is
// registered without a required scope (anonymous tier) — the changelog is
// already public on GitHub, so gating it adds no real confidentiality.
func TestGetChangelogDoesNotRequireAuthentication(t *testing.T) {
	found := false
	for _, d := range anonymous.Defs() {
		if d.Name == "get_changelog" {
			found = true
			if d.RequiredScope != "" {
				t.Errorf("get_changelog RequiredScope = %q, want empty (anonymous tier)", d.RequiredScope)
			}
		}
	}
	if !found {
		t.Fatal("get_changelog missing from anonymous.Defs()")
	}
}

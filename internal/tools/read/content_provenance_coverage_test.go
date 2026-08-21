package read_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools/read"
)

// contentProvenanceClassification records, for every tool package read
// registers (read.Defs()), whether it must carry
// meta.content_provenance="site_source_untrusted" (or the rendered-output
// variant) per docs/mcp-contract.md §6.27, and why. This is the
// completeness invariant: TestReadDefsHaveExplicitContentProvenanceClassification
// fails on any read.Defs() tool missing an entry — mirrors
// TestToolExposureTierCoversEveryRegisteredTool
// (internal/server/exposure_profile_internal_test.go), but unlike that
// table there is no silent "advanced" default: an unclassified tool must
// fail the build, not fall through untagged.
//
// `tag` is the literal expected meta.content_provenance value, or "" for a
// tool whose payload carries no site-authored text (server-computed
// metadata/statistics/diagnostics only).
type contentProvenanceClassification struct {
	tag    string
	reason string
}

var expectedContentProvenance = map[string]contentProvenanceClassification{
	"get_page_markdown":    {"site_source_untrusted", "returns the raw page body"},
	"get_page_frontmatter": {"site_source_untrusted", "returns raw frontmatter fields"},
	"get_related_content":  {"site_source_untrusted", "related-page titles/shared-tag terms are site content"},
	"build_agent_context":  {"site_source_untrusted", "aggregates page bodies for agent context"},
	"export_agent_context": {"site_source_untrusted", "aggregates page bodies for agent export"},
	"get_page_for_edit":    {"site_source_untrusted", "bundles markdown/frontmatter for editing"},
	"search_content":       {"site_source_untrusted", "returns page titles/snippets from a text search"},
	"get_broken_links":     {"site_source_untrusted", "reports link text/URLs found in page bodies"},
	"get_backlinks":        {"site_source_untrusted", "reports anchor text of pages linking to a slug"},
	"suggest_links":        {"site_source_untrusted", "suggests link anchor text drawn from page titles"},
	"plan_page":            {"site_source_untrusted", "suggested_links carries page-title/anchor text"},
	"diff_page":            {"", "tags conditionally on SourceContent — verified separately by diff_page's own tests, not this table (see diff.go's newDiffPageOutput)"},
	"inspect_rendered":     {"site_rendered_public_untrusted", "check details embed fragments of the rendered HTML"},

	// Deliberately untagged: server-computed statistics, fixed-vocabulary
	// diagnostics, or filenames — not raw page-body/title text. Tracked
	// here explicitly (not silently omitted) so a future change that adds
	// title/body echoing to one of these must update this table, and this
	// list stays the honest record of what's NOT covered rather than an
	// implicit claim of full coverage.
	"list_content_types":   {"", "content-type/special-file names are config-derived, not page content"},
	"list_page_assets":     {"", "asset filenames only — known residual gap, filenames are still editor-controlled text"},
	"check_ai_readiness":   {"", "checks/warnings/suggestions are fixed-vocabulary diagnostic strings"},
	"explain_structure":    {"", "recent_pages titles are site content — known residual gap, not yet tagged (contentEnvelope is shared with get_site_health)"},
	"get_site_health":      {"", "scores/counters computed from site state, no page text echoed"},
	"validate_frontmatter": {"", "issues[] are fixed-vocabulary diagnostic strings, not copied page text"},
	"validate_site":        {"", "issues[] are fixed-vocabulary diagnostic strings, not copied page text"},
	"list_page_revisions":  {"", "git revision metadata (hash/date/author) — known residual gap, commit messages are editor-controlled text"},
}

func TestReadDefsHaveExplicitContentProvenanceClassification(t *testing.T) {
	defs := read.Defs()
	var missing []string
	for _, d := range defs {
		if _, ok := expectedContentProvenance[d.Name]; !ok {
			missing = append(missing, d.Name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("read.Defs() tools with no content-provenance classification entry (add one to expectedContentProvenance): %v", missing)
	}

	// Every entry must carry a non-empty reason: an unexplained
	// classification (tagged or exempt) is exactly the kind of drift this
	// table exists to prevent from going silent.
	for name, c := range expectedContentProvenance {
		if strings.TrimSpace(c.reason) == "" {
			t.Fatalf("expectedContentProvenance[%q] has no reason — every classification must explain why", name)
		}
	}

	registered := make(map[string]bool, len(defs))
	for _, d := range defs {
		registered[d.Name] = true
	}
	var stale []string
	for name := range expectedContentProvenance {
		if !registered[name] {
			stale = append(stale, name)
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Fatalf("expectedContentProvenance has entries for tools no longer in read.Defs(): %v", stale)
	}
}

// TestSiteContentToolsTagContentProvenance is the behavioral spot-check:
// for every tool classified above as requiring a tag, actually call it
// through the real tools/call boundary and assert the tag lands in the
// envelope. diff_page is excluded (conditional tagging, covered by its own
// tests); build_agent_context, export_agent_context, get_page_for_edit, and
// inspect_rendered are excluded here as redundant — already spot-checked
// via assertEnvelopeContentProvenance in tools_test.go and
// inspect_rendered_page_test.go respectively (inspect_rendered needs a
// rendered-public-HTML fixture this file's plain mustTestIndex() doesn't
// provide).
func TestSiteContentToolsTagContentProvenance(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	alreadyCoveredElsewhere := map[string]bool{
		"diff_page":            true,
		"build_agent_context":  true,
		"export_agent_context": true,
		"get_page_for_edit":    true,
		"inspect_rendered":     true,
	}

	args := map[string]map[string]any{
		"get_page_markdown":    {"slug": "/posts/hello"},
		"get_page_frontmatter": {"slug": "/posts/hello"},
		"get_related_content":  {"slug": "/posts/hello", "limit": 5},
		"search_content":       {"query": "hello", "limit": 5},
		"get_broken_links":     {"limit": 5, "offset": 0},
		"get_backlinks":        {"slug": "/posts/hello"},
		"suggest_links":        {"slug": "/posts/hello"},
		"plan_page":            {"tags": []any{"hugo"}},
	}

	for name, want := range expectedContentProvenance {
		if want.tag == "" || alreadyCoveredElsewhere[name] {
			continue
		}
		tc, ok := args[name]
		if !ok {
			t.Fatalf("%s: requires a tag (%q) but has no test args wired up in this test's args map", name, want.tag)
		}
		t.Run(name, func(t *testing.T) {
			res := callTool(t, session, name, tc)
			if res.IsError {
				t.Fatalf("%s returned error: %v", name, res.Content)
			}
			envelope := decodeEnvelope(t, res)
			meta, ok := envelope["meta"].(map[string]any)
			if !ok {
				t.Fatalf("%s: envelope missing meta object, got %v", name, envelope["meta"])
			}
			if got := meta["content_provenance"]; got != want.tag {
				t.Fatalf("%s: meta.content_provenance = %v, want %q", name, got, want.tag)
			}
		})
	}
}

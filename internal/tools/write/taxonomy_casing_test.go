package write_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSeedPage writes a minimal, already-indexed Hugo page directly to disk
// (bypassing create_page) so tests can seed an "existing casing" for
// normalize_taxonomy_casing (#589) to resolve new writes against.
func writeSeedPage(t *testing.T, contentRoot, slug, lang string, tags, categories []string) {
	t.Helper()
	dir := filepath.Join(contentRoot, filepath.FromSlash(slug))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	filename := "index.md"
	if lang != "" {
		filename = "index." + lang + ".md"
	}
	var b strings.Builder
	b.WriteString("---\ntitle: \"Seed\"\n")
	if len(tags) > 0 {
		b.WriteString("tags:\n")
		for _, tag := range tags {
			b.WriteString("  - " + tag + "\n")
		}
	}
	if len(categories) > 0 {
		b.WriteString("categories:\n")
		for _, cat := range categories {
			b.WriteString("  - " + cat + "\n")
		}
	}
	b.WriteString("---\n\nSeed body.\n")
	if err := os.WriteFile(filepath.Join(dir, filename), []byte(b.String()), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

// TestCreatePageNormalizesTagCasingToExistingForm is a regression test for
// #589: with normalize_taxonomy_casing opted in, a newly submitted tag that
// only differs in casing from a single existing spelling elsewhere in the
// index is rewritten to that existing spelling before the page is written.
func TestCreatePageNormalizesTagCasingToExistingForm(t *testing.T) {
	contentRoot := t.TempDir()
	writeSeedPage(t, contentRoot, "posts/existing", "", []string{"JavaScript"}, nil)

	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug":                      "posts/new-post",
		"title":                     "New Post",
		"body":                      "Body",
		"tags":                      []any{"javascript"},
		"categories":                []any{},
		"normalize_taxonomy_casing": true,
	})
	if res.IsError {
		t.Fatalf("create_page failed: %s", marshalContent(t, res))
	}
	data := decodeWriteData(t, res)

	normalized, ok := data["taxonomy_casing_normalized"].([]any)
	if !ok || len(normalized) != 1 {
		t.Fatalf("data.taxonomy_casing_normalized = %#v, want one entry", data["taxonomy_casing_normalized"])
	}
	entry := normalized[0].(map[string]any)
	if entry["type"] != "tag" || entry["from"] != "javascript" || entry["to"] != "JavaScript" {
		t.Fatalf("taxonomy_casing_normalized[0] = %#v, want {type:tag from:javascript to:JavaScript}", entry)
	}

	raw, err := os.ReadFile(filepath.Join(contentRoot, "posts", "new-post", "index.md"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(raw), "JavaScript") || strings.Contains(string(raw), "- javascript") {
		t.Fatalf("written page did not adopt existing casing:\n%s", raw)
	}
}

// TestCreatePageDoesNotNormalizeCasingByDefault confirms
// normalize_taxonomy_casing is opt-in: omitting it (the default) writes the
// tag exactly as submitted even when a differently-cased existing form is
// present, so this is a pure additive feature with no behavior change for
// existing callers.
func TestCreatePageDoesNotNormalizeCasingByDefault(t *testing.T) {
	contentRoot := t.TempDir()
	writeSeedPage(t, contentRoot, "posts/existing", "", []string{"JavaScript"}, nil)

	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug":       "posts/new-post",
		"title":      "New Post",
		"body":       "Body",
		"tags":       []any{"javascript"},
		"categories": []any{},
	})
	if res.IsError {
		t.Fatalf("create_page failed: %s", marshalContent(t, res))
	}
	data := decodeWriteData(t, res)
	if _, present := data["taxonomy_casing_normalized"]; present {
		t.Fatalf("data.taxonomy_casing_normalized = %#v, want absent when not opted in", data["taxonomy_casing_normalized"])
	}

	raw, err := os.ReadFile(filepath.Join(contentRoot, "posts", "new-post", "index.md"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(raw), "- javascript") {
		t.Fatalf("written page should keep the submitted casing verbatim when not opted in:\n%s", raw)
	}
}

// TestCreatePageLeavesAmbiguousCasingUnchanged confirms that when the index
// already has two or more distinct casings for the same term (pre-existing
// drift — the #577 casing_variant scenario), normalize_taxonomy_casing
// leaves the new submission exactly as typed rather than guessing which
// existing spelling is "correct", and reports it as ambiguous.
func TestCreatePageLeavesAmbiguousCasingUnchanged(t *testing.T) {
	contentRoot := t.TempDir()
	writeSeedPage(t, contentRoot, "posts/existing-a", "", []string{"JavaScript"}, nil)
	writeSeedPage(t, contentRoot, "posts/existing-b", "", []string{"javascript"}, nil)

	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug":                      "posts/new-post",
		"title":                     "New Post",
		"body":                      "Body",
		"tags":                      []any{"JAVASCRIPT"},
		"categories":                []any{},
		"normalize_taxonomy_casing": true,
	})
	if res.IsError {
		t.Fatalf("create_page failed: %s", marshalContent(t, res))
	}
	data := decodeWriteData(t, res)
	if _, present := data["taxonomy_casing_normalized"]; present {
		t.Fatalf("data.taxonomy_casing_normalized = %#v, want absent for an ambiguous term", data["taxonomy_casing_normalized"])
	}
	ambiguous, ok := data["taxonomy_casing_ambiguous"].([]any)
	if !ok || len(ambiguous) != 1 {
		t.Fatalf("data.taxonomy_casing_ambiguous = %#v, want one entry", data["taxonomy_casing_ambiguous"])
	}
	entry := ambiguous[0].(map[string]any)
	if entry["type"] != "tag" || entry["term"] != "JAVASCRIPT" {
		t.Fatalf("taxonomy_casing_ambiguous[0] = %#v, want {type:tag term:JAVASCRIPT}", entry)
	}

	raw, err := os.ReadFile(filepath.Join(contentRoot, "posts", "new-post", "index.md"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(raw), "- JAVASCRIPT") {
		t.Fatalf("written page should keep the ambiguous term verbatim:\n%s", raw)
	}
}

// TestCreatePageNormalizationRespectsLanguageScope confirms
// normalize_taxonomy_casing never borrows an existing casing from a
// different language: an existing "fr" spelling must not influence a new
// "en" page's tag, since a casing difference that only ever appears across
// languages can be a deliberate per-language style choice.
func TestCreatePageNormalizationRespectsLanguageScope(t *testing.T) {
	contentRoot := t.TempDir()
	writeSeedPage(t, contentRoot, "posts/existing", "fr", []string{"Securite"}, nil)

	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug":                      "posts/new-post",
		"lang":                      "en",
		"title":                     "New Post",
		"body":                      "Body",
		"tags":                      []any{"securite"},
		"categories":                []any{},
		"normalize_taxonomy_casing": true,
	})
	if res.IsError {
		t.Fatalf("create_page failed: %s", marshalContent(t, res))
	}
	data := decodeWriteData(t, res)
	if _, present := data["taxonomy_casing_normalized"]; present {
		t.Fatalf("data.taxonomy_casing_normalized = %#v, want absent across languages", data["taxonomy_casing_normalized"])
	}
	if _, present := data["taxonomy_casing_ambiguous"]; present {
		t.Fatalf("data.taxonomy_casing_ambiguous = %#v, want absent (zero same-language forms is not ambiguous)", data["taxonomy_casing_ambiguous"])
	}

	raw, err := os.ReadFile(filepath.Join(contentRoot, "posts", "new-post", "index.en.md"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(raw), "- securite") {
		t.Fatalf("written page should keep its own casing across languages:\n%s", raw)
	}
}

// TestUpdatePageNormalizesCategoryCasing mirrors the create_page coverage
// above for update_page, confirming the same opt-in resolution applies to
// categories on an existing page.
func TestUpdatePageNormalizesCategoryCasing(t *testing.T) {
	contentRoot := t.TempDir()
	writeSeedPage(t, contentRoot, "posts/canonical", "", nil, []string{"Infrastructure"})
	// target starts with no categories: the only existing casing for this
	// slug across the site is "canonical"'s "Infrastructure", so this is a
	// clean single-match normalization. If target already carried
	// "infrastructure" itself and this call resubmitted that same spelling,
	// the verbatim short-circuit (forms[term] == true) would fire first and
	// silently no-op before the ambiguity check ever ran — see
	// TestUpdatePageNormalizesCategoryCasingIsNoOpForOwnVerbatimSpelling and
	// TestUpdatePageLeavesAmbiguousCasingUnchanged below for both of those
	// cases.
	writeSeedPage(t, contentRoot, "posts/target", "", nil, nil)

	session, _, done := newTestServer(t, contentRoot)
	defer done()

	expectedRevision := currentRevision(t, filepath.Join(contentRoot, "posts", "target", "index.md"))
	res := callTool(t, session, "update_page", map[string]any{
		"slug":                      "posts/target",
		"categories":                []any{"infrastructure"},
		"normalize_taxonomy_casing": true,
		"expected_revision":         expectedRevision,
	})
	if res.IsError {
		t.Fatalf("update_page failed: %s", marshalContent(t, res))
	}
	data := decodeWriteData(t, res)
	normalized, ok := data["taxonomy_casing_normalized"].([]any)
	if !ok || len(normalized) != 1 {
		t.Fatalf("data.taxonomy_casing_normalized = %#v, want one entry", data["taxonomy_casing_normalized"])
	}
	entry := normalized[0].(map[string]any)
	if entry["type"] != "category" || entry["from"] != "infrastructure" || entry["to"] != "Infrastructure" {
		t.Fatalf("taxonomy_casing_normalized[0] = %#v, want {type:category from:infrastructure to:Infrastructure}", entry)
	}

	raw, err := os.ReadFile(filepath.Join(contentRoot, "posts", "target", "index.md"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(raw), "- Infrastructure") || strings.Contains(string(raw), "- infrastructure") {
		t.Fatalf("updated page did not adopt existing casing:\n%s", raw)
	}
}

// TestUpdatePageDryRunPreviewsTaxonomyCasingNormalization confirms
// normalize_taxonomy_casing resolution is also visible on a dry_run call
// (the diff/preview path), not only on the real write.
func TestUpdatePageDryRunPreviewsTaxonomyCasingNormalization(t *testing.T) {
	contentRoot := t.TempDir()
	writeSeedPage(t, contentRoot, "posts/canonical", "", []string{"JavaScript"}, nil)
	// target starts with no tags — see the comment in
	// TestUpdatePageNormalizesCategoryCasing on why it must not already
	// carry a different casing of the same term.
	writeSeedPage(t, contentRoot, "posts/target", "", nil, nil)

	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "update_page", map[string]any{
		"slug":                      "posts/target",
		"tags":                      []any{"javascript"},
		"normalize_taxonomy_casing": true,
		"dry_run":                   true,
	})
	if res.IsError {
		t.Fatalf("update_page dry_run failed: %s", marshalContent(t, res))
	}
	data := decodeWriteData(t, res)
	normalized, ok := data["taxonomy_casing_normalized"].([]any)
	if !ok || len(normalized) != 1 {
		t.Fatalf("data.taxonomy_casing_normalized = %#v, want one entry on dry_run preview", data["taxonomy_casing_normalized"])
	}

	raw, err := os.ReadFile(filepath.Join(contentRoot, "posts", "target", "index.md"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.Contains(string(raw), "JavaScript") {
		t.Fatalf("dry_run must not write anything to disk:\n%s", raw)
	}
}

// TestUpdatePageNormalizesCategoryCasingIsNoOpForOwnVerbatimSpelling
// confirms that resubmitting a page's own existing spelling verbatim hits
// the "already matches an existing form" short-circuit, not the
// normalization path: nothing is rewritten and nothing is reported. This is
// deliberate, not a gap — normalize_taxonomy_casing prevents new drift, it
// doesn't remediate a page whose own casing is the one an operator actually
// wants to fix (that still requires an explicit new value, same as any
// other update_page field change).
func TestUpdatePageNormalizesCategoryCasingIsNoOpForOwnVerbatimSpelling(t *testing.T) {
	contentRoot := t.TempDir()
	writeSeedPage(t, contentRoot, "posts/target", "", nil, []string{"JavaScript"})

	session, _, done := newTestServer(t, contentRoot)
	defer done()

	expectedRevision := currentRevision(t, filepath.Join(contentRoot, "posts", "target", "index.md"))
	res := callTool(t, session, "update_page", map[string]any{
		"slug":                      "posts/target",
		"categories":                []any{"JavaScript"},
		"normalize_taxonomy_casing": true,
		"expected_revision":         expectedRevision,
	})
	if res.IsError {
		t.Fatalf("update_page failed: %s", marshalContent(t, res))
	}
	data := decodeWriteData(t, res)
	if _, present := data["taxonomy_casing_normalized"]; present {
		t.Fatalf("data.taxonomy_casing_normalized = %#v, want absent for a verbatim resubmission", data["taxonomy_casing_normalized"])
	}
	if _, present := data["taxonomy_casing_ambiguous"]; present {
		t.Fatalf("data.taxonomy_casing_ambiguous = %#v, want absent for a verbatim resubmission", data["taxonomy_casing_ambiguous"])
	}
}

// TestUpdatePageLeavesAmbiguousCasingUnchanged mirrors the create_page
// ambiguous-casing coverage for update_page: a target page submitting a
// third, brand-new casing of a term that already has two distinct existing
// spellings elsewhere in the index is left exactly as typed and reported as
// ambiguous, never guessed at.
func TestUpdatePageLeavesAmbiguousCasingUnchanged(t *testing.T) {
	contentRoot := t.TempDir()
	writeSeedPage(t, contentRoot, "posts/canonical", "", []string{"JavaScript"}, nil)
	writeSeedPage(t, contentRoot, "posts/other", "", []string{"javascript"}, nil)
	writeSeedPage(t, contentRoot, "posts/target", "", nil, nil)

	session, _, done := newTestServer(t, contentRoot)
	defer done()

	expectedRevision := currentRevision(t, filepath.Join(contentRoot, "posts", "target", "index.md"))
	res := callTool(t, session, "update_page", map[string]any{
		"slug":                      "posts/target",
		"tags":                      []any{"javaSCRIPT"},
		"normalize_taxonomy_casing": true,
		"expected_revision":         expectedRevision,
	})
	if res.IsError {
		t.Fatalf("update_page failed: %s", marshalContent(t, res))
	}
	data := decodeWriteData(t, res)
	if _, present := data["taxonomy_casing_normalized"]; present {
		t.Fatalf("data.taxonomy_casing_normalized = %#v, want absent for an ambiguous term", data["taxonomy_casing_normalized"])
	}
	ambiguous, ok := data["taxonomy_casing_ambiguous"].([]any)
	if !ok || len(ambiguous) != 1 {
		t.Fatalf("data.taxonomy_casing_ambiguous = %#v, want one entry", data["taxonomy_casing_ambiguous"])
	}
	entry := ambiguous[0].(map[string]any)
	if entry["type"] != "tag" || entry["term"] != "javaSCRIPT" {
		t.Fatalf("taxonomy_casing_ambiguous[0] = %#v, want {type:tag term:javaSCRIPT}", entry)
	}

	raw, err := os.ReadFile(filepath.Join(contentRoot, "posts", "target", "index.md"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(raw), "- javaSCRIPT") {
		t.Fatalf("updated page should keep the ambiguous term verbatim:\n%s", raw)
	}
}

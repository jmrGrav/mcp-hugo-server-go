package read

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/gitutil"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
)

func TestContentHelperFunctions(t *testing.T) {
	pages := []site.Page{
		{Slug: "/posts/a/", Title: "Alpha", Summary: "first", Tags: []string{"go"}, Categories: []string{"docs"}, Date: "2026-07-03", URL: "https://example.test/posts/a/", Lang: "en"},
		{Slug: "/posts/b/", Title: "Beta", Summary: "second", Tags: []string{"mcp"}, Categories: []string{"docs"}, Date: "2026-07-04", URL: "https://example.test/posts/b/", Lang: "fr"},
		{Slug: "/about/", Title: "About", Summary: "third", Tags: []string{"go"}, Categories: []string{"pages"}, Date: "2026-07-02", URL: "https://example.test/about/", Lang: "en"},
	}

	if got := canonicalSort(""); got != "date" {
		t.Fatalf("canonicalSort(\"\") = %q", got)
	}
	if got := canonicalSort("title"); got != "title" {
		t.Fatalf("canonicalSort(title) = %q", got)
	}
	if got := canonicalOrder("ASC"); got != "asc" {
		t.Fatalf("canonicalOrder(ASC) = %q", got)
	}
	if got := effectiveSort(searchContentInput{Query: "alpha"}); got != "relevance" {
		t.Fatalf("effectiveSort(query) = %q", got)
	}

	filtered := filterContentPages(pages, searchContentInput{Query: "go", Type: "post", Order: "desc"}, nil)
	if len(filtered) != 1 || filtered[0].Slug != "/posts/a/" {
		t.Fatalf("filterContentPages() = %#v", filtered)
	}
	classifier := site.NewClassifierFromPages(pages)
	if !matchContentFilters(pages[0], searchContentInput{Tag: "go", Category: "docs", Language: "en", Type: "posts"}, classifier, nil) {
		t.Fatal("matchContentFilters() should match expected page")
	}
	if matchContentFilters(pages[2], searchContentInput{Type: "posts"}, classifier, nil) {
		t.Fatal("matchContentFilters() should reject non-post for posts filter")
	}

	sorted := append([]site.Page(nil), pages...)
	sortContentPages(sorted, searchContentInput{Sort: "title", Order: "asc"})
	if sorted[0].Slug != "/about/" || sorted[2].Slug != "/posts/b/" {
		t.Fatalf("sortContentPages(title asc) = %#v", sorted)
	}
	sorted = append([]site.Page(nil), pages...)
	sortContentPages(sorted, searchContentInput{Query: "go", Order: "desc"})
	if sorted[0].Slug != "/posts/a/" {
		t.Fatalf("sortContentPages(relevance) = %#v", sorted)
	}

	if got := sliceContentPages(pages, 1, 1); len(got) != 1 || got[0].Slug != "/posts/b/" {
		t.Fatalf("sliceContentPages() = %#v", got)
	}
	if got := sliceContentPages(pages, 10, 1); len(got) != 0 {
		t.Fatalf("sliceContentPages(offset overflow) = %#v", got)
	}

	dto := toPageDTO(pages[0], nil, "")
	if dto.Slug != pages[0].Slug || dto.Title != "Alpha" {
		t.Fatalf("toPageDTO() = %#v", dto)
	}
	if got := toPageDTOs(pages, nil, nil, "", ""); len(got) != 3 || got[1].Slug != "/posts/b/" {
		t.Fatalf("toPageDTOs() = %#v", got)
	}
	if got := countSections(pages); len(got) == 0 || got[0].Name == "" {
		t.Fatalf("countSections() = %#v", got)
	}
	if got := topSection("/posts/hello/"); got != "posts" {
		t.Fatalf("topSection(posts) = %q", got)
	}
	if got := topSection("/about/"); got != "about" {
		t.Fatalf("topSection(about) = %q", got)
	}
	if got := uniqueLanguages(pages); len(got) != 2 {
		t.Fatalf("uniqueLanguages() = %#v", got)
	}
}

func TestResolveSourceForPagePrefersMatchingLanguage(t *testing.T) {
	if lookup := newSourceLookup(nil); lookup != nil {
		t.Fatal("newSourceLookup(nil) should return nil")
	}

	lookup := &sourceLookup{
		byLang: map[string]hugosite.SourcePage{
			sourceLookupKey("posts/hello", "fr"): {Slug: "posts/hello", Lang: "fr", FilePath: "/tmp/posts/hello/index.fr.md"},
			sourceLookupKey("posts/hello", "en"): {Slug: "posts/hello", Lang: "en", FilePath: "/tmp/posts/hello/index.en.md"},
		},
		byDefault: map[string]hugosite.SourcePage{
			"posts/default": {Slug: "posts/default", FilePath: "/tmp/posts/default/index.md"},
		},
		bySlug: map[string]hugosite.SourcePage{
			"posts/hello":    {Slug: "posts/hello", Lang: "fr", FilePath: "/tmp/posts/hello/index.fr.md"},
			"posts/default":  {Slug: "posts/default", FilePath: "/tmp/posts/default/index.md"},
			"posts/leaf.fr":  {Slug: "posts/leaf.fr", FilePath: "/tmp/posts/leaf.fr.md"},
			"posts/leaf":     {Slug: "posts/leaf", FilePath: "/tmp/posts/leaf.md"},
			"posts/bonjour":  {Slug: "posts/bonjour", Lang: "fr", FilePath: "/tmp/posts/bonjour/index.fr.md"},
			"posts/bonjour2": {Slug: "posts/bonjour2", Lang: "fr", FilePath: "/tmp/posts/bonjour2/index.fr.md"},
		},
	}

	got, ok := resolveSourceForPage(site.Page{Slug: "/fr/posts/hello/", Lang: "fr"}, lookup)
	if !ok || got.Page.FilePath != "/tmp/posts/hello/index.fr.md" || got.ResolvedLang != "fr" {
		t.Fatalf("resolveSourceForPage(fr) = %#v, %v", got, ok)
	}

	got, ok = resolveSourceForPage(site.Page{Slug: "/en/posts/hello/", Lang: "en"}, lookup)
	if !ok || got.Page.FilePath != "/tmp/posts/hello/index.en.md" || got.ResolvedLang != "en" {
		t.Fatalf("resolveSourceForPage(en) = %#v, %v", got, ok)
	}

	got, ok = resolveSourceForPage(site.Page{Slug: "/en/posts/default/", Lang: "en"}, lookup)
	if !ok || got.Page.FilePath != "/tmp/posts/default/index.md" {
		t.Fatalf("resolveSourceForPage(default fallback) = %#v, %v", got, ok)
	}

	match, ok := resolveSourceForPage(site.Page{Slug: "/fr/posts/leaf/", Lang: "fr"}, lookup)
	if !ok || match.Page.FilePath != "/tmp/posts/leaf.fr.md" || match.ResolvedLang != "fr" {
		t.Fatalf("resolveSourceForPage(leaf fallback) = %#v, %v", match, ok)
	}
}

func TestValidationHelpers(t *testing.T) {
	root := t.TempDir()
	write := func(rel, raw string) {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(raw), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("posts/a/index.md", "---\ntitle: Alpha\ndate: 2026-07-03\n---\nBody A\n")
	write("posts/b/index.md", "---\ndraft: true\n---\nBody B\n")
	src, err := hugosite.NewSourceIndex(root)
	if err != nil {
		t.Fatalf("NewSourceIndex() error = %v", err)
	}
	if got, err := sourcePagesForValidation(src, "posts/a"); err != nil || len(got) != 1 {
		t.Fatalf("sourcePagesForValidation(slug) = %#v err=%v", got, err)
	}
	if got, err := sourcePagesForValidation(src, ""); err != nil || len(got) != 2 {
		t.Fatalf("sourcePagesForValidation(all) = %#v err=%v", got, err)
	}
	if _, err := sourcePagesForValidation(src, "does-not-exist"); err == nil {
		t.Fatal("sourcePagesForValidation(missing): expected error, got nil")
	}
	issues := validateFrontMatterPage(hugosite.SourcePage{Slug: "/broken/", FrontmatterRaw: map[string]any{}}, nil)
	if len(issues) < 2 {
		t.Fatalf("validateFrontMatterPage() = %#v", issues)
	}
	out := validatePagesWithIssues(src.ListPages(0, 0), 0, 1, nil)
	if !out.Success || out.Data.PagesChecked != 2 || len(out.Data.Pages) != 1 {
		t.Fatalf("validatePagesWithIssues() = %#v", out)
	}
	health := buildSiteHealth(&site.Index{}, src, nil)
	if health.SourcePages != 2 || health.DraftPages != 1 {
		t.Fatalf("buildSiteHealth() = %#v", health)
	}
}

func TestReadHelperBranches(t *testing.T) {
	if got := clampLimit(0, 10, 50); got != 10 {
		t.Fatalf("clampLimit(0) = %d", got)
	}
	if got := clampLimit(100, 10, 50); got != 50 {
		t.Fatalf("clampLimit(100) = %d", got)
	}
	if got := clampLimit(25, 10, 50); got != 25 {
		t.Fatalf("clampLimit(25) = %d", got)
	}
	if got := nullsafeStrings(nil); len(got) != 0 {
		t.Fatalf("nullsafeStrings(nil) = %#v", got)
	}
	if got := readingTimeMinutes(""); got != 1 {
		t.Fatalf("readingTimeMinutes(empty) = %d", got)
	}
	if got := readingTimeMinutes(strings.Repeat("word ", 201)); got != 2 {
		t.Fatalf("readingTimeMinutes(201 words) = %d", got)
	}

	idx := &site.Index{}
	related := computeRelated(idx, site.Page{Slug: "/posts/a/", Tags: []string{"go"}, Categories: []string{"docs"}}, 5)
	if len(related) != 0 {
		t.Fatalf("computeRelated() = %#v", related)
	}
}

func TestDiffHelperBranches(t *testing.T) {
	if got := diffStatus(true, []byte("same"), []byte("same")); got != "unchanged" {
		t.Fatalf("diffStatus(unchanged) = %q", got)
	}
	if got := diffStatus(true, []byte("new"), []byte("old")); got != "modified" {
		t.Fatalf("diffStatus(modified) = %q", got)
	}
	if got := diffStatus(false, []byte{}, nil); got != "deleted" {
		t.Fatalf("diffStatus(deleted) = %q", got)
	}
	if got := diffStatus(false, []byte("new"), nil); got != "git_untracked" {
		t.Fatalf("diffStatus(git_untracked) = %q", got)
	}
	cmd128 := exec.Command("bash", "-c", "exit 128")
	err128 := cmd128.Run()
	if !isGitPathMissing(err128) {
		t.Fatal("isGitPathMissing() should detect exit code 128")
	}
	cmd0 := exec.Command("bash", "-c", "exit 0")
	if err0 := cmd0.Run(); isGitPathMissing(err0) {
		t.Fatal("isGitPathMissing() should not match exit code 0")
	}
	cmd1 := exec.Command("bash", "-c", "exit 1")
	err1 := cmd1.Run()
	if isGitPathMissing(err1) {
		t.Fatal("isGitPathMissing() should not match exit code 1")
	}

	root := t.TempDir()

	if diff, err := unifiedDiff("posts/hello/index.md", []byte("one\n"), []byte("two\n")); err != nil || !strings.Contains(diff, "two") {
		t.Fatalf("unifiedDiff() = %q, %v", diff, err)
	}

	if out, err := gitutil.Bytes(context.Background(), root, "--version"); err != nil || !strings.Contains(string(out), "git version") {
		t.Fatalf("gitutil.Bytes() = %q, %v", out, err)
	}
}

func TestScoreLinkSuggestions(t *testing.T) {
	cfg := config.Default()
	cfg.SiteRoot = t.TempDir()
	cfg.SiteURL = "https://example.test"

	emptyIdx, err := site.NewIndex(cfg)
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}
	// Empty index returns empty slice
	if got := scoreLinkSuggestions(emptyIdx, "", []string{"go"}, nil, "", 5); len(got) != 0 {
		t.Fatalf("scoreLinkSuggestions(empty index) = %v", got)
	}

	realIdx, err := site.NewIndex(cfg)
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}
	pages := []site.Page{
		{Slug: "/posts/a/", Title: "Alpha", Tags: []string{"go", "hugo"}, Categories: []string{"docs"}, URL: "https://example.test/posts/a/"},
		{Slug: "/posts/b/", Title: "Beta", Tags: []string{"go"}, Categories: []string{"ops"}, URL: "https://example.test/posts/b/"},
		{Slug: "/posts/c/", Title: "Gamma", Tags: []string{"rust"}, Categories: []string{"ops"}, URL: "https://example.test/posts/c/"},
	}
	for _, pg := range pages {
		realIdx.UpsertPage(pg)
	}

	// refTags=["go"] matches A (score 2) and B (score 2); C has no overlap
	got := scoreLinkSuggestions(realIdx, "", []string{"go"}, nil, "", 10)
	if len(got) != 2 {
		t.Fatalf("want 2 suggestions, got %d: %v", len(got), got)
	}

	// excluding /posts/a/ should return only B
	got = scoreLinkSuggestions(realIdx, "/posts/a/", []string{"go"}, nil, "", 10)
	if len(got) != 1 || got[0].Slug != "/posts/b/" {
		t.Fatalf("exclude slug: want [/posts/b/], got %v", got)
	}

	// body mention bumps to top (W2: phrase-boundary, not substring)
	got = scoreLinkSuggestions(realIdx, "", []string{"go"}, nil, "check out Alpha for more", 10)
	if len(got) == 0 || !got[0].BodyMention || got[0].Slug != "/posts/a/" {
		t.Fatalf("body_mention: want /posts/a/ first, got %v", got)
	}

	// W2: "Beta" must NOT match "Alphabeta" (substring but not word-boundary)
	got = scoreLinkSuggestions(realIdx, "", []string{"go"}, nil, "Alphabeta context", 10)
	for _, s := range got {
		if s.Slug == "/posts/b/" && s.BodyMention {
			t.Fatal("body_mention false positive: 'Beta' should not match inside 'Alphabeta'")
		}
	}

	// E1: empty-title page must not produce false body_mention
	emptyTitleIdx, _ := site.NewIndex(cfg)
	emptyTitleIdx.UpsertPage(site.Page{Slug: "/posts/notitle/", Title: "", Tags: []string{"go"}, URL: "https://example.test/posts/notitle/"})
	got = scoreLinkSuggestions(emptyTitleIdx, "", []string{"go"}, nil, "anything goes here", 10)
	for _, s := range got {
		if s.BodyMention {
			t.Fatalf("E1: empty-title page must not have body_mention=true, got %#v", s)
		}
	}

	// limit respected
	got = scoreLinkSuggestions(realIdx, "", []string{"go"}, nil, "", 1)
	if len(got) != 1 {
		t.Fatalf("limit=1: want 1, got %d", len(got))
	}

	// anchor_text is the page title
	if got[0].AnchorText == "" {
		t.Fatal("anchor_text should not be empty")
	}
}

func TestDetectTaxonomyInconsistencies(t *testing.T) {
	root := t.TempDir()
	write := func(rel, raw string) {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(raw), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("posts/a/index.md", "---\ntitle: A\ntags: [golang]\ncategories: [docs]\n---\n")
	write("posts/b/index.md", "---\ntitle: B\ntags: [go]\ncategories: [docs]\n---\n")
	src, err := hugosite.NewSourceIndex(root)
	if err != nil {
		t.Fatalf("NewSourceIndex: %v", err)
	}
	// nil index returns nil
	if got := detectTaxonomyInconsistencies(nil, nil); got != nil {
		t.Fatalf("detectTaxonomyInconsistencies(nil) = %v", got)
	}
	// alias map: "golang" is an alias for "go"
	aliases := map[string]string{"golang": "go"}
	issues := detectTaxonomyInconsistencies(src, aliases)
	var match *taxonomyInconsistencyDTO
	for i := range issues {
		if strings.Contains(issues[i].Message, "golang") {
			match = &issues[i]
			break
		}
	}
	if match == nil {
		t.Fatalf("detectTaxonomyInconsistencies() did not flag alias 'golang': %v", issues)
	}
	if match.TermA != "golang" {
		t.Fatalf("detectTaxonomyInconsistencies() term_a = %q, want %q", match.TermA, "golang")
	}
	if len(match.PagesWithTermA) != 1 || match.PagesWithTermA[0] != "posts/a" {
		t.Fatalf("detectTaxonomyInconsistencies() pages_with_term_a = %v, want [posts/a] (#324)", match.PagesWithTermA)
	}
	if match.Severity != "warning" {
		t.Fatalf("detectTaxonomyInconsistencies() alias_mismatch severity = %q, want %q (#419)", match.Severity, "warning")
	}
}

// TestTaxonomyFindingSeverity covers #419: every Kind this server ever
// assigns must map to an explicit Severity, since score_breakdown's
// taxonomy penalty is computed purely from that mapping.
func TestTaxonomyFindingSeverity(t *testing.T) {
	cases := map[string]string{
		"alias_mismatch":     "warning",
		"possible_duplicate": "warning",
		"translation_pair":   "info",
	}
	for kind, want := range cases {
		if got := taxonomyFindingSeverity(kind); got != want {
			t.Errorf("taxonomyFindingSeverity(%q) = %q, want %q", kind, got, want)
		}
	}
}

// TestDetectTaxonomyInconsistenciesTranslationPairNotFlaggedAsDuplicate
// covers #183: security (EN) and sécurité (FR) tagged on the same Hugo page
// bundle (index.en.md/index.fr.md sharing one Slug per hugosite.SlugFromRel)
// must be classified as a translation_pair, not a possible_duplicate,
// reproducing the exact pair ChatGPT's live audit (2026-07-17) flagged.
func TestDetectTaxonomyInconsistenciesTranslationPairNotFlaggedAsDuplicate(t *testing.T) {
	root := t.TempDir()
	write := func(rel, raw string) {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(raw), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Same bundle (posts/csp), two languages, same concept tagged in each language.
	write("posts/csp/index.en.md", "---\ntitle: CSP\ntags: [security]\n---\n")
	write("posts/csp/index.fr.md", "---\ntitle: CSP\ntags: [sécurité]\n---\n")
	// A genuinely different page pair with a similar-looking spelling typo,
	// unrelated to any bundle/translation relationship.
	write("posts/one/index.md", "---\ntitle: One\ntags: [postmortem]\n---\n")
	write("posts/two/index.md", "---\ntitle: Two\ntags: [post-mortems]\n---\n")

	src, err := hugosite.NewSourceIndex(root)
	if err != nil {
		t.Fatalf("NewSourceIndex: %v", err)
	}
	issues := detectTaxonomyInconsistencies(src, nil)

	var translationPair, possibleDup *taxonomyInconsistencyDTO
	for i := range issues {
		switch {
		case issues[i].TermA == "security" || issues[i].TermB == "security":
			translationPair = &issues[i]
		case issues[i].TermA == "postmortem" || issues[i].TermB == "postmortem":
			possibleDup = &issues[i]
		}
	}
	if translationPair == nil {
		t.Fatalf("expected a security/sécurité finding, got %#v", issues)
	}
	if translationPair.Kind != "translation_pair" {
		t.Fatalf("security/sécurité Kind = %q, want translation_pair", translationPair.Kind)
	}
	if strings.Contains(translationPair.Message, "may be duplicates") {
		t.Fatalf("security/sécurité message should not read as a possible-duplicate finding: %q", translationPair.Message)
	}

	if possibleDup == nil {
		t.Fatalf("expected a postmortem/post-mortems finding, got %#v", issues)
	}
	if possibleDup.Kind != "possible_duplicate" {
		t.Fatalf("postmortem/post-mortems Kind = %q, want possible_duplicate (different pages, not a translation pair)", possibleDup.Kind)
	}
}

// TestDetectTaxonomyInconsistenciesSamePageBothSpellingsIsNotATranslation
// covers the failure mode a same-slug-set-only check misses: a single
// monolingual page tagged with BOTH spelling variants directly (e.g. a
// copy-paste typo) hits the same set of page slugs on both sides — the
// naive proxy would wrongly call this a translation_pair. It must still be
// possible_duplicate, since it's the exact case this detector exists for.
func TestDetectTaxonomyInconsistenciesSamePageBothSpellingsIsNotATranslation(t *testing.T) {
	root := t.TempDir()
	write := func(rel, raw string) {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(raw), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("posts/x/index.md", "---\ntitle: X\ntags: [postmortem, post-mortems]\n---\n")

	src, err := hugosite.NewSourceIndex(root)
	if err != nil {
		t.Fatalf("NewSourceIndex: %v", err)
	}
	issues := detectTaxonomyInconsistencies(src, nil)

	var match *taxonomyInconsistencyDTO
	for i := range issues {
		if issues[i].TermA == "postmortem" || issues[i].TermB == "postmortem" {
			match = &issues[i]
			break
		}
	}
	if match == nil {
		t.Fatalf("expected a postmortem/post-mortems finding, got %#v", issues)
	}
	if match.Kind != "possible_duplicate" {
		t.Fatalf("Kind = %q, want possible_duplicate (same page, same language, both spelling variants — a real typo, not a translation)", match.Kind)
	}
}

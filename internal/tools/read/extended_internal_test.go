package read

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/buildstatus"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/db"
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

	dto := toPageDTO(pages[0], nil, "", true)
	if dto.Slug != pages[0].Slug || dto.Title != "Alpha" {
		t.Fatalf("toPageDTO() = %#v", dto)
	}
	if got := toPageDTOs(pages, nil, nil, "", "", true); len(got) != 3 || got[1].Slug != "/posts/b/" {
		t.Fatalf("toPageDTOs() = %#v", got)
	}
	snippets := map[string]string{"/posts/a/": "alpha snippet"}
	if got := toPageDTOsWithSnippets(pages[:1], nil, snippets, nil, "", "", true); len(got) != 1 || got[0].Snippet != "alpha snippet" {
		t.Fatalf("toPageDTOsWithSnippets() = %#v", got)
	}
	if got := countSections(pages); len(got) == 0 || got[0].Name == "" {
		t.Fatalf("countSections() = %#v", got)
	}
	if got := topSection("/posts/hello/", ""); got != "posts" {
		t.Fatalf("topSection(posts) = %q", got)
	}
	if got := topSection("/about/", ""); got != "about" {
		t.Fatalf("topSection(about) = %q", got)
	}
	if got := topSection("/en/posts/hello/", "en"); got != "posts" {
		t.Fatalf("topSection(lang-prefixed) = %q, want language prefix stripped", got)
	}
	if got := topSection("/en/", "en"); got != "root" {
		t.Fatalf("topSection(bare lang root) = %q, want root", got)
	}
	if got := uniqueLanguages(pages); len(got) != 2 {
		t.Fatalf("uniqueLanguages() = %#v", got)
	}
}

// TestBuildSiteHealthReportsBrokenLinksOnlyWhenDBPathConfigured is #1105's
// resolution of its own design question: broken-link volume feeds
// get_site_health, but only as a status override sourced from
// get_broken_links's own O(1) db_path link graph — never a full-HTML-rescan
// paid on every get_site_health call, and never folded into the weighted
// score's arithmetic.
func TestBuildSiteHealthReportsBrokenLinksOnlyWhenDBPathConfigured(t *testing.T) {
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
	write("posts/hello/index.html", `<html><head><title>Hello</title></head><body><a href="/missing/">bad</a></body></html>`)

	siteIdx, err := site.NewIndex(config.Config{
		SiteRoot:         root,
		SiteURL:          "https://example.test",
		SiteName:         "example",
		DefaultLanguage:  "en",
		RejectSymlinks:   true,
		RejectHiddenPath: true,
	})
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}

	dbPath := filepath.Join(t.TempDir(), "test.db")
	siteDB, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer siteDB.Close()

	hello, found := siteIdx.GetBySlug("/posts/hello/")
	if !found {
		t.Fatal("GetBySlug(/posts/hello/) not found")
	}
	if err := siteDB.SyncPublicPage(*hello, siteIdx); err != nil {
		t.Fatalf("SyncPublicPage: %v", err)
	}

	t.Run("without db_path: signal entirely omitted, no full-rescan fallback", func(t *testing.T) {
		health := buildSiteHealth(context.Background(), siteIdx, nil, nil, config.Config{}, nil)
		if health.BrokenLinksCount != nil {
			t.Fatalf("BrokenLinksCount = %v, want nil (not computed without db_path)", *health.BrokenLinksCount)
		}
		if health.ScoreBreakdown == nil || health.ScoreBreakdown.BrokenLinks != nil {
			t.Fatalf("score_breakdown.broken_links = %#v, want nil (omitted, not computed)", health.ScoreBreakdown)
		}
		if health.Status != "healthy" || health.ContentStatus != "healthy" {
			t.Fatalf("status/content_status = %q/%q, want healthy/healthy — an uncomputed signal must never degrade status", health.Status, health.ContentStatus)
		}
		if health.Score != 100 {
			t.Fatalf("score = %d, want 100", health.Score)
		}
	})

	t.Run("with db_path: nonzero broken links degrades status and caps score", func(t *testing.T) {
		health := buildSiteHealth(context.Background(), siteIdx, nil, nil, config.Config{}, siteDB)
		if health.BrokenLinksCount == nil || *health.BrokenLinksCount != 1 {
			t.Fatalf("BrokenLinksCount = %v, want *1", health.BrokenLinksCount)
		}
		if health.ScoreBreakdown == nil || health.ScoreBreakdown.BrokenLinks == nil {
			t.Fatal("score_breakdown.broken_links = nil, want populated when db_path is configured")
		}
		bl := health.ScoreBreakdown.BrokenLinks
		if bl.Score != 0 || bl.Weight != 0 || bl.Issues != 1 {
			t.Fatalf("score_breakdown.broken_links = %#v, want {Score:0 Weight:0 Issues:1}", bl)
		}
		if health.Status != "degraded" || health.ContentStatus != "degraded" {
			t.Fatalf("status/content_status = %q/%q, want degraded/degraded", health.Status, health.ContentStatus)
		}
		if health.Score != 99 {
			t.Fatalf("score = %d, want 99 (capped, weight 0 never moves the weighted score itself)", health.Score)
		}
	})
}

func TestBuildSiteHealthSurfacesRuntimeDegraded(t *testing.T) {
	buildstatus.ResetForTest()
	defer buildstatus.ResetForTest()
	buildstatus.RecordFailure("permission_denied", time.Now())
	health := buildSiteHealth(context.Background(), &site.Index{}, nil, nil, config.Config{}, nil)
	if health.RuntimeDegraded == nil || !*health.RuntimeDegraded {
		t.Fatalf("runtime_degraded after failed build = %#v, want true", health.RuntimeDegraded)
	}
	if health.Status != "degraded" || health.ContentStatus != "healthy" {
		t.Fatalf("status/content_status after failed build = %q/%q, want degraded/healthy", health.Status, health.ContentStatus)
	}
	if health.Score != 99 {
		t.Fatalf("score after failed build = %d, want 99 so a degraded runtime never advertises perfection", health.Score)
	}
	buildstatus.RecordSuccess(time.Now())
	health = buildSiteHealth(context.Background(), &site.Index{}, nil, nil, config.Config{}, nil)
	if health.RuntimeDegraded == nil || *health.RuntimeDegraded {
		t.Fatalf("runtime_degraded after successful build = %#v, want false", health.RuntimeDegraded)
	}
	if health.Score != 100 {
		t.Fatalf("score after successful build = %d, want 100", health.Score)
	}
}

func TestBuildSiteHealthDetectsIncompleteMultilingualPublicOutput(t *testing.T) {
	buildstatus.ResetForTest()
	defer buildstatus.ResetForTest()

	siteRoot := t.TempDir()
	publicPath := filepath.Join(siteRoot, "posts", "hello", "index.html")
	if err := os.MkdirAll(filepath.Dir(publicPath), 0o755); err != nil {
		t.Fatalf("MkdirAll public: %v", err)
	}
	if err := os.WriteFile(publicPath, []byte(`<!DOCTYPE html><html lang="fr"><head><title>Bonjour</title>
<link rel="canonical" href="https://example.test/posts/hello/"></head><body>Bonjour</body></html>`), 0o644); err != nil {
		t.Fatalf("WriteFile public: %v", err)
	}

	contentRoot := t.TempDir()
	for rel, raw := range map[string]string{
		"posts/hello/index.fr.md":       "---\ntitle: Bonjour\ndate: 2026-08-10\n---\nBonjour\n",
		"posts/hello/index.en.md":       "---\ntitle: Hello\ndate: 2026-08-10\n---\nHello\n",
		"posts/headless/index.en.md":    "---\ntitle: Data only\ndate: 2026-08-10\nheadless: true\n---\nData\n",
		"posts/no-render/index.en.md":   "---\ntitle: No render\ndate: 2026-08-10\n_build:\n  render: never\n---\nData\n",
		"posts/link-render/index.en.md": "---\ntitle: Link render\ndate: 2026-08-10\n_build:\n  render: link\n---\nData\n",
	} {
		full := filepath.Join(contentRoot, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("MkdirAll source: %v", err)
		}
		if err := os.WriteFile(full, []byte(raw), 0o644); err != nil {
			t.Fatalf("WriteFile source: %v", err)
		}
	}

	cfg := config.Default()
	cfg.SiteRoot = siteRoot
	cfg.ContentRoot = contentRoot
	cfg.SiteURL = "https://example.test"
	cfg.DefaultLanguage = "fr"
	cfg.MaxIndexEntries = 1000
	idx, err := site.NewIndex(cfg)
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}
	srcIdx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatalf("NewSourceIndex: %v", err)
	}

	health := buildSiteHealth(context.Background(), idx, srcIdx, nil, cfg, nil)
	if health.PublishableSourcePages != 2 || health.MissingPublicPages != 1 {
		t.Fatalf("publishable/missing = %d/%d, want 2/1", health.PublishableSourcePages, health.MissingPublicPages)
	}
	if health.PublicOutputComplete == nil || *health.PublicOutputComplete {
		t.Fatalf("public_output_complete = %#v, want false", health.PublicOutputComplete)
	}
	if health.RuntimeDegraded == nil || !*health.RuntimeDegraded || health.Status != "degraded" {
		t.Fatalf("runtime/status = %#v/%q, want true/degraded", health.RuntimeDegraded, health.Status)
	}
	if health.ContentStatus != "healthy" || health.Score != 100 {
		t.Fatalf("content status/score = %q/%d, want healthy/100 (public_output_incomplete affects status, not score)", health.ContentStatus, health.Score)
	}
	if !slicesContain(health.RuntimeDegradedReasons, "public_output_incomplete") {
		t.Fatalf("runtime_degraded_reasons = %#v, want public_output_incomplete", health.RuntimeDegradedReasons)
	}
	coverage := health.PublicationCoverage
	if coverage == nil || coverage.PublishableContentSources != 2 || coverage.OtherExcludedSources != 3 ||
		coverage.MissingPublishableContentPages != 1 || coverage.Complete {
		t.Fatalf("publication_coverage = %#v, want 2 publishable, 3 excluded, 1 missing, incomplete", coverage)
	}
}

func TestBuildSiteHealthRecognizesCustomPublicURL(t *testing.T) {
	buildstatus.ResetForTest()
	defer buildstatus.ResetForTest()

	siteRoot := t.TempDir()
	publicPath := filepath.Join(siteRoot, "guides", "custom", "index.html")
	if err := os.MkdirAll(filepath.Dir(publicPath), 0o755); err != nil {
		t.Fatalf("MkdirAll public: %v", err)
	}
	if err := os.WriteFile(publicPath, []byte(`<!DOCTYPE html><html lang="en"><head><title>Custom</title>
<link rel="canonical" href="https://example.test/guides/custom/"></head><body>Custom</body></html>`), 0o644); err != nil {
		t.Fatalf("WriteFile public: %v", err)
	}

	contentRoot := t.TempDir()
	sourcePath := filepath.Join(contentRoot, "posts", "custom", "index.en.md")
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0o755); err != nil {
		t.Fatalf("MkdirAll source: %v", err)
	}
	if err := os.WriteFile(sourcePath, []byte("---\ntitle: Custom\ndate: 2026-08-10\nurl: /guides/custom/\n---\nCustom\n"), 0o644); err != nil {
		t.Fatalf("WriteFile source: %v", err)
	}

	cfg := config.Default()
	cfg.SiteRoot = siteRoot
	cfg.ContentRoot = contentRoot
	cfg.SiteURL = "https://example.test"
	cfg.DefaultLanguage = "en"
	idx, err := site.NewIndex(cfg)
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}
	srcIdx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatalf("NewSourceIndex: %v", err)
	}

	health := buildSiteHealth(context.Background(), idx, srcIdx, nil, cfg, nil)
	if health.PublishableSourcePages != 1 || health.MissingPublicPages != 0 {
		t.Fatalf("publishable/missing = %d/%d, want 1/0", health.PublishableSourcePages, health.MissingPublicPages)
	}
	if health.PublicOutputComplete == nil || !*health.PublicOutputComplete {
		t.Fatalf("public_output_complete = %#v, want true", health.PublicOutputComplete)
	}
}

// TestBuildSiteHealthIgnoresSectionIndexBundles guards a false-positive found
// by running this check against arleo.eu's real production content: Hugo
// homepage/section bundles (_index.md, _index.<lang>.md) route to their
// section's own URL ("/", "/posts/"), not to a slug derived from their own
// filename. SlugFromRel gives "_index.en.md" the literal slug "_index.en",
// which the public index never contains under that name — so without this
// exclusion, every real site using Hugo section indexes at all would have
// get_site_health flip to "degraded" immediately, even with 100% of content
// actually published.
func TestBuildSiteHealthIgnoresSectionIndexBundles(t *testing.T) {
	buildstatus.ResetForTest()
	defer buildstatus.ResetForTest()

	siteRoot := t.TempDir()
	for rel, html := range map[string]string{
		"index.html":       `<!DOCTYPE html><html lang="en"><head><title>Home</title></head><body>Home</body></html>`,
		"posts/index.html": `<!DOCTYPE html><html lang="en"><head><title>Posts</title></head><body>Posts</body></html>`,
	} {
		full := filepath.Join(siteRoot, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("MkdirAll public: %v", err)
		}
		if err := os.WriteFile(full, []byte(html), 0o644); err != nil {
			t.Fatalf("WriteFile public: %v", err)
		}
	}

	contentRoot := t.TempDir()
	for rel, raw := range map[string]string{
		"_index.en.md":       "---\ntitle: Home\ndate: 2026-08-10\n---\nHome\n",
		"posts/_index.en.md": "---\ntitle: Posts\ndate: 2026-08-10\n---\nPosts\n",
	} {
		full := filepath.Join(contentRoot, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("MkdirAll source: %v", err)
		}
		if err := os.WriteFile(full, []byte(raw), 0o644); err != nil {
			t.Fatalf("WriteFile source: %v", err)
		}
	}

	cfg := config.Default()
	cfg.SiteRoot = siteRoot
	cfg.ContentRoot = contentRoot
	cfg.SiteURL = "https://example.test"
	cfg.DefaultLanguage = "en"
	cfg.MaxIndexEntries = 1000
	idx, err := site.NewIndex(cfg)
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}
	srcIdx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatalf("NewSourceIndex: %v", err)
	}

	health := buildSiteHealth(context.Background(), idx, srcIdx, nil, cfg, nil)
	if health.PublishableSourcePages != 0 || health.MissingPublicPages != 0 {
		t.Fatalf("publishable/missing = %d/%d, want 0/0 (section indexes excluded)", health.PublishableSourcePages, health.MissingPublicPages)
	}
	if health.PublicOutputComplete == nil || !*health.PublicOutputComplete {
		t.Fatalf("public_output_complete = %#v, want true", health.PublicOutputComplete)
	}
	if health.Status != "healthy" {
		t.Fatalf("status = %q, want healthy", health.Status)
	}
	coverage := health.PublicationCoverage
	if health.SourcePages != 2 || health.SectionIndexPages != 2 || health.PublishableContentPages != 0 ||
		coverage == nil || coverage.SourceDocuments != 2 || coverage.SectionIndexSources != 2 || coverage.OtherExcludedSources != 0 || !coverage.Complete {
		t.Fatalf("section-index publication coverage = %#v; health=%#v", coverage, health)
	}
}

// TestBuildSiteHealthExplainsEightyContentPlusTwoLanguageIndexes reproduces
// the v1.8.2 numeric shape from #992. It intentionally proves that equal
// residual counts do not establish identity: two source indexes and two
// additional public content routes are separate populations.
func TestBuildSiteHealthExplainsEightyContentPlusTwoLanguageIndexes(t *testing.T) {
	buildstatus.ResetForTest()
	defer buildstatus.ResetForTest()

	siteRoot := t.TempDir()
	contentRoot := t.TempDir()
	writePublic := func(rel, lang, canonical, title string) {
		t.Helper()
		full := filepath.Join(siteRoot, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		html := fmt.Sprintf(`<!DOCTYPE html><html lang="%s"><head><title>%s</title><link rel="canonical" href="https://example.test%s"></head><body>%s</body></html>`, lang, title, canonical, title)
		if err := os.WriteFile(full, []byte(html), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeSource := func(rel, title string) {
		t.Helper()
		full := filepath.Join(contentRoot, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		raw := fmt.Sprintf("---\ntitle: %s\ndate: 2026-01-01\n---\n%s\n", title, title)
		if err := os.WriteFile(full, []byte(raw), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	writePublic("posts/index.html", "en", "/posts/", "Posts")
	writePublic("fr/posts/index.html", "fr", "/fr/posts/", "Articles")
	// Two public-only regular pages make published_pages 82. They deliberately
	// do not masquerade as the two source _index documents: the breakdown must
	// expose both residual populations independently rather than infer a match.
	writePublic("about/index.html", "en", "/about/", "About")
	writePublic("contact/index.html", "en", "/contact/", "Contact")
	writeSource("posts/_index.en.md", "Posts")
	writeSource("posts/_index.fr.md", "Articles")
	for i := 0; i < 80; i++ {
		slug := fmt.Sprintf("page-%02d", i)
		title := fmt.Sprintf("Page %02d", i)
		writePublic(filepath.ToSlash(filepath.Join("posts", slug, "index.html")), "en", "/posts/"+slug+"/", title)
		writeSource(filepath.ToSlash(filepath.Join("posts", slug, "index.en.md")), title)
	}

	cfg := config.Default()
	cfg.SiteRoot = siteRoot
	cfg.ContentRoot = contentRoot
	cfg.SiteURL = "https://example.test"
	cfg.DefaultLanguage = "en"
	cfg.MaxIndexEntries = 1000
	idx, err := site.NewIndex(cfg)
	if err != nil {
		t.Fatal(err)
	}
	srcIdx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatal(err)
	}

	health := buildSiteHealth(context.Background(), idx, srcIdx, nil, cfg, nil)
	if health.SourcePages != 82 || health.PublishableSourcePages != 80 || health.PublishableContentPages != 80 ||
		health.SectionIndexPages != 2 || health.PublishedPages != 82 || health.MissingPublicPages != 0 ||
		health.PublicOutputComplete == nil || !*health.PublicOutputComplete {
		t.Fatalf("health counters = source:%d legacy_publishable:%d content:%d indexes:%d published:%d missing:%d complete:%v",
			health.SourcePages, health.PublishableSourcePages, health.PublishableContentPages, health.SectionIndexPages,
			health.PublishedPages, health.MissingPublicPages, health.PublicOutputComplete)
	}
	want := publicationCoverageDTO{
		SourceDocuments: 82, PublishableContentSources: 80, SectionIndexSources: 2,
		OtherExcludedSources: 0, PublishedContentPages: 82, MissingPublishableContentPages: 0,
		CompletenessBasis: "publishable_content_sources", CountersDirectlyComparable: false, Complete: true,
	}
	if health.PublicationCoverage == nil || *health.PublicationCoverage != want {
		t.Fatalf("publication_coverage = %#v, want %#v", health.PublicationCoverage, want)
	}
}

func slicesContain(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
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
	write("posts/multi/index.fr.md", "---\ntitle: Bonjour\ndate: 2026-07-05\n---\nBody FR\n")
	write("posts/multi/index.en.md", "---\ntitle: Hello\ndate: 2026-07-05\n---\nBody EN\n")
	src, err := hugosite.NewSourceIndex(root)
	if err != nil {
		t.Fatalf("NewSourceIndex() error = %v", err)
	}
	if got, err := sourcePagesForValidation(src, "posts/a", ""); err != nil || len(got) != 1 {
		t.Fatalf("sourcePagesForValidation(slug) = %#v err=%v", got, err)
	}
	if got, err := sourcePagesForValidation(src, "posts/multi", ""); err != nil || len(got) != 2 {
		t.Fatalf("sourcePagesForValidation(multilingual slug) = %#v err=%v", got, err)
	} else if got[0].Lang != "en" || got[1].Lang != "fr" {
		t.Fatalf("sourcePagesForValidation(multilingual slug) langs = %q, %q want en, fr", got[0].Lang, got[1].Lang)
	}
	if got, err := sourcePagesForValidation(src, "", ""); err != nil || len(got) != 4 {
		t.Fatalf("sourcePagesForValidation(all) = %#v err=%v", got, err)
	}
	if _, err := sourcePagesForValidation(src, "does-not-exist", ""); err == nil {
		t.Fatal("sourcePagesForValidation(missing): expected error, got nil")
	}
	if got, err := sourcePagesForValidation(src, "posts/multi", "fr"); err != nil || len(got) != 1 || got[0].Lang != "fr" {
		t.Fatalf("sourcePagesForValidation(multilingual slug, fr) = %#v err=%v", got, err)
	}
	if got, err := sourcePagesForValidation(src, "/en/posts/multi/", ""); err != nil || len(got) != 2 {
		t.Fatalf("sourcePagesForValidation(public multilingual slug) = %#v err=%v", got, err)
	}
	if got, err := sourcePagesForValidation(src, "/en/posts/multi/", "en"); err != nil || len(got) != 1 || got[0].Lang != "en" {
		t.Fatalf("sourcePagesForValidation(public multilingual slug, en) = %#v err=%v", got, err)
	}
	issues := validateFrontMatterPage(hugosite.SourcePage{Slug: "/broken/", FrontmatterRaw: map[string]any{}}, nil)
	if len(issues) < 2 {
		t.Fatalf("validateFrontMatterPage() = %#v", issues)
	}
	out := validatePagesWithIssues(src.ListPages(0, 0), 0, 1, "", nil, site.NewPageResolver(&site.Index{}, src, config.Config{}))
	if !out.Success || out.Data.PagesChecked != 4 || len(out.Data.Pages) != 1 {
		t.Fatalf("validatePagesWithIssues() = %#v", out)
	}
	health := buildSiteHealth(context.Background(), &site.Index{}, src, nil, config.Config{}, nil)
	if health.SourcePages != 4 || health.DraftPages != 1 {
		t.Fatalf("buildSiteHealth() = %#v", health)
	}
}

func TestReaderSafeResolvedPage(t *testing.T) {
	public := site.Page{Slug: "/posts/demo/", Title: "Demo", URL: "https://example.test/posts/demo/", Lang: "fr"}
	source := &hugosite.SourcePage{Slug: "posts/demo", Lang: "fr", Body: "draft body"}
	resolved := site.ResolvedPage{Public: &public, Source: source}

	got, err := readerSafeResolvedPage(context.Background(), resolved, "posts/demo")
	if err != nil {
		t.Fatalf("readerSafeResolvedPage(non-reader) error = %v", err)
	}
	if got.Public == nil || got.Public.Slug != "/posts/demo/" {
		t.Fatalf("readerSafeResolvedPage(non-reader) = %#v, want public page preserved", got)
	}

	readerCtx := site.WithAccessProfile(context.Background(), site.AccessProfileReader)
	got, err = readerSafeResolvedPage(readerCtx, resolved, "posts/demo")
	if err != nil {
		t.Fatalf("readerSafeResolvedPage(reader public) error = %v", err)
	}
	if got.Public == nil || got.Source != nil {
		t.Fatalf("readerSafeResolvedPage(reader public) = %#v, want public-only resolved page", got)
	}

	_, err = readerSafeResolvedPage(readerCtx, site.ResolvedPage{Source: source}, "posts/demo")
	if err == nil || !strings.Contains(err.Error(), "content_not_public") {
		t.Fatalf("readerSafeResolvedPage(reader source-only) error = %v, want content_not_public", err)
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
	related, _ := computeRelated(idx, site.Page{Slug: "/posts/a/", Tags: []string{"go"}, Categories: []string{"docs"}}, 5)
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
	if got, evaluated := scoreLinkSuggestions(emptyIdx, "", []string{"go"}, nil, "", 5); len(got) != 0 || evaluated != 0 {
		t.Fatalf("scoreLinkSuggestions(empty index) = %v, evaluated=%d", got, evaluated)
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
	got, _ := scoreLinkSuggestions(realIdx, "", []string{"go"}, nil, "", 10)
	if len(got) != 2 {
		t.Fatalf("want 2 suggestions, got %d: %v", len(got), got)
	}

	// excluding /posts/a/ should return only B
	got, _ = scoreLinkSuggestions(realIdx, "/posts/a/", []string{"go"}, nil, "", 10)
	if len(got) != 1 || got[0].Slug != "/posts/b/" {
		t.Fatalf("exclude slug: want [/posts/b/], got %v", got)
	}

	// body mention bumps to top (W2: phrase-boundary, not substring)
	got, _ = scoreLinkSuggestions(realIdx, "", []string{"go"}, nil, "check out Alpha for more", 10)
	if len(got) == 0 || !got[0].BodyMention || got[0].Slug != "/posts/a/" {
		t.Fatalf("body_mention: want /posts/a/ first, got %v", got)
	}

	// W2: "Beta" must NOT match "Alphabeta" (substring but not word-boundary)
	got, _ = scoreLinkSuggestions(realIdx, "", []string{"go"}, nil, "Alphabeta context", 10)
	for _, s := range got {
		if s.Slug == "/posts/b/" && s.BodyMention {
			t.Fatal("body_mention false positive: 'Beta' should not match inside 'Alphabeta'")
		}
	}

	// E1: empty-title page must not produce false body_mention
	emptyTitleIdx, _ := site.NewIndex(cfg)
	emptyTitleIdx.UpsertPage(site.Page{Slug: "/posts/notitle/", Title: "", Tags: []string{"go"}, URL: "https://example.test/posts/notitle/"})
	got, _ = scoreLinkSuggestions(emptyTitleIdx, "", []string{"go"}, nil, "anything goes here", 10)
	for _, s := range got {
		if s.BodyMention {
			t.Fatalf("E1: empty-title page must not have body_mention=true, got %#v", s)
		}
	}

	// limit respected
	got, _ = scoreLinkSuggestions(realIdx, "", []string{"go"}, nil, "", 1)
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
		"casing_variant":     "warning",
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

// TestDetectTaxonomyInconsistenciesFlagsSameLanguageCasingVariant is a
// regression test for #577: a live Claude.ai audit (2026-07-20) found
// "Infrastructure"/"infrastructure" and similar casing-only variants mixed
// across English-language pages, which get_site_health never flagged —
// possible_duplicate/translation_pair never see this case because
// taxonomy.Slug() already lowercases before either check ever runs, so both
// spellings collapse to one slug and never even reach the edit-distance
// pairing pass.
func TestDetectTaxonomyInconsistenciesFlagsSameLanguageCasingVariant(t *testing.T) {
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
	write("posts/one/index.md", "---\ntitle: One\ncategories: [Infrastructure]\n---\n")
	write("posts/two/index.md", "---\ntitle: Two\ncategories: [infrastructure]\n---\n")

	src, err := hugosite.NewSourceIndex(root)
	if err != nil {
		t.Fatalf("NewSourceIndex: %v", err)
	}
	issues := detectTaxonomyInconsistencies(src, nil)

	var match *taxonomyInconsistencyDTO
	for i := range issues {
		if issues[i].Kind == "casing_variant" {
			match = &issues[i]
			break
		}
	}
	if match == nil {
		t.Fatalf("expected a casing_variant finding for Infrastructure/infrastructure, got %#v", issues)
	}
	if match.Severity != "warning" {
		t.Fatalf("casing_variant severity = %q, want warning (#419)", match.Severity)
	}
	gotPages := append(append([]string{}, match.PagesWithTermA...), match.PagesWithTermB...)
	sort.Strings(gotPages)
	wantPages := []string{"posts/one", "posts/two"}
	if len(gotPages) != len(wantPages) || gotPages[0] != wantPages[0] || gotPages[1] != wantPages[1] {
		t.Fatalf("casing_variant affected pages = %v, want %v", gotPages, wantPages)
	}
}

// TestDetectTaxonomyInconsistenciesDoesNotFlagDisjointLanguageCasing
// confirms the casing_variant detector only fires when both spellings
// share at least one language: two forms confined to entirely different
// languages could be a deliberate per-language capitalization convention,
// not necessarily a bug, so they're left alone rather than guessed at.
func TestDetectTaxonomyInconsistenciesDoesNotFlagDisjointLanguageCasing(t *testing.T) {
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
	write("posts/en-only/index.en.md", "---\ntitle: EN\ncategories: [Homelab]\n---\n")
	write("posts/fr-only/index.fr.md", "---\ntitle: FR\ncategories: [homelab]\n---\n")

	src, err := hugosite.NewSourceIndex(root)
	if err != nil {
		t.Fatalf("NewSourceIndex: %v", err)
	}
	issues := detectTaxonomyInconsistencies(src, nil)

	for _, issue := range issues {
		if issue.Kind == "casing_variant" {
			t.Fatalf("unexpected casing_variant for disjoint-language spellings: %#v", issue)
		}
	}
}

func TestValidateFrontMatterPageDetectsOnlyFrontmatterLikeBodyPrefix(t *testing.T) {
	page := hugosite.SourcePage{Title: "Valid", Date: "2026-08-10", FrontmatterRaw: map[string]any{"title": "Valid", "date": "2026-08-10"}, Body: "aliases:\n- /old/\n\nBody."}
	issues := validateFrontMatterPage(page, nil)
	if !containsString(issues, "possible misplaced front matter at start of markdown body") {
		t.Fatalf("issues = %#v, want misplaced front matter finding", issues)
	}
	page.Body = "The aliases: field is explained in this paragraph."
	issues = validateFrontMatterPage(page, nil)
	if containsString(issues, "possible misplaced front matter at start of markdown body") {
		t.Fatalf("ordinary markdown must not be flagged: %#v", issues)
	}
}

// TestValidateFrontMatterPageDoesNotFlagFrontmatterLikeLinesMidBody is a
// regression test for false-positives away from the true start of the
// body: a legitimate mid-article line that happens to start with a known
// key (e.g. explaining frontmatter syntax) must not be flagged — #1004
// asks for detection "at the beginning of Markdown bodies" specifically.
func TestValidateFrontMatterPageDoesNotFlagFrontmatterLikeLinesMidBody(t *testing.T) {
	page := hugosite.SourcePage{
		Title: "Valid", Date: "2026-08-10",
		FrontmatterRaw: map[string]any{"title": "Valid", "date": "2026-08-10"},
		Body:           "This article explains Hugo front matter.\n\nFor example:\n\ntags: this is just prose demonstrating the syntax\n\nMore prose after.",
	}
	issues := validateFrontMatterPage(page, nil)
	if containsString(issues, "possible misplaced front matter at start of markdown body") {
		t.Fatalf("a frontmatter-like line mid-body must not be flagged: %#v", issues)
	}
}

// TestOverfetchLimitPreventsUnderDeliveryWhenFilteringAfterTruncation is a
// regression test for #1041: computeRelated/scoreLinkSuggestions each
// truncate to whatever limit they're given internally, *before* the
// language/one_per_source_key filter runs at the call site. If the call
// site asked for only `limit` raw candidates, a same-language-and-topic
// sibling that happens to sort ahead of the caller's actually-wanted
// candidate (here, by the tie-break on Date) can fill the only slot,
// leaving nothing left for the requested language after filtering — not
// just "fewer than limit", but potentially zero. The fix is to request
// overfetchLimit(limit) raw candidates whenever a language/one_per_source_key
// filter will run, then truncate to the real limit afterward.
func TestOverfetchLimitPreventsUnderDeliveryWhenFilteringAfterTruncation(t *testing.T) {
	cfg := config.Default()
	cfg.SiteRoot = t.TempDir()
	cfg.SiteURL = "https://example.test"
	idx, err := site.NewIndex(cfg)
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}
	ref := site.Page{Slug: "/posts/ref/", Title: "Ref", Tags: []string{"go"}, URL: "https://example.test/posts/ref/"}
	// b-fr: no English sibling, but sorts first (most recent) among equally
	// scored candidates. a-fr/a-en: an actual translation pair; a-en is the
	// candidate a language:"en" filter should surface, but it sorts last.
	pages := []site.Page{
		{Slug: "/posts/b/", Title: "B", Tags: []string{"go"}, Lang: "fr", Date: "2026-01-03", URL: "https://example.test/posts/b/"},
		{Slug: "/posts/a/", Title: "A FR", Tags: []string{"go"}, Lang: "fr", Date: "2026-01-02", URL: "https://example.test/posts/a/"},
		{Slug: "/en/posts/a/", Title: "A EN", Tags: []string{"go"}, Lang: "en", Date: "2026-01-01", URL: "https://example.test/en/posts/a/"},
	}
	for _, pg := range pages {
		idx.UpsertPage(pg)
	}

	const limit = 1

	// Requesting only `limit` raw candidates (the pre-#1041 call pattern)
	// reproduces the bug: the single highest-ranked candidate (b-fr, most
	// recent) doesn't match language:"en", so filtering leaves nothing —
	// even though a genuinely matching candidate (a-en) exists.
	buggy, _ := computeRelated(idx, ref, limit)
	buggyFiltered := filterRelatedPages(buggy, "en", true)
	if len(buggyFiltered) != 0 {
		t.Fatalf("precondition not met: requesting only limit=%d raw candidates should reproduce the under-delivery bug (0 results), got %d", limit, len(buggyFiltered))
	}

	// The actual call-site behavior: over-fetch before filtering.
	fetched, _ := computeRelated(idx, ref, overfetchLimit(limit))
	filtered := filterRelatedPages(fetched, "en", true)
	if len(filtered) > limit {
		filtered = filtered[:limit]
	}
	if len(filtered) != 1 {
		t.Fatalf("overfetch+filter = %d results, want 1 (a-en)", len(filtered))
	}
	if filtered[0].Slug != "/en/posts/a/" {
		t.Fatalf("overfetch+filter result = %q, want /en/posts/a/", filtered[0].Slug)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

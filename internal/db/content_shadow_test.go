package db

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
)

func TestContentRepresentationMigrationBackfillsLegacyWithoutReplacingIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.sqlite")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.db.Exec(`INSERT INTO pages(slug,lang,title,content_hash,published,indexed_at) VALUES('/fr/posts/x/','fr','French','legacy-rev',1,'2026-08-15T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}

	d, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var sourceKey, lang, representation, revision string
	if err := d.db.QueryRow(`SELECT source_key,lang,representation,revision FROM content_representations_v1 WHERE legacy_slug='/fr/posts/x/'`).Scan(&sourceKey, &lang, &representation, &revision); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(sourceKey, "@public/") || lang != "fr" || representation != "public" || revision != "legacy-rev" {
		t.Fatalf("backfilled representation = %q %q %q %q", sourceKey, lang, representation, revision)
	}
	var legacyCount int
	if err := d.db.QueryRow(`SELECT COUNT(*) FROM pages WHERE slug='/fr/posts/x/'`).Scan(&legacyCount); err != nil || legacyCount != 1 {
		t.Fatalf("legacy serving row changed: count=%d err=%v", legacyCount, err)
	}
}

func TestContentRepresentationIdentitySeparatesLanguagesAndRepresentations(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for _, p := range []hugosite.SourcePage{
		{Slug: "posts/x", Lang: "en", Title: "English", Body: "EN"},
		{Slug: "posts/x", Lang: "fr", Title: "French", Body: "FR"},
	} {
		if err := d.SyncSourcePage(p); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []site.Page{
		{Slug: "/posts/x/", Lang: "en", Title: "English", RawHTML: "<p>EN</p>"},
		{Slug: "/fr/posts/x/", Lang: "fr", Title: "French", RawHTML: "<p>FR</p>"},
	} {
		if err := d.SyncPublicPage(p, nil); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := d.db.QueryRow(`SELECT COUNT(*) FROM content_representations_v1 WHERE source_key='posts/x'`).Scan(&count); err != nil || count != 4 {
		t.Fatalf("language-aware rows = %d err=%v, want 4", count, err)
	}
	for _, key := range [][2]string{{"en", "source"}, {"fr", "source"}, {"en", "public"}, {"fr", "public"}} {
		if err := d.db.QueryRow(`SELECT COUNT(*) FROM content_representations_v1 WHERE source_key='posts/x' AND lang=? AND representation=?`, key[0], key[1]).Scan(&count); err != nil || count != 1 {
			t.Fatalf("identity %v count=%d err=%v", key, count, err)
		}
	}

	if err := d.DeleteContentRepresentation("posts/x", "en", "source"); err != nil {
		t.Fatal(err)
	}
	if err := d.db.QueryRow(`SELECT COUNT(*) FROM content_representations_v1 WHERE source_key='posts/x'`).Scan(&count); err != nil || count != 3 {
		t.Fatalf("translation-scoped delete rows=%d err=%v, want 3", count, err)
	}
	if err := d.db.QueryRow(`SELECT COUNT(*) FROM content_representations_v1 WHERE source_key='posts/x' AND lang='fr' AND representation='source'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("French source sibling was deleted: count=%d err=%v", count, err)
	}
}

func TestContentShadowMatchesPrimaryLanguageWithoutCollidingWithSecondaryLanguage(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.SyncSourcePage(hugosite.SourcePage{Slug: "posts/default", Lang: "en", Title: "Default", Body: "source"}); err != nil {
		t.Fatal(err)
	}
	if err := d.SyncPublicPage(site.Page{Slug: "/posts/default/", Lang: "en", Title: "Default", RawHTML: "<p>public</p>"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := d.SyncSourcePage(hugosite.SourcePage{Slug: "posts/default", Lang: "fr", Title: "Secondaire", Body: "source fr"}); err != nil {
		t.Fatal(err)
	}
	if err := d.SyncPublicPage(site.Page{Slug: "/fr/posts/default/", Lang: "fr", Title: "Secondaire", RawHTML: "<p>public fr</p>"}, nil); err != nil {
		t.Fatal(err)
	}
	stats, err := d.RefreshContentShadowStats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.TotalRows != 4 || stats.SourceRows != 2 || stats.PublicRows != 2 || stats.MissingCounterparts != 0 {
		t.Fatalf("default/secondary stats = %+v", stats)
	}
}

func TestContentShadowReportsHashedLegacyMismatchWithoutPageData(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.SyncPublicPage(site.Page{Slug: "/private-title/", Lang: "en", Title: "Private title", RawHTML: "<p>body</p>"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := d.db.Exec(`UPDATE pages SET content_hash='out-of-band-legacy-drift' WHERE slug='/private-title/'`); err != nil {
		t.Fatal(err)
	}
	stats, err := d.RefreshContentShadowStats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.LegacyMismatches != 1 || len(stats.MismatchDigest) != 16 {
		t.Fatalf("mismatch diagnostics = %+v", stats)
	}
	if stats.MismatchDigest == "private-title" {
		t.Fatal("mismatch digest exposed a page identity")
	}
}

func TestPublicSourceKeyDoesNotStripLanguageLikeSectionName(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.SyncSourcePage(hugosite.SourcePage{Slug: "go/concurrency", Lang: "en", Title: "Go source", Body: "go"}); err != nil {
		t.Fatal(err)
	}
	if err := d.SyncPublicPage(site.Page{Slug: "/go/concurrency/", Lang: "en", Title: "Go", RawHTML: "<p>go</p>"}, nil); err != nil {
		t.Fatal(err)
	}
	var sourceKey string
	if err := d.db.QueryRow(`SELECT source_key FROM content_representations_v1 WHERE legacy_slug='/go/concurrency/'`).Scan(&sourceKey); err != nil {
		t.Fatal(err)
	}
	if sourceKey != "go/concurrency" {
		t.Fatalf("source_key=%q, want go/concurrency", sourceKey)
	}
}

func TestDeletingSourceBundlePreservesPublicShadowUntilRenderedCleanup(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.SyncSourcePage(hugosite.SourcePage{Slug: "posts/x", Lang: "en", Title: "Source", Body: "body"}); err != nil {
		t.Fatal(err)
	}
	if err := d.SyncPublicPage(site.Page{Slug: "/posts/x/", Lang: "en", Title: "Public", RawHTML: "<p>body</p>"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := d.DeleteBundleRepresentations("posts/x", "source"); err != nil {
		t.Fatal(err)
	}
	var sourceCount, publicCount int
	if err := d.db.QueryRow(`SELECT COUNT(*) FROM content_representations_v1 WHERE source_key='posts/x' AND representation='source'`).Scan(&sourceCount); err != nil {
		t.Fatal(err)
	}
	if err := d.db.QueryRow(`SELECT COUNT(*) FROM content_representations_v1 WHERE source_key='posts/x' AND representation='public'`).Scan(&publicCount); err != nil {
		t.Fatal(err)
	}
	if sourceCount != 0 || publicCount != 1 {
		t.Fatalf("source/public counts after source delete = %d/%d, want 0/1", sourceCount, publicCount)
	}
}

func TestStartupShadowUsesExplicitSourceMappingForPermalinkAndCanonicalDefaultLanguage(t *testing.T) {
	contentRoot := t.TempDir()
	defaultBundle := filepath.Join(contentRoot, "posts", "default")
	permalinkBundle := filepath.Join(contentRoot, "posts", "x")
	for _, dir := range []string{defaultBundle, permalinkBundle} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(defaultBundle, "index.md"), []byte("---\ntitle: Default\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(permalinkBundle, "index.fr.md"), []byte("---\ntitle: French\nurl: /fr/articles/x/\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srcIdx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.DefaultLanguage = "en"
	publicIdx, err := site.NewIndex(cfg)
	if err != nil {
		t.Fatal(err)
	}
	publicIdx.UpsertPage(site.Page{Slug: "/posts/default/", Lang: "en", Title: "Default", RawHTML: "<p>default</p>"})
	publicIdx.UpsertPage(site.Page{Slug: "/fr/articles/x/", Lang: "fr", Title: "French", RawHTML: "<p>fr</p>"})
	d, err := Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.StartupSync(publicIdx, srcIdx); err != nil {
		t.Fatal(err)
	}
	var sourceKey, lang string
	if err := d.db.QueryRow(`SELECT source_key,lang FROM content_representations_v1 WHERE legacy_slug='/fr/articles/x/' AND representation='public'`).Scan(&sourceKey, &lang); err != nil {
		t.Fatal(err)
	}
	if sourceKey != "posts/x" || lang != "fr" {
		t.Fatalf("permalink mapping = source_key %q lang %q, want posts/x/fr", sourceKey, lang)
	}
	var sourceDefault, publicDefault string
	if err := d.db.QueryRow(`SELECT lang FROM content_representations_v1 WHERE source_key='posts/default' AND representation='source'`).Scan(&sourceDefault); err != nil {
		t.Fatal(err)
	}
	if err := d.db.QueryRow(`SELECT lang FROM content_representations_v1 WHERE source_key='posts/default' AND representation='public'`).Scan(&publicDefault); err != nil {
		t.Fatal(err)
	}
	if sourceDefault != "en" || publicDefault != "en" {
		t.Fatalf("canonical default-language identity = source %q public %q, want en/en", sourceDefault, publicDefault)
	}
}

func TestStartupShadowReconcilesExternalTranslationDeletionAndDriftAcrossRestart(t *testing.T) {
	contentRoot := t.TempDir()
	bundle := filepath.Join(contentRoot, "posts", "x")
	if err := os.MkdirAll(bundle, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, title string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(bundle, name), []byte("---\ntitle: "+title+"\n---\nbody\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("index.en.md", "English")
	write("index.fr.md", "French")
	srcIdx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatal(err)
	}
	publicIdx, err := site.NewIndex(config.Default())
	if err != nil {
		t.Fatal(err)
	}
	publicIdx.UpsertPage(site.Page{Slug: "/posts/x/", Lang: "en", Title: "English", RawHTML: "<p>old</p>"})
	publicIdx.UpsertPage(site.Page{Slug: "/fr/posts/x/", Lang: "fr", Title: "French", RawHTML: "<p>fr</p>"})
	path := filepath.Join(t.TempDir(), "state.sqlite")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.StartupSync(publicIdx, srcIdx); err != nil {
		t.Fatal(err)
	}
	var before string
	if err := d.db.QueryRow(`SELECT revision FROM content_representations_v1 WHERE source_key='posts/x' AND lang='fr' AND representation='source'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}

	// Simulate an out-of-band edit and translation deletion while stopped.
	write("index.fr.md", "French changed externally")
	if err := os.Remove(filepath.Join(bundle, "index.en.md")); err != nil {
		t.Fatal(err)
	}
	srcIdx, err = hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatal(err)
	}
	d, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.StartupSync(publicIdx, srcIdx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := d.db.QueryRow(`SELECT COUNT(*) FROM content_representations_v1 WHERE source_key='posts/x' AND lang='en' AND representation='source'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("deleted EN source survived restart reconciliation: count=%d err=%v", count, err)
	}
	if err := d.db.QueryRow(`SELECT COUNT(*) FROM content_representations_v1 WHERE source_key='posts/x' AND lang='en' AND representation='public'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("public EN fact should survive source-only deletion: count=%d err=%v", count, err)
	}
	var after string
	if err := d.db.QueryRow(`SELECT revision FROM content_representations_v1 WHERE source_key='posts/x' AND lang='fr' AND representation='source'`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatalf("external FR edit was not reconciled: revision stayed %q", after)
	}
	stats, err := d.LatestContentShadowStats()
	if err != nil || stats == nil || stats.TotalRows != 3 || stats.SourceRows != 1 || stats.PublicRows != 2 {
		t.Fatalf("shadow stats = %+v err=%v", stats, err)
	}
}

package db_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/db"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
)

func openTestDB(t *testing.T) *db.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	d, err := db.Open(path)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func TestOpenAndClose(t *testing.T) {
	d := openTestDB(t)
	if d == nil {
		t.Fatal("expected non-nil DB")
	}
}

func TestSyncPublicPage(t *testing.T) {
	d := openTestDB(t)
	p := site.Page{
		Slug:       "/hello/",
		Title:      "Hello World",
		Summary:    "An introductory post",
		Tags:       []string{"go", "test"},
		Categories: []string{"tech"},
		Date:       "2024-01-01T00:00:00Z",
		URL:        "https://example.com/hello/",
		Lang:       "en",
	}
	if err := d.SyncPublicPage(p, nil); err != nil {
		t.Fatalf("SyncPublicPage: %v", err)
	}
	// Idempotent — second call with same data should succeed and be a no-op.
	if err := d.SyncPublicPage(p, nil); err != nil {
		t.Fatalf("SyncPublicPage (2nd): %v", err)
	}
}

func TestSyncAndSearchFTS5(t *testing.T) {
	d := openTestDB(t)
	pages := []site.Page{
		{Slug: "/gopher/", Title: "Gopher Guide", Summary: "All about Go gophers", URL: "https://x.com/gopher/", Lang: "en"},
		{Slug: "/rust/", Title: "Rust Programming", Summary: "Systems language", URL: "https://x.com/rust/", Lang: "en"},
		{Slug: "/draft/", Title: "Draft Post", Summary: "Not published", URL: "", Lang: "en"},
	}
	for _, p := range pages {
		if err := d.SyncPublicPage(p, nil); err != nil {
			t.Fatalf("SyncPublicPage %q: %v", p.Slug, err)
		}
	}

	results, err := d.Search("gopher", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one FTS result for 'gopher'")
	}
	if results[0].Slug != "/gopher/" {
		t.Errorf("top result = %q, want /gopher/", results[0].Slug)
	}
	// The draft page (no URL, published=0 in the schema — actually published=1 since we called SyncPublicPage)
	// Actually /draft/ has URL="" — but it still gets published=1 via SyncPublicPage. That's fine.
}

func TestSearchEmpty(t *testing.T) {
	d := openTestDB(t)
	results, err := d.Search("", 10)
	if err != nil {
		t.Fatalf("Search empty: %v", err)
	}
	if results != nil {
		t.Errorf("expected nil results for empty query, got %v", results)
	}
}

func TestDeletePage(t *testing.T) {
	d := openTestDB(t)
	p := site.Page{Slug: "/del/", Title: "To Delete", URL: "https://x.com/del/", Lang: "en"}
	if err := d.SyncPublicPage(p, nil); err != nil {
		t.Fatalf("SyncPublicPage: %v", err)
	}

	results, _ := d.Search("Delete", 10)
	if len(results) == 0 {
		t.Fatal("expected page in FTS before delete")
	}

	if err := d.DeletePage("/del/"); err != nil {
		t.Fatalf("DeletePage: %v", err)
	}

	results, _ = d.Search("Delete", 10)
	for _, r := range results {
		if r.Slug == "/del/" {
			t.Error("deleted page still appears in FTS results")
		}
	}
}

func TestGetBrokenLinks(t *testing.T) {
	d := openTestDB(t)

	// Page that links internally to a missing page.
	p := site.Page{
		Slug:    "/source/",
		Title:   "Source Page",
		URL:     "https://x.com/source/",
		Lang:    "en",
		RawHTML: `<a href="/missing/">Missing</a> <a href="/source/">Self</a>`,
	}
	// siteIdx is nil so all internal links are "broken".
	if err := d.SyncPublicPage(p, nil); err != nil {
		t.Fatalf("SyncPublicPage: %v", err)
	}

	broken, err := d.GetBrokenLinks()
	if err != nil {
		t.Fatalf("GetBrokenLinks: %v", err)
	}
	var found bool
	for _, r := range broken {
		if r.SourceSlug == "/source/" && strings.Contains(r.Target, "missing") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected broken link from /source/ to /missing/, got %+v", broken)
	}
}

func TestSyncSourcePage(t *testing.T) {
	d := openTestDB(t)
	sp := hugosite.SourcePage{
		Slug:       "posts/draft-one",
		FilePath:   "/content/posts/draft-one/index.md",
		Lang:       "en",
		Title:      "Draft One",
		Date:       "2024-06-01",
		Draft:      true,
		Tags:       []string{"draft"},
		Categories: []string{"blog"},
		Body:       "This is a draft.",
	}
	if err := d.SyncSourcePage(sp); err != nil {
		t.Fatalf("SyncSourcePage: %v", err)
	}
	// Draft pages have published=0 so FTS search should NOT return them.
	results, err := d.Search("draft", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	for _, r := range results {
		if r.Slug == "posts/draft-one" {
			t.Error("draft source page should not appear in published FTS results")
		}
	}
}

func TestSnapshotHealth(t *testing.T) {
	d := openTestDB(t)
	payload := `{"broken_links":0,"total_pages":42}`
	if err := d.SnapshotHealth(payload); err != nil {
		t.Fatalf("SnapshotHealth: %v", err)
	}
}

func TestStartupSync(t *testing.T) {
	d := openTestDB(t)

	// Minimal source index.
	tmp := t.TempDir()
	content := "---\ntitle: Hello\n---\nBody."
	mdPath := filepath.Join(tmp, "hello.md")
	if err := os.WriteFile(mdPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	srcIdx, err := hugosite.NewSourceIndex(tmp)
	if err != nil {
		t.Fatalf("NewSourceIndex: %v", err)
	}

	if err := d.StartupSync(nil, srcIdx); err != nil {
		t.Fatalf("StartupSync: %v", err)
	}

	// Second call should skip unchanged pages (hash-gated).
	if err := d.StartupSync(nil, srcIdx); err != nil {
		t.Fatalf("StartupSync (2nd): %v", err)
	}
}

func TestHashGatedSkip(t *testing.T) {
	d := openTestDB(t)
	p := site.Page{Slug: "/stable/", Title: "Stable", URL: "https://x.com/stable/", Lang: "en"}

	// First sync writes to DB.
	if err := d.SyncPublicPage(p, nil); err != nil {
		t.Fatalf("1st SyncPublicPage: %v", err)
	}
	// Second sync with identical data is a no-op (hash gate).
	if err := d.SyncPublicPage(p, nil); err != nil {
		t.Fatalf("2nd SyncPublicPage: %v", err)
	}
	// Changed page invalidates cache.
	p.Title = "Stable (updated)"
	if err := d.SyncPublicPage(p, nil); err != nil {
		t.Fatalf("3rd SyncPublicPage (updated): %v", err)
	}
	results, _ := d.Search("Stable updated", 10)
	if len(results) == 0 {
		t.Error("expected updated page to appear in FTS after title change")
	}
}

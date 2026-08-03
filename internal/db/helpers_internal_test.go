package db

import (
	"path/filepath"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
)

func TestExtractAttr(t *testing.T) {
	tests := []struct {
		name  string
		attrs string
		attr  string
		want  string
	}{
		{name: "double quoted", attrs: `href="/posts/demo/" class="x"`, attr: "href", want: "/posts/demo/"},
		{name: "single quoted", attrs: `href='/posts/demo/' class='x'`, attr: "href", want: "/posts/demo/"},
		{name: "unquoted", attrs: `href=/posts/demo/ class=x`, attr: "href", want: "/posts/demo/"},
		{name: "missing", attrs: `class="x"`, attr: "href", want: ""},
		{name: "empty rest", attrs: `href=`, attr: "href", want: ""},
		{name: "unterminated quote", attrs: `href="/posts/demo/`, attr: "href", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractAttr(tt.attrs, tt.attr); got != tt.want {
				t.Fatalf("extractAttr(%q, %q) = %q, want %q", tt.attrs, tt.attr, got, tt.want)
			}
		})
	}
}

func TestExtractHTMLLinks(t *testing.T) {
	html := `<a href="/one/">One</a><A class="x" href='/two/'>Two</A><a href=/three/>Three</a><a>NoHref</a>`
	got := extractHTMLLinks(html)
	want := []string{"/one/", "/two/", "/three/"}
	if len(got) != len(want) {
		t.Fatalf("extractHTMLLinks() = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("extractHTMLLinks()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if got := extractHTMLLinks("   "); got != nil {
		t.Fatalf("extractHTMLLinks(blank) = %#v, want nil", got)
	}
}

func TestHashFunctionsDeterministic(t *testing.T) {
	public := site.Page{
		Title:      "Demo",
		Summary:    "Summary",
		Date:       "2026-08-03",
		Lang:       "en",
		URL:        "https://example.test/demo/",
		Tags:       []string{"a", "b"},
		Categories: []string{"c"},
		RawHTML:    "<article>demo</article>",
	}
	if got := hashPublicPage(public); len(got) != 16 {
		t.Fatalf("hashPublicPage() len = %d, want 16", len(got))
	}
	if got1, got2 := hashPublicPage(public), hashPublicPage(public); got1 != got2 {
		t.Fatalf("hashPublicPage() not deterministic: %q vs %q", got1, got2)
	}

	source := hugosite.SourcePage{
		Title:      "Demo",
		Date:       "2026-08-03",
		Body:       "body",
		Draft:      true,
		Tags:       []string{"a"},
		Categories: []string{"c"},
	}
	if got := hashSourcePage(source); len(got) != 16 {
		t.Fatalf("hashSourcePage() len = %d, want 16", len(got))
	}
	if got1, got2 := hashSourcePage(source), hashSourcePage(source); got1 != got2 {
		t.Fatalf("hashSourcePage() not deterministic: %q vs %q", got1, got2)
	}
}

func TestTxSyncTagsCatsAndFTSReplaceExistingValues(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()

	res, err := d.db.Exec(`INSERT INTO pages(slug, title, published, indexed_at) VALUES('/demo/', 'Demo', 1, ?)`, now())
	if err != nil {
		t.Fatalf("insert page: %v", err)
	}
	pageID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId: %v", err)
	}

	tx, err := d.db.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := txSyncTags(tx, pageID, []string{"go", "mcp"}); err != nil {
		t.Fatalf("txSyncTags: %v", err)
	}
	if err := txSyncCats(tx, pageID, []string{"infra"}); err != nil {
		t.Fatalf("txSyncCats: %v", err)
	}
	if err := txSyncFTS(tx, "/demo/", "First title", "first summary", []string{"go", "mcp"}, []string{"infra"}); err != nil {
		t.Fatalf("txSyncFTS: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	tx, err = d.db.Begin()
	if err != nil {
		t.Fatalf("Begin (replace): %v", err)
	}
	if err := txSyncTags(tx, pageID, []string{"security"}); err != nil {
		t.Fatalf("txSyncTags (replace): %v", err)
	}
	if err := txSyncCats(tx, pageID, []string{"reliability", "qa"}); err != nil {
		t.Fatalf("txSyncCats (replace): %v", err)
	}
	if err := txSyncFTS(tx, "/demo/", "Second title", "second summary", []string{"security"}, []string{"reliability", "qa"}); err != nil {
		t.Fatalf("txSyncFTS (replace): %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit (replace): %v", err)
	}

	var tagCount int
	if err := d.db.QueryRow(`SELECT COUNT(*) FROM page_tags WHERE page_id = ?`, pageID).Scan(&tagCount); err != nil {
		t.Fatalf("count tags: %v", err)
	}
	if tagCount != 1 {
		t.Fatalf("tag count = %d, want 1", tagCount)
	}
	var catCount int
	if err := d.db.QueryRow(`SELECT COUNT(*) FROM page_categories WHERE page_id = ?`, pageID).Scan(&catCount); err != nil {
		t.Fatalf("count categories: %v", err)
	}
	if catCount != 2 {
		t.Fatalf("category count = %d, want 2", catCount)
	}

	var title, summary, tags, cats string
	if err := d.db.QueryRow(`SELECT title, summary, tags, categories FROM page_fts WHERE slug = ?`, "/demo/").Scan(&title, &summary, &tags, &cats); err != nil {
		t.Fatalf("query fts row: %v", err)
	}
	if title != "Second title" || summary != "second summary" {
		t.Fatalf("fts row = title %q summary %q, want updated values", title, summary)
	}
	if tags != "security" {
		t.Fatalf("fts tags = %q, want %q", tags, "security")
	}
	if cats != "reliability qa" {
		t.Fatalf("fts categories = %q, want %q", cats, "reliability qa")
	}
}

func TestTxSyncLinksFiltersAndStatuses(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()

	siteIdx, err := site.NewIndex(config.Default())
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}
	siteIdx.UpsertPage(site.Page{Slug: "/ok/", Title: "OK", URL: "https://example.test/ok/", Lang: "en"})

	page := site.Page{
		Slug: "/source/",
		URL:  "https://example.test/source/",
		Lang: "en",
		RawHTML: `<a href="#frag">frag</a>
<a href="mailto:test@example.test">mail</a>
<a href="tel:+33123456789">tel</a>
<a href="ftp://example.test/file">ftp</a>
<a href="http://[::1">bad</a>
<a href="https://outside.example/elsewhere/">ext</a>
<a href="/ok/">ok</a>
<a href="/broken/">broken</a>
<a href="/broken/">broken again</a>
<a href="/source/">self</a>`,
	}

	if err := d.SyncPublicPage(page, siteIdx); err != nil {
		t.Fatalf("SyncPublicPage: %v", err)
	}

	rows, err := d.db.Query(`SELECT target, target_slug, status FROM links ORDER BY id`)
	if err != nil {
		t.Fatalf("query links: %v", err)
	}
	defer rows.Close()

	type linkRow struct {
		target string
		slug   string
		status string
	}
	var got []linkRow
	for rows.Next() {
		var row linkRow
		if err := rows.Scan(&row.target, &row.slug, &row.status); err != nil {
			t.Fatalf("scan link: %v", err)
		}
		got = append(got, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows err: %v", err)
	}

	want := []linkRow{
		{target: "https://outside.example/elsewhere/", slug: "", status: "external"},
		{target: "/ok/", slug: "/ok/", status: "ok"},
		{target: "/broken/", slug: "/broken/", status: "broken"},
	}
	if len(got) != len(want) {
		t.Fatalf("link row count = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("link row[%d] = %#v, want %#v", i, got[i], want[i])
		}
	}
}

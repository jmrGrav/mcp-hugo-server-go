// Package db provides a SQLite-backed derived index for the Hugo MCP server.
// It is optional: when db_path is unset the server falls back to the
// existing in-memory index behaviour. The database is always re-derivable
// from scratch by deleting the file — Markdown files remain the source of truth.
package db

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
	_ "modernc.org/sqlite"
)

// SearchResult is a single FTS5 match returned by Search.
type SearchResult struct {
	Slug    string
	Title   string
	Summary string
	Snippet string
}

// BrokenLinkRecord is a broken internal link from the links table.
type BrokenLinkRecord struct {
	SourceSlug  string
	SourceTitle string
	Target      string
	AnchorText  string
}

// DB wraps a SQLite database used as the MCP site index.
type DB struct {
	db *sql.DB
}

// Open opens (or creates) the SQLite database at path and runs migrations.
func Open(path string) (*DB, error) {
	sqlDB, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("db: open %q: %w", path, err)
	}
	// Single connection so PRAGMA foreign_keys=ON holds for every statement.
	// journal_mode=WAL is file-level and survives pool rotation; foreign_keys is not.
	sqlDB.SetMaxOpenConns(1)
	if _, err := sqlDB.Exec("PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON;"); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("db: pragmas: %w", err)
	}
	d := &DB{db: sqlDB}
	if err := d.createTables(); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("db: create tables: %w", err)
	}
	return d, nil
}

func (d *DB) Close() error { return d.db.Close() }

func (d *DB) createTables() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS pages (
			id           INTEGER PRIMARY KEY,
			slug         TEXT UNIQUE NOT NULL,
			source_path  TEXT DEFAULT '',
			lang         TEXT DEFAULT '',
			title        TEXT DEFAULT '',
			summary      TEXT DEFAULT '',
			date         TEXT DEFAULT '',
			draft        INTEGER DEFAULT 0,
			content_hash TEXT DEFAULT '',
			url          TEXT DEFAULT '',
			published    INTEGER DEFAULT 1,
			indexed_at   TEXT DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS page_tags (
			page_id INTEGER NOT NULL REFERENCES pages(id) ON DELETE CASCADE,
			tag     TEXT NOT NULL,
			PRIMARY KEY (page_id, tag)
		)`,
		`CREATE TABLE IF NOT EXISTS page_categories (
			page_id  INTEGER NOT NULL REFERENCES pages(id) ON DELETE CASCADE,
			category TEXT NOT NULL,
			PRIMARY KEY (page_id, category)
		)`,
		`CREATE TABLE IF NOT EXISTS links (
			id             INTEGER PRIMARY KEY,
			source_page_id INTEGER NOT NULL REFERENCES pages(id) ON DELETE CASCADE,
			target         TEXT NOT NULL,
			target_slug    TEXT DEFAULT '',
			anchor_text    TEXT DEFAULT '',
			status         TEXT DEFAULT 'unchecked'
		)`,
		`CREATE INDEX IF NOT EXISTS idx_links_broken ON links(status)`,
		`CREATE INDEX IF NOT EXISTS idx_links_target_slug ON links(target_slug)`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS page_fts USING fts5(
			slug UNINDEXED,
			title,
			summary,
			tags,
			categories,
			tokenize='unicode61'
		)`,
		`CREATE TABLE IF NOT EXISTS site_health_snapshots (
			id          INTEGER PRIMARY KEY,
			captured_at TEXT NOT NULL,
			payload     TEXT NOT NULL
		)`,
	}
	for _, s := range stmts {
		if _, err := d.db.Exec(s); err != nil {
			return fmt.Errorf("exec %q: %w", s[:min(40, len(s))], err)
		}
	}
	return nil
}

// SyncPublicPage upserts a public (published) page, its taxonomy, its link graph,
// and its FTS entry. It is hash-gated: unchanged pages are skipped.
func (d *DB) SyncPublicPage(p site.Page, siteIdx *site.Index) error {
	hash := hashPublicPage(p)

	// Quick hash check before opening a transaction.
	var existing string
	_ = d.db.QueryRow("SELECT content_hash FROM pages WHERE slug = ?", p.Slug).Scan(&existing)
	if existing == hash {
		return nil
	}

	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	var id int64
	err = tx.QueryRow(`
		INSERT INTO pages(slug, lang, title, summary, date, draft, content_hash, url, published, indexed_at)
		VALUES (?, ?, ?, ?, ?, 0, ?, ?, 1, ?)
		ON CONFLICT(slug) DO UPDATE SET
			lang=excluded.lang, title=excluded.title, summary=excluded.summary,
			date=excluded.date, content_hash=excluded.content_hash,
			url=excluded.url, published=1, indexed_at=excluded.indexed_at
		RETURNING id`,
		p.Slug, p.Lang, p.Title, p.Summary, p.Date, hash, p.URL, now(),
	).Scan(&id)
	if err != nil {
		return err
	}

	if err := txSyncTags(tx, id, p.Tags); err != nil {
		return err
	}
	if err := txSyncCats(tx, id, p.Categories); err != nil {
		return err
	}
	if err := txSyncLinks(tx, id, p, siteIdx); err != nil {
		return err
	}
	if err := txSyncFTS(tx, p.Slug, p.Title, p.Summary, p.Tags, p.Categories); err != nil {
		return err
	}
	return tx.Commit()
}

// SyncSourcePage upserts a source (draft/markdown) page and its taxonomy and FTS entry.
func (d *DB) SyncSourcePage(p hugosite.SourcePage) error {
	hash := hashSourcePage(p)

	var existing string
	_ = d.db.QueryRow("SELECT content_hash FROM pages WHERE slug = ?", p.Slug).Scan(&existing)
	if existing == hash {
		return nil
	}

	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	var id int64
	err = tx.QueryRow(`
		INSERT INTO pages(slug, source_path, lang, title, date, draft, content_hash, published, indexed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, 0, ?)
		ON CONFLICT(slug) DO UPDATE SET
			source_path=excluded.source_path, lang=excluded.lang, title=excluded.title,
			date=excluded.date, draft=excluded.draft, content_hash=excluded.content_hash,
			published=0, indexed_at=excluded.indexed_at
		RETURNING id`,
		p.Slug, p.FilePath, p.Lang, p.Title, p.Date, boolToInt(p.Draft), hash, now(),
	).Scan(&id)
	if err != nil {
		return err
	}

	if err := txSyncTags(tx, id, p.Tags); err != nil {
		return err
	}
	if err := txSyncCats(tx, id, p.Categories); err != nil {
		return err
	}
	if err := txSyncFTS(tx, p.Slug, p.Title, "", p.Tags, p.Categories); err != nil {
		return err
	}
	return tx.Commit()
}

// DeletePage removes a page and all its dependent rows (cascade) from the DB.
func (d *DB) DeletePage(slug string) error {
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.Exec("DELETE FROM page_fts WHERE slug = ?", slug); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM pages WHERE slug = ?", slug); err != nil {
		return err
	}
	return tx.Commit()
}

// Search runs an FTS5 query and returns ranked results with summary snippets.
// Returns nil, nil when the DB is not initialised or the query is empty.
func (d *DB) Search(query string, limit int) ([]SearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 1000
	}
	rows, err := d.db.Query(`
		SELECT f.slug, f.title, f.summary,
		       snippet(page_fts, 2, '<<', '>>', '...', 10) AS snippet
		FROM page_fts f
		JOIN pages p ON p.slug = f.slug
		WHERE page_fts MATCH ?
		  AND p.published = 1
		  AND p.draft = 0
		ORDER BY rank
		LIMIT ?`,
		query, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		if err := rows.Scan(&r.Slug, &r.Title, &r.Summary, &r.Snippet); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// GetBrokenLinks returns all broken internal links recorded in the links table.
func (d *DB) GetBrokenLinks() ([]BrokenLinkRecord, error) {
	rows, err := d.db.Query(`
		SELECT p.slug, p.title, l.target, l.anchor_text
		FROM links l
		JOIN pages p ON p.id = l.source_page_id
		WHERE l.status = 'broken'
		ORDER BY p.slug, l.target`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []BrokenLinkRecord
	for rows.Next() {
		var r BrokenLinkRecord
		if err := rows.Scan(&r.SourceSlug, &r.SourceTitle, &r.Target, &r.AnchorText); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// SnapshotHealth persists a JSON health snapshot (for Phase 3 history).
func (d *DB) SnapshotHealth(payload string) error {
	_, err := d.db.Exec(
		"INSERT INTO site_health_snapshots(captured_at, payload) VALUES(?, ?)",
		now(), payload,
	)
	return err
}

// SnapshotSiteHealth queries current DB state and writes a health snapshot.
// Called by the build_site callback after PostBuildSync completes.
func (d *DB) SnapshotSiteHealth() error {
	var totalPages, brokenLinks int
	_ = d.db.QueryRow("SELECT COUNT(*) FROM pages WHERE published=1 AND draft=0").Scan(&totalPages)
	_ = d.db.QueryRow("SELECT COUNT(*) FROM links WHERE status='broken'").Scan(&brokenLinks)
	payload := fmt.Sprintf(`{"total_pages":%d,"broken_links":%d}`, totalPages, brokenLinks)
	return d.SnapshotHealth(payload)
}

// StartupSync performs a hash-gated full reindex from the in-memory indexes.
// Pages already in the DB with matching content hashes are skipped.
// Stale DB entries (no longer in either index) are deleted.
func (d *DB) StartupSync(siteIdx *site.Index, srcIdx *hugosite.SourceIndex) error {
	// Load current DB hashes.
	hashes, err := d.allHashes()
	if err != nil {
		return err
	}

	// publicSourceSlugs collects every source-slug candidate (bare and
	// language-stripped, via the same site.SourceSlugCandidates lookup the
	// page resolver uses) of each page just synced by SyncPublicPage. A
	// source page whose own bare slug appears here already has a built
	// public counterpart with its own pages/page_fts row — syncing it again
	// under the bare source slug would create a second row for the same
	// logical page (#475), which search_content's FTS path (keyed off the
	// public index) can never reach anyway.
	publicSourceSlugs := make(map[string]bool)
	if siteIdx != nil {
		for _, p := range siteIdx.Sitemap() {
			delete(hashes, p.Slug)
			if err := d.SyncPublicPage(p, siteIdx); err != nil {
				slog.Warn("db: startup sync: public page", "slug", p.Slug, "error", err)
				continue
			}
			for _, c := range site.SourceSlugCandidates(strings.Trim(p.Slug, "/")) {
				publicSourceSlugs[c] = true
			}
		}
	}
	if srcIdx != nil {
		for _, p := range srcIdx.ListPages(0, 0) {
			if publicSourceSlugs[p.Slug] {
				// Deliberately do NOT delete(hashes, p.Slug) here: if a
				// duplicate row already exists under this bare slug from
				// before this fix (or from a write-path call to
				// SyncSourcePage while the page was still source-only), it
				// must stay in `hashes` so the orphan-cleanup pass below
				// deletes it, instead of being silently left behind forever.
				continue
			}
			delete(hashes, p.Slug)
			if err := d.SyncSourcePage(p); err != nil {
				slog.Warn("db: startup sync: source page", "slug", p.Slug, "error", err)
			}
		}
	}

	// Delete orphaned entries.
	for slug := range hashes {
		if err := d.DeletePage(slug); err != nil {
			slog.Warn("db: startup sync: delete orphan", "slug", slug, "error", err)
		}
	}
	return nil
}

// PostBuildSync reindexes the public site index after a successful build.
func (d *DB) PostBuildSync(siteIdx *site.Index) error {
	if siteIdx == nil {
		return nil
	}
	for _, p := range siteIdx.Sitemap() {
		if err := d.SyncPublicPage(p, siteIdx); err != nil {
			slog.Warn("db: post-build sync: public page", "slug", p.Slug, "error", err)
		}
	}
	return nil
}

// --- helpers ---

func (d *DB) allHashes() (map[string]string, error) {
	rows, err := d.db.Query("SELECT slug, content_hash FROM pages")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := make(map[string]string)
	for rows.Next() {
		var slug, hash string
		if err := rows.Scan(&slug, &hash); err != nil {
			return nil, err
		}
		m[slug] = hash
	}
	return m, rows.Err()
}

func txSyncTags(tx *sql.Tx, pageID int64, tags []string) error {
	if _, err := tx.Exec("DELETE FROM page_tags WHERE page_id = ?", pageID); err != nil {
		return err
	}
	for _, tag := range tags {
		if _, err := tx.Exec("INSERT OR IGNORE INTO page_tags(page_id, tag) VALUES(?,?)", pageID, tag); err != nil {
			return err
		}
	}
	return nil
}

func txSyncCats(tx *sql.Tx, pageID int64, cats []string) error {
	if _, err := tx.Exec("DELETE FROM page_categories WHERE page_id = ?", pageID); err != nil {
		return err
	}
	for _, cat := range cats {
		if _, err := tx.Exec("INSERT OR IGNORE INTO page_categories(page_id, category) VALUES(?,?)", pageID, cat); err != nil {
			return err
		}
	}
	return nil
}

func txSyncFTS(tx *sql.Tx, slug, title, summary string, tags, cats []string) error {
	if _, err := tx.Exec("DELETE FROM page_fts WHERE slug = ?", slug); err != nil {
		return err
	}
	_, err := tx.Exec(
		"INSERT INTO page_fts(slug, title, summary, tags, categories) VALUES(?,?,?,?,?)",
		slug, title, summary,
		strings.Join(tags, " "),
		strings.Join(cats, " "),
	)
	return err
}

// txSyncLinks extracts links from page.RawHTML, resolves them against siteIdx,
// and stores them in the links table within the given transaction.
func txSyncLinks(tx *sql.Tx, pageID int64, p site.Page, siteIdx *site.Index) error {
	if _, err := tx.Exec("DELETE FROM links WHERE source_page_id = ?", pageID); err != nil {
		return err
	}
	if p.RawHTML == "" || p.URL == "" {
		return nil
	}
	base, err := url.Parse(p.URL)
	if err != nil || base == nil {
		return nil
	}

	seen := make(map[string]bool)
	for _, href := range extractHTMLLinks(p.RawHTML) {
		if strings.HasPrefix(href, "#") || strings.HasPrefix(href, "mailto:") || strings.HasPrefix(href, "tel:") {
			continue
		}
		ref, err := url.Parse(href)
		if err != nil {
			continue
		}
		if ref.Scheme != "" && ref.Scheme != "http" && ref.Scheme != "https" {
			continue
		}
		target := base.ResolveReference(ref)

		if target.Host != "" && target.Host != base.Host {
			// External — store but don't count as broken.
			if _, err := tx.Exec(
				"INSERT INTO links(source_page_id, target, target_slug, anchor_text, status) VALUES(?,?,'','','external')",
				pageID, href,
			); err != nil {
				return err
			}
			continue
		}

		targetSlug := site.NormalizeSlug(target.Path)
		if seen[targetSlug] || targetSlug == p.Slug {
			continue
		}
		seen[targetSlug] = true

		status := "broken"
		if siteIdx != nil {
			if _, found := siteIdx.GetBySlug(targetSlug); found {
				status = "ok"
			}
		}
		if _, err := tx.Exec(
			"INSERT INTO links(source_page_id, target, target_slug, anchor_text, status) VALUES(?,?,?,?,?)",
			pageID, href, targetSlug, "", status,
		); err != nil {
			return err
		}
	}
	return nil
}

// extractHTMLLinks extracts <a href> values from raw HTML.
func extractHTMLLinks(rawHTML string) []string {
	if strings.TrimSpace(rawHTML) == "" {
		return nil
	}
	// Simple pattern scan — avoids importing x/net/html here.
	var out []string
	s := rawHTML
	for {
		i := strings.Index(strings.ToLower(s), "<a ")
		if i < 0 {
			break
		}
		s = s[i+3:]
		j := strings.IndexByte(s, '>')
		if j < 0 {
			break
		}
		attrs := s[:j]
		s = s[j+1:]
		href := extractAttr(attrs, "href")
		if href != "" {
			out = append(out, href)
		}
	}
	return out
}

func extractAttr(attrs, name string) string {
	lower := strings.ToLower(attrs)
	needle := name + "="
	i := strings.Index(lower, needle)
	if i < 0 {
		return ""
	}
	rest := attrs[i+len(needle):]
	if len(rest) == 0 {
		return ""
	}
	if rest[0] == '"' || rest[0] == '\'' {
		q := rest[0]
		rest = rest[1:]
		end := strings.IndexByte(rest, q)
		if end < 0 {
			return ""
		}
		return strings.TrimSpace(rest[:end])
	}
	// Unquoted attribute value.
	end := strings.IndexAny(rest, " \t\r\n>")
	if end < 0 {
		return strings.TrimSpace(rest)
	}
	return strings.TrimSpace(rest[:end])
}

func hashPublicPage(p site.Page) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%s|%s|%s|%s|%s|%s|%s",
		p.Title, p.Summary, p.Date, p.Lang, p.URL,
		strings.Join(p.Tags, ","), strings.Join(p.Categories, ","),
		p.RawHTML,
	)
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func hashSourcePage(p hugosite.SourcePage) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%s|%s|%t|%s|%s",
		p.Title, p.Date, p.Body, p.Draft,
		strings.Join(p.Tags, ","), strings.Join(p.Categories, ","),
	)
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func now() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

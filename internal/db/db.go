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

// PublicationManifest records facts observed after a successful Hugo build.
// It is deliberately a derived operational record: Markdown/Git remain the
// source-of-truth for source content and public/ remains the source-of-truth
// for rendered output. The revisions are fingerprints of those two trees at
// the point the build completed, not revisions to be trusted over the files.
type PublicationManifest struct {
	BuildID        string
	SourceRevision string
	OutputRevision string
	HugoVersion    string
	Status         string
	ObservedAt     time.Time
}

// MutationJournalEntry is a caller-scoped successful mutation result retained
// for idempotent replay and get_mutation_status after a process restart.
type MutationJournalEntry struct {
	CallerKey   string
	Tool        string
	Key         string
	RequestHash string
	ResultJSON  []byte
	CreatedAt   time.Time
}

// EphemeralRecord is caller-owned TTL state returned for one record kind.
// Domain payloads remain in the write package; SQLite only enforces lifetime
// and ownership across a process restart.
type EphemeralRecord struct {
	ID        string
	Payload   []byte
	CreatedAt time.Time
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
		`CREATE TABLE IF NOT EXISTS publication_manifests (
			id              INTEGER PRIMARY KEY,
			build_id        TEXT NOT NULL UNIQUE,
			source_revision TEXT NOT NULL,
			output_revision TEXT NOT NULL,
			hugo_version    TEXT NOT NULL DEFAULT '',
			status          TEXT NOT NULL,
			observed_at     TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_publication_manifests_observed_at
			ON publication_manifests(observed_at DESC)`,
		`CREATE TABLE IF NOT EXISTS mutation_journal (
			caller_key TEXT NOT NULL, tool TEXT NOT NULL, idempotency_key TEXT NOT NULL,
			request_hash TEXT NOT NULL, result_json BLOB NOT NULL, created_at TEXT NOT NULL,
			PRIMARY KEY(caller_key, tool, idempotency_key)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_mutation_journal_created_at ON mutation_journal(created_at)`,
		`CREATE TABLE IF NOT EXISTS ephemeral_records (
			kind TEXT NOT NULL, record_id TEXT NOT NULL, caller_key TEXT NOT NULL,
			payload BLOB NOT NULL, created_at TEXT NOT NULL,
			PRIMARY KEY(kind, record_id)
		)`,
	}
	for _, s := range stmts {
		if _, err := d.db.Exec(s); err != nil {
			return fmt.Errorf("exec %q: %w", s[:min(40, len(s))], err)
		}
	}
	return nil
}

// PutEphemeralRecord stores server-owned TTL state such as plans/snapshots.
// The caller key is persisted with the payload boundary so a restarted server
// retains the same isolation rule as the in-memory stores.
func (d *DB) PutEphemeralRecord(kind, id, callerKey string, payload []byte, createdAt time.Time) error {
	if kind == "" || id == "" || len(payload) == 0 {
		return fmt.Errorf("ephemeral record: kind, id, and payload are required")
	}
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	_, err := d.db.Exec(`INSERT INTO ephemeral_records(kind,record_id,caller_key,payload,created_at) VALUES(?,?,?,?,?) ON CONFLICT(kind,record_id) DO UPDATE SET caller_key=excluded.caller_key,payload=excluded.payload,created_at=excluded.created_at`, kind, id, callerKey, payload, createdAt.UTC().Format(time.RFC3339Nano))
	return err
}

func (d *DB) GetEphemeralRecord(kind, id, callerKey string, ttl time.Duration) ([]byte, bool, error) {
	var owner, created string
	var payload []byte
	err := d.db.QueryRow(`SELECT caller_key,payload,created_at FROM ephemeral_records WHERE kind=? AND record_id=?`, kind, id).Scan(&owner, &payload, &created)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if owner != callerKey {
		return nil, false, nil
	}
	at, err := time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return nil, false, err
	}
	if ttl > 0 && time.Since(at) > ttl {
		_, _ = d.db.Exec(`DELETE FROM ephemeral_records WHERE kind=? AND record_id=?`, kind, id)
		return nil, false, nil
	}
	return payload, true, nil
}

func (d *DB) DeleteEphemeralRecord(kind, id, callerKey string) error {
	_, err := d.db.Exec(`DELETE FROM ephemeral_records WHERE kind=? AND record_id=? AND caller_key=?`, kind, id, callerKey)
	return err
}

// ListEphemeralRecords returns only unexpired records owned by callerKey.
// Expiry is enforced here as well as on point lookup so a restart cannot make
// an old snapshot listable beyond its advertised retention window.
func (d *DB) ListEphemeralRecords(kind, callerKey string, ttl time.Duration) ([]EphemeralRecord, error) {
	rows, err := d.db.Query(`SELECT record_id,payload,created_at FROM ephemeral_records WHERE kind=? AND caller_key=? ORDER BY created_at DESC`, kind, callerKey)
	if err != nil {
		return nil, err
	}
	var records []EphemeralRecord
	var expiredIDs []string
	for rows.Next() {
		var record EphemeralRecord
		var created string
		if err := rows.Scan(&record.ID, &record.Payload, &created); err != nil {
			rows.Close()
			return nil, err
		}
		at, err := time.Parse(time.RFC3339Nano, created)
		if err != nil {
			rows.Close()
			return nil, err
		}
		if ttl > 0 && time.Since(at) > ttl {
			expiredIDs = append(expiredIDs, record.ID)
			continue
		}
		record.CreatedAt = at.UTC()
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	// rows must be fully drained and closed before issuing another statement
	// on this *sql.DB: with SetMaxOpenConns(1), a nested Exec while rows still
	// holds the only connection checked out would deadlock forever.
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for _, id := range expiredIDs {
		if _, err := d.db.Exec(`DELETE FROM ephemeral_records WHERE kind=? AND record_id=? AND caller_key=?`, kind, id, callerKey); err != nil {
			return nil, err
		}
	}
	return records, nil
}

// RecordPublicationManifest persists a completed build's observed source and
// public fingerprints. Replaying the same build ID is idempotent, which is
// important if the process dies after the output swap but before the callback
// returns to the MCP transport.
func (d *DB) RecordPublicationManifest(m PublicationManifest) error {
	if strings.TrimSpace(m.BuildID) == "" {
		return fmt.Errorf("publication manifest: build_id is required")
	}
	if strings.TrimSpace(m.SourceRevision) == "" || strings.TrimSpace(m.OutputRevision) == "" {
		return fmt.Errorf("publication manifest: source_revision and output_revision are required")
	}
	if strings.TrimSpace(m.Status) == "" {
		return fmt.Errorf("publication manifest: status is required")
	}
	if m.ObservedAt.IsZero() {
		m.ObservedAt = time.Now().UTC()
	}
	_, err := d.db.Exec(`
		INSERT INTO publication_manifests(build_id, source_revision, output_revision, hugo_version, status, observed_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(build_id) DO UPDATE SET
			source_revision=excluded.source_revision,
			output_revision=excluded.output_revision,
			hugo_version=excluded.hugo_version,
			status=excluded.status,
			observed_at=excluded.observed_at`,
		m.BuildID, m.SourceRevision, m.OutputRevision, m.HugoVersion, m.Status,
		m.ObservedAt.UTC().Format(time.RFC3339Nano),
	)
	return err
}

// LatestPublicationManifest returns the newest completed build record, or
// nil when this server has not recorded a build yet. Callers must still
// reconcile it against the filesystem before treating it as current.
func (d *DB) LatestPublicationManifest() (*PublicationManifest, error) {
	var m PublicationManifest
	var observed string
	err := d.db.QueryRow(`
		SELECT build_id, source_revision, output_revision, hugo_version, status, observed_at
		FROM publication_manifests
		ORDER BY observed_at DESC, id DESC
		LIMIT 1`).Scan(&m.BuildID, &m.SourceRevision, &m.OutputRevision, &m.HugoVersion, &m.Status, &observed)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	parsed, err := time.Parse(time.RFC3339Nano, observed)
	if err != nil {
		return nil, fmt.Errorf("publication manifest: parse observed_at: %w", err)
	}
	m.ObservedAt = parsed.UTC()
	return &m, nil
}

func (d *DB) RememberMutation(e MutationJournalEntry) error {
	if e.CallerKey == "" || e.Tool == "" || e.Key == "" || e.RequestHash == "" || len(e.ResultJSON) == 0 {
		return fmt.Errorf("mutation journal: caller, tool, key, request hash, and result are required")
	}
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now().UTC()
	}
	_, err := d.db.Exec(`INSERT INTO mutation_journal(caller_key, tool, idempotency_key, request_hash, result_json, created_at) VALUES(?,?,?,?,?,?) ON CONFLICT(caller_key, tool, idempotency_key) DO UPDATE SET request_hash=excluded.request_hash, result_json=excluded.result_json, created_at=excluded.created_at`, e.CallerKey, e.Tool, e.Key, e.RequestHash, e.ResultJSON, e.CreatedAt.UTC().Format(time.RFC3339Nano))
	return err
}

func (d *DB) LookupMutation(callerKey, tool, key string, ttl time.Duration) (*MutationJournalEntry, error) {
	var e MutationJournalEntry
	var created string
	err := d.db.QueryRow(`SELECT request_hash, result_json, created_at FROM mutation_journal WHERE caller_key=? AND tool=? AND idempotency_key=?`, callerKey, tool, key).Scan(&e.RequestHash, &e.ResultJSON, &created)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	e.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return nil, fmt.Errorf("mutation journal: parse created_at: %w", err)
	}
	if ttl > 0 && time.Since(e.CreatedAt) > ttl {
		_, _ = d.db.Exec(`DELETE FROM mutation_journal WHERE caller_key=? AND tool=? AND idempotency_key=?`, callerKey, tool, key)
		return nil, nil
	}
	e.CallerKey, e.Tool, e.Key = callerKey, tool, key
	return &e, nil
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

// PostBuildSync reindexes the public site index after a successful build,
// then prunes any published DB rows for pages no longer in the sitemap.
//
// The prune step closes a reconciliation gap (#646): delete_page performs a
// best-effort siteDB.DeletePage call that can fail independently of the disk
// removal (reported via the existing partial_success/warning convention).
// Previously, the only code path that ever cleaned up such an orphaned row
// was StartupSync, which runs once at process boot — on a long-running,
// low-traffic deployment a failed delete could leave a stale row (and a
// stale search_content hit) in place for weeks. Running the same prune on
// every post-build callback means it self-heals on the very next build.
func (d *DB) PostBuildSync(siteIdx *site.Index) error {
	if siteIdx == nil {
		return nil
	}
	want := make(map[string]bool, len(siteIdx.Sitemap()))
	for _, p := range siteIdx.Sitemap() {
		want[p.Slug] = true
		if err := d.SyncPublicPage(p, siteIdx); err != nil {
			slog.Warn("db: post-build sync: public page", "slug", p.Slug, "error", err)
		}
	}

	rows, err := d.db.Query("SELECT slug FROM pages WHERE published = 1")
	if err != nil {
		slog.Warn("db: post-build sync: list published pages", "error", err)
		return nil
	}
	var stale []string
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err != nil {
			continue
		}
		if !want[slug] {
			stale = append(stale, slug)
		}
	}
	rows.Close()

	for _, slug := range stale {
		if err := d.DeletePage(slug); err != nil {
			slog.Warn("db: post-build sync: delete stale page", "slug", slug, "error", err)
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

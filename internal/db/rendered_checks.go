package db

import (
	"database/sql"
	"errors"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
)

// RenderedCheckFn computes the number of failing inspect_rendered-style
// checks for a public page. Injected by the caller (internal/server, at
// process startup) rather than implemented in this package: computing it
// means parsing rendered HTML and running internal/tools/read's exact check
// functions, and this package must not import internal/tools/read (that
// would invert the codebase's layering — tools depend on db, not the
// reverse). See DB.SetRenderedCheckFn.
//
// ok is false when the count could not be computed (e.g. the rendered HTML
// file is missing/unreadable) — the caller (syncPublicPage) leaves any
// previously cached count untouched in that case rather than overwrite it
// with a misleading 0.
type RenderedCheckFn func(p site.Page) (issuesCount int, ok bool)

// SetRenderedCheckFn installs the function syncPublicPage calls to compute
// and cache each page's rendered_issues_count. Left nil (the zero value),
// the rendered-checks columns are simply never populated — the same "not
// computed, not a lying zero" contract get_site_health's other opt-in
// fields already use elsewhere in this codebase.
func (d *DB) SetRenderedCheckFn(fn RenderedCheckFn) {
	d.renderedCheckFn = fn
}

// migrateRenderedChecksSchema adds the rendered-checks cache columns to the
// pre-existing pages table (a fresh DB already gets them from createTables;
// this covers a DB file created before #1151) and creates the single-row
// template_fingerprint table used to invalidate that cache on a
// template/theme change — see ComputeTemplateFingerprint's doc comment in
// internal/tools/read for why a per-page content hash alone cannot detect
// that class of change.
func (d *DB) migrateRenderedChecksSchema() error {
	if err := addColumnIfMissing(d.db, "pages", "rendered_issues_count", "INTEGER"); err != nil {
		return err
	}
	if err := addColumnIfMissing(d.db, "pages", "rendered_checked_at", "TEXT DEFAULT ''"); err != nil {
		return err
	}
	_, err := d.db.Exec(`CREATE TABLE IF NOT EXISTS template_fingerprint (
		id          INTEGER PRIMARY KEY CHECK (id = 1),
		fingerprint TEXT NOT NULL,
		updated_at  TEXT NOT NULL
	)`)
	return err
}

// addColumnIfMissing adds column to table with the given DDL type/default
// only if it does not already exist. SQLite has no "ADD COLUMN IF NOT
// EXISTS" — PRAGMA table_info is the standard way to check first instead of
// attempting the ALTER and pattern-matching the driver's error string.
func addColumnIfMissing(db *sql.DB, table, column, ddlType string) error {
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notNull int
		var dflt sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err != nil {
			return err
		}
		if name == column {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = db.Exec("ALTER TABLE " + table + " ADD COLUMN " + column + " " + ddlType)
	return err
}

// SyncTemplateFingerprint compares fp against the site-wide fingerprint
// stored from the previous build. It persists fp either way and reports
// changed=true when it differs from what was stored (including the
// brand-new-DB case: no prior row means every page's rendered-checks cache
// is unpopulated, which the caller should treat the same as a template
// change — nothing to short-circuit on yet). One row for the whole site,
// not one per page (#1151): every page shares the same template, so a
// mismatch means every page's cached rendered_issues_count needs
// recomputing, not just some.
func (d *DB) SyncTemplateFingerprint(fp string) (changed bool, err error) {
	var existing string
	scanErr := d.db.QueryRow("SELECT fingerprint FROM template_fingerprint WHERE id = 1").Scan(&existing)
	if scanErr != nil && !errors.Is(scanErr, sql.ErrNoRows) {
		return false, scanErr
	}
	if scanErr == nil && existing == fp {
		return false, nil
	}
	if _, err := d.db.Exec(`
		INSERT INTO template_fingerprint(id, fingerprint, updated_at) VALUES (1, ?, ?)
		ON CONFLICT(id) DO UPDATE SET fingerprint=excluded.fingerprint, updated_at=excluded.updated_at`,
		fp, now(),
	); err != nil {
		return false, err
	}
	return true, nil
}

// renderedChecksScopeSchemaVersion identifies the one-time reconciliation
// pass below, the same "run once per DB file, bump on policy change"
// convention linksIgnorePolicySchemaVersion already uses.
const renderedChecksScopeSchemaVersion = 1

// reconcileRenderedChecksScope is #1156's one-time catch-up: before this
// fix, syncPublicPage's forceRenderedRecheck sweep (#1151) populated
// rendered_issues_count for every published route in the pages table —
// taxonomy/section/pagination/technical routes included, not just ordinary
// content pages — because it never consulted classification at all.
// Confirmed live: rendered_seo_summary.pages_checked reported 245 against a
// site with 82 publishable content pages. Those non-content rows are
// hash-gated (SyncPublicPage: "if existing == hash { return nil }"), so a
// stable taxonomy page that never changes would carry a stale, in-scope
// rendered_issues_count forever without this pass — the deploy that ships
// the new content-only gating (internal/server's RenderedCheckFn wiring)
// does not retroactively clear rows a previous process already wrote.
// This runs once per DB file: classify every currently-published page from
// its own stored slug (NewClassifierFromPages needs only the sibling slug
// set to detect section/taxonomy roots, no live site.Index required) and
// null out rendered_issues_count/rendered_checked_at for every page that
// isn't ordinary content, so RenderedIssuesSummary's "checked" count only
// reflects the checks a caller can actually act on.
func (d *DB) reconcileRenderedChecksScope() error {
	var applied int
	_ = d.db.QueryRow(`SELECT version FROM derived_schema_migrations WHERE name = 'rendered_checks_scope'`).Scan(&applied)
	if applied >= renderedChecksScopeSchemaVersion {
		return nil
	}

	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	// Classify against every published slug, not just the ones this pass is
	// about to fix: NewClassifierFromPages derives section roots from the
	// sibling slug set, so restricting that input to already-checked rows
	// would make a real section index misclassify as ordinary content
	// whenever some other page under it hadn't been checked yet.
	rows, err := tx.Query(`SELECT id, slug, rendered_issues_count IS NOT NULL FROM pages WHERE published = 1`)
	if err != nil {
		return err
	}
	type checkedRow struct {
		id   int64
		slug string
	}
	var checkedRows []checkedRow
	slugs := make([]site.Page, 0)
	for rows.Next() {
		var id int64
		var slug string
		var checked bool
		if err := rows.Scan(&id, &slug, &checked); err != nil {
			rows.Close()
			return err
		}
		slugs = append(slugs, site.Page{Slug: slug})
		if checked {
			checkedRows = append(checkedRows, checkedRow{id: id, slug: slug})
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}

	classifier := site.NewClassifierFromPages(slugs)
	for _, r := range checkedRows {
		if classifier.IsContent(site.Page{Slug: r.slug}) {
			continue
		}
		if _, err := tx.Exec(
			`UPDATE pages SET rendered_issues_count = NULL, rendered_checked_at = '' WHERE id = ?`,
			r.id,
		); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`INSERT INTO derived_schema_migrations(name,version,applied_at) VALUES('rendered_checks_scope',?,?) ON CONFLICT(name) DO UPDATE SET version=excluded.version,applied_at=excluded.applied_at`, renderedChecksScopeSchemaVersion, now()); err != nil {
		return err
	}
	return tx.Commit()
}

// RenderedIssuesSummary reports how many published pages have a cached
// rendered-checks result (checked) and how many of those have at least one
// failing check (withIssues), for get_site_health's opt-in aggregation
// (#1151). Pages with no cached result yet (rendered_issues_count IS NULL —
// never synced, or synced before RenderedCheckFn was wired up) are excluded
// from checked rather than counted as clean, so this can never
// under-report.
func (d *DB) RenderedIssuesSummary() (checked, withIssues int, err error) {
	err = d.db.QueryRow(`
		SELECT COUNT(*), COALESCE(SUM(CASE WHEN rendered_issues_count > 0 THEN 1 ELSE 0 END), 0)
		FROM pages
		WHERE published = 1 AND rendered_issues_count IS NOT NULL`,
	).Scan(&checked, &withIssues)
	return checked, withIssues, err
}

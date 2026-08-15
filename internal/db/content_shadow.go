package db

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
)

const contentRepresentationSchemaVersion = 1

// ContentShadowStats is aggregate-only migration telemetry. It deliberately
// contains no body, host path, token, or caller-owned identifier.
type ContentShadowStats struct {
	SchemaVersion       int
	TotalRows           int
	SourceRows          int
	PublicRows          int
	MissingCounterparts int
	LegacyMismatches    int
	MismatchDigest      string
	LanguageCounts      map[string]int
	ObservedAt          time.Time
}

type contentRepresentation struct {
	SourceKey      string
	Lang           string
	Representation string
	LegacySlug     string
	SourcePath     string
	Revision       string
	LegacyRevision string
	ObservedAt     time.Time
}

// migrateContentRepresentations creates the versioned shadow schema and
// backfills facts that can be recovered from the legacy slug-keyed table. The
// legacy table remains the serving index for the complete shadow period.
func (d *DB) migrateContentRepresentations() error {
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS derived_schema_migrations (
			name TEXT PRIMARY KEY, version INTEGER NOT NULL, applied_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS content_representations_v1 (
			source_key TEXT NOT NULL,
			lang TEXT NOT NULL,
			representation TEXT NOT NULL CHECK (representation IN ('source','public')),
			legacy_slug TEXT NOT NULL DEFAULT '',
			source_path TEXT NOT NULL DEFAULT '',
			revision TEXT NOT NULL,
			legacy_revision TEXT NOT NULL,
			observed_at TEXT NOT NULL,
			PRIMARY KEY(source_key,lang,representation)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_content_representations_v1_legacy_slug
			ON content_representations_v1(legacy_slug)`,
		`CREATE TABLE IF NOT EXISTS content_shadow_runs_v1 (
			id INTEGER PRIMARY KEY,
			total_rows INTEGER NOT NULL,
			source_rows INTEGER NOT NULL,
			public_rows INTEGER NOT NULL,
			missing_counterparts INTEGER NOT NULL,
			legacy_mismatches INTEGER NOT NULL,
			mismatch_digest TEXT NOT NULL DEFAULT '',
			language_counts_json TEXT NOT NULL,
			observed_at TEXT NOT NULL
		)`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return err
		}
	}

	rows, err := tx.Query(`SELECT slug,source_path,lang,content_hash,published,indexed_at FROM pages`)
	if err != nil {
		return err
	}
	var backfill []contentRepresentation
	for rows.Next() {
		var slug, sourcePath, lang, revision, observed string
		var published int
		if err := rows.Scan(&slug, &sourcePath, &lang, &revision, &published, &observed); err != nil {
			rows.Close()
			return err
		}
		representation := "source"
		sourceKey := normaliseSourceKey(slug)
		if published != 0 {
			representation = "public"
			sourceKey = unresolvedPublicSourceKey(slug, lang)
		}
		at, parseErr := time.Parse(time.RFC3339Nano, observed)
		if parseErr != nil {
			at = time.Now().UTC()
		}
		backfill = append(backfill, contentRepresentation{
			SourceKey: sourceKey, Lang: normaliseContentLang(lang), Representation: representation,
			LegacySlug: slug, SourcePath: sourcePath, Revision: revision, LegacyRevision: revision, ObservedAt: at,
		})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, r := range backfill {
		var alreadyMapped int
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM content_representations_v1 WHERE legacy_slug=? AND lang=? AND representation=?)`, r.LegacySlug, normaliseContentLang(r.Lang), r.Representation).Scan(&alreadyMapped); err != nil {
			return err
		}
		if alreadyMapped != 0 {
			continue
		}
		if err := upsertContentRepresentationTx(tx, r, true); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`INSERT INTO derived_schema_migrations(name,version,applied_at) VALUES('content_representations',?,?) ON CONFLICT(name) DO UPDATE SET version=excluded.version,applied_at=excluded.applied_at`, contentRepresentationSchemaVersion, now()); err != nil {
		return err
	}
	return tx.Commit()
}

func (d *DB) syncContentRepresentation(r contentRepresentation) error {
	if strings.TrimSpace(r.SourceKey) == "" || (r.Representation != "source" && r.Representation != "public") || strings.TrimSpace(r.Revision) == "" {
		return fmt.Errorf("content representation: source_key, representation, and revision are required")
	}
	if r.ObservedAt.IsZero() {
		r.ObservedAt = time.Now().UTC()
	}
	_, err := d.db.Exec(`INSERT INTO content_representations_v1(source_key,lang,representation,legacy_slug,source_path,revision,legacy_revision,observed_at)
		VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(source_key,lang,representation) DO UPDATE SET
		legacy_slug=excluded.legacy_slug,source_path=excluded.source_path,revision=excluded.revision,legacy_revision=excluded.legacy_revision,observed_at=excluded.observed_at`,
		r.SourceKey, normaliseContentLang(r.Lang), r.Representation, r.LegacySlug, r.SourcePath, r.Revision, r.LegacyRevision, r.ObservedAt.UTC().Format(time.RFC3339Nano))
	return err
}

func upsertContentRepresentationTx(tx *sql.Tx, r contentRepresentation, preserveExisting bool) error {
	conflict := `DO UPDATE SET legacy_slug=excluded.legacy_slug,source_path=excluded.source_path,revision=excluded.revision,legacy_revision=excluded.legacy_revision,observed_at=excluded.observed_at`
	if preserveExisting {
		conflict = `DO NOTHING`
	}
	_, err := tx.Exec(`INSERT INTO content_representations_v1(source_key,lang,representation,legacy_slug,source_path,revision,legacy_revision,observed_at)
		VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(source_key,lang,representation) `+conflict,
		r.SourceKey, normaliseContentLang(r.Lang), r.Representation, r.LegacySlug, r.SourcePath, r.Revision, r.LegacyRevision, r.ObservedAt.UTC().Format(time.RFC3339Nano))
	return err
}

func unresolvedPublicSourceKey(slug, lang string) string {
	digest := sha256.Sum256([]byte(site.NormalizeSlug(slug) + "\x00" + normaliseContentLang(lang)))
	return "@public/" + hex.EncodeToString(digest[:8])
}

func normaliseSourceKey(sourceKey string) string {
	sourceKey = strings.Trim(sourceKey, "/")
	if sourceKey == "" {
		return "."
	}
	return sourceKey
}

func normaliseContentLang(lang string) string {
	return strings.ToLower(strings.TrimSpace(lang))
}

func (d *DB) sourceRepresentation(p hugosite.SourcePage) contentRepresentation {
	lang := normaliseContentLang(p.Lang)
	if lang == "" {
		// Preserve the canonical default-language identity established by the
		// deterministic startup reconciliation for later index.md writes.
		_ = d.db.QueryRow(`SELECT lang FROM content_representations_v1 WHERE legacy_slug=? AND representation='source' AND lang<>'' ORDER BY observed_at DESC LIMIT 1`, p.Slug).Scan(&lang)
	}
	return contentRepresentation{
		SourceKey: normaliseSourceKey(p.Slug), Lang: lang, Representation: "source",
		LegacySlug: p.Slug, SourcePath: p.FilePath, Revision: sourcePageRevision(p), LegacyRevision: hashSourcePage(p), ObservedAt: time.Now().UTC(),
	}
}

func sourceRepresentationResolved(p hugosite.SourcePage, defaultLang string) contentRepresentation {
	lang := normaliseContentLang(p.Lang)
	if lang == "" {
		lang = normaliseContentLang(defaultLang)
	}
	return contentRepresentation{
		SourceKey: normaliseSourceKey(p.Slug), Lang: lang, Representation: "source",
		LegacySlug: p.Slug, SourcePath: p.FilePath, Revision: sourcePageRevision(p), LegacyRevision: hashSourcePage(p), ObservedAt: time.Now().UTC(),
	}
}

func (d *DB) publicRepresentation(p site.Page) contentRepresentation {
	lang := normaliseContentLang(p.Lang)
	sourceKey := ""
	_ = d.db.QueryRow(`SELECT source_key FROM content_representations_v1 WHERE legacy_slug=? AND lang=? AND representation='public' ORDER BY observed_at DESC LIMIT 1`, p.Slug, lang).Scan(&sourceKey)
	if sourceKey == "" {
		candidates := []string{normaliseSourceKey(p.Slug)}
		parts := strings.Split(strings.Trim(p.Slug, "/"), "/")
		if len(parts) > 1 && lang != "" && strings.EqualFold(parts[0], lang) {
			candidates = append(candidates, normaliseSourceKey(strings.Join(parts[1:], "/")))
		}
		for _, candidate := range candidates {
			var matched string
			err := d.db.QueryRow(`SELECT source_key FROM content_representations_v1 WHERE source_key=? AND representation='source' AND (lang=? OR lang='') LIMIT 1`, candidate, lang).Scan(&matched)
			if err == nil {
				sourceKey = matched
				break
			}
		}
	}
	if sourceKey == "" {
		sourceKey = unresolvedPublicSourceKey(p.Slug, lang)
	}
	return contentRepresentation{
		SourceKey: sourceKey, Lang: lang, Representation: "public",
		LegacySlug: p.Slug, Revision: publicPageRevision(p), LegacyRevision: hashPublicPage(p), ObservedAt: time.Now().UTC(),
	}
}

func publicRepresentationResolved(p site.Page, sourceKey, lang string) contentRepresentation {
	return contentRepresentation{
		SourceKey: normaliseSourceKey(sourceKey), Lang: normaliseContentLang(lang), Representation: "public",
		LegacySlug: p.Slug, Revision: publicPageRevision(p), LegacyRevision: hashPublicPage(p), ObservedAt: time.Now().UTC(),
	}
}

// resolvePublicSource maps rendered URLs to source bundles from source facts,
// never by declaring the URL itself to be the source identity. Explicit
// frontmatter url/permalink values cover non-isomorphic Hugo permalinks.
// Ambiguous or unknown mappings remain in an opaque @public namespace and are
// surfaced as shadow counterpart gaps instead of being guessed.
func resolvePublicSource(p site.Page, srcIdx *hugosite.SourceIndex, defaultLang string) (string, string, bool) {
	if srcIdx == nil {
		return "", "", false
	}
	publicLang := normaliseContentLang(p.Lang)
	if publicLang == "" {
		publicLang = normaliseContentLang(defaultLang)
	}
	var matches []hugosite.SourcePage
	for _, source := range srcIdx.ListPages(0, 0) {
		sourceLang := normaliseContentLang(source.Lang)
		if sourceLang == "" {
			sourceLang = normaliseContentLang(defaultLang)
		}
		if sourceLang != publicLang {
			continue
		}
		expected := site.PublicSlugForSourceLang(source.Slug, sourceLang, normaliseContentLang(defaultLang))
		if site.NormalizeSlug(expected) == site.NormalizeSlug(p.Slug) || sourceFrontmatterURLMatches(source, p.Slug) {
			matches = append(matches, source)
		}
	}
	if len(matches) != 1 {
		return "", publicLang, false
	}
	return matches[0].Slug, publicLang, true
}

func sourceFrontmatterURLMatches(source hugosite.SourcePage, publicSlug string) bool {
	for _, key := range []string{"url", "permalink"} {
		value, ok := source.FrontmatterRaw[key].(string)
		if ok && strings.TrimSpace(value) != "" && site.NormalizeSlug(value) == site.NormalizeSlug(publicSlug) {
			return true
		}
	}
	return false
}

func sourcePageRevision(p hugosite.SourcePage) string {
	if strings.TrimSpace(p.FilePath) != "" {
		if raw, err := os.ReadFile(p.FilePath); err == nil {
			return revisionBytes(raw)
		}
	}
	return hashSourcePage(p)
}

func publicPageRevision(p site.Page) string {
	if p.RawHTML != "" {
		return revisionBytes([]byte(p.RawHTML))
	}
	return hashPublicPage(p)
}

func revisionBytes(raw []byte) string {
	digest := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func representationIdentity(sourceKey, lang, representation string) string {
	return normaliseSourceKey(sourceKey) + "\x00" + normaliseContentLang(lang) + "\x00" + representation
}

// reconcileContentRepresentations refreshes shadow rows from the actual
// in-memory indexes and prunes rows absent from the requested representation
// set. Readers continue to use the legacy pages/FTS tables.
func (d *DB) reconcileContentRepresentations(siteIdx *site.Index, srcIdx *hugosite.SourceIndex, includeSource, includePublic bool) error {
	want := map[string]bool{}
	defaultLang := ""
	if siteIdx != nil {
		defaultLang = siteIdx.SiteInfo()["lang"]
	}
	if includePublic && siteIdx != nil {
		for _, p := range siteIdx.Sitemap() {
			r := d.publicRepresentation(p)
			if sourceKey, lang, resolved := resolvePublicSource(p, srcIdx, defaultLang); resolved {
				r = publicRepresentationResolved(p, sourceKey, lang)
			}
			if err := d.syncContentRepresentation(r); err != nil {
				return err
			}
			want[representationIdentity(r.SourceKey, r.Lang, r.Representation)] = true
		}
	}
	if includeSource && srcIdx != nil {
		for _, p := range srcIdx.ListPages(0, 0) {
			r := sourceRepresentationResolved(p, defaultLang)
			if err := d.syncContentRepresentation(r); err != nil {
				return err
			}
			want[representationIdentity(r.SourceKey, r.Lang, r.Representation)] = true
		}
	}
	representations := []string{}
	if includeSource {
		representations = append(representations, "source")
	}
	if includePublic {
		representations = append(representations, "public")
	}
	for _, representation := range representations {
		rows, err := d.db.Query(`SELECT source_key,lang FROM content_representations_v1 WHERE representation=?`, representation)
		if err != nil {
			return err
		}
		var stale [][2]string
		for rows.Next() {
			var sourceKey, lang string
			if err := rows.Scan(&sourceKey, &lang); err != nil {
				rows.Close()
				return err
			}
			if !want[representationIdentity(sourceKey, lang, representation)] {
				stale = append(stale, [2]string{sourceKey, lang})
			}
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for _, key := range stale {
			if _, err := d.db.Exec(`DELETE FROM content_representations_v1 WHERE source_key=? AND lang=? AND representation=?`, key[0], key[1], representation); err != nil {
				return err
			}
		}
	}
	_, err := d.RefreshContentShadowStats()
	return err
}

// DeleteContentRepresentation deletes one language/representation without
// collapsing its siblings. It is used by translation-scoped source deletes.
func (d *DB) DeleteContentRepresentation(sourceKey, lang, representation string) error {
	_, err := d.db.Exec(`DELETE FROM content_representations_v1 WHERE source_key=? AND lang=? AND representation=?`, normaliseSourceKey(sourceKey), normaliseContentLang(lang), representation)
	return err
}

// DeleteBundleRepresentations removes one representation class for every
// language in a logical bundle. Source deletion must not erase public facts
// until the rendered tree is actually removed or rebuilt.
func (d *DB) DeleteBundleRepresentations(sourceKey, representation string) error {
	if representation != "source" && representation != "public" {
		return fmt.Errorf("content representation: invalid representation %q", representation)
	}
	_, err := d.db.Exec(`DELETE FROM content_representations_v1 WHERE source_key=? AND representation=?`, normaliseSourceKey(sourceKey), representation)
	return err
}

// RefreshContentShadowStats computes and persists aggregate-only comparison
// facts. Legacy readers remain authoritative until a later, explicitly gated
// cutover.
func (d *DB) RefreshContentShadowStats() (ContentShadowStats, error) {
	stats := ContentShadowStats{SchemaVersion: contentRepresentationSchemaVersion, LanguageCounts: map[string]int{}, ObservedAt: time.Now().UTC()}
	if err := d.db.QueryRow(`SELECT COUNT(*),COALESCE(SUM(representation='source'),0),COALESCE(SUM(representation='public'),0) FROM content_representations_v1`).Scan(&stats.TotalRows, &stats.SourceRows, &stats.PublicRows); err != nil {
		return ContentShadowStats{}, err
	}
	rows, err := d.db.Query(`SELECT lang,COUNT(*) FROM content_representations_v1 GROUP BY lang ORDER BY lang`)
	if err != nil {
		return ContentShadowStats{}, err
	}
	for rows.Next() {
		var lang string
		var count int
		if err := rows.Scan(&lang, &count); err != nil {
			rows.Close()
			return ContentShadowStats{}, err
		}
		stats.LanguageCounts[lang] = count
	}
	if err := rows.Close(); err != nil {
		return ContentShadowStats{}, err
	}
	if err := d.db.QueryRow(`SELECT COUNT(*) FROM content_representations_v1 r WHERE NOT EXISTS (
		SELECT 1 FROM content_representations_v1 c WHERE c.source_key=r.source_key AND c.representation<>r.representation
		AND c.lang=r.lang)`).Scan(&stats.MissingCounterparts); err != nil {
		return ContentShadowStats{}, err
	}
	if err := d.db.QueryRow(`SELECT COUNT(*) FROM pages p WHERE NOT EXISTS (
		SELECT 1 FROM content_representations_v1 r WHERE r.legacy_slug=p.slug
		AND r.representation=CASE WHEN p.published=1 THEN 'public' ELSE 'source' END
		AND (TRIM(p.lang)='' OR r.lang=LOWER(TRIM(p.lang))) AND r.legacy_revision=p.content_hash)`).Scan(&stats.LegacyMismatches); err != nil {
		return ContentShadowStats{}, err
	}
	var mismatchKeys []string
	rows, err = d.db.Query(`SELECT p.slug,p.lang,p.published FROM pages p WHERE NOT EXISTS (
		SELECT 1 FROM content_representations_v1 r WHERE r.legacy_slug=p.slug
		AND r.representation=CASE WHEN p.published=1 THEN 'public' ELSE 'source' END
		AND (TRIM(p.lang)='' OR r.lang=LOWER(TRIM(p.lang))) AND r.legacy_revision=p.content_hash) ORDER BY p.slug,p.lang`)
	if err != nil {
		return ContentShadowStats{}, err
	}
	for rows.Next() {
		var slug, lang string
		var published int
		if err := rows.Scan(&slug, &lang, &published); err != nil {
			rows.Close()
			return ContentShadowStats{}, err
		}
		mismatchKeys = append(mismatchKeys, fmt.Sprintf("%s|%s|%d", slug, lang, published))
	}
	if err := rows.Close(); err != nil {
		return ContentShadowStats{}, err
	}
	if len(mismatchKeys) > 0 {
		sort.Strings(mismatchKeys)
		digest := sha256.Sum256([]byte(strings.Join(mismatchKeys, "\n")))
		stats.MismatchDigest = hex.EncodeToString(digest[:8])
	}
	languages, err := json.Marshal(stats.LanguageCounts)
	if err != nil {
		return ContentShadowStats{}, err
	}
	_, err = d.db.Exec(`INSERT INTO content_shadow_runs_v1(total_rows,source_rows,public_rows,missing_counterparts,legacy_mismatches,mismatch_digest,language_counts_json,observed_at) VALUES(?,?,?,?,?,?,?,?)`,
		stats.TotalRows, stats.SourceRows, stats.PublicRows, stats.MissingCounterparts, stats.LegacyMismatches, stats.MismatchDigest, string(languages), stats.ObservedAt.Format(time.RFC3339Nano))
	return stats, err
}

func (d *DB) LatestContentShadowStats() (*ContentShadowStats, error) {
	var stats ContentShadowStats
	var languages, observed string
	err := d.db.QueryRow(`SELECT total_rows,source_rows,public_rows,missing_counterparts,legacy_mismatches,mismatch_digest,language_counts_json,observed_at FROM content_shadow_runs_v1 ORDER BY id DESC LIMIT 1`).Scan(
		&stats.TotalRows, &stats.SourceRows, &stats.PublicRows, &stats.MissingCounterparts, &stats.LegacyMismatches, &stats.MismatchDigest, &languages, &observed)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	stats.SchemaVersion = contentRepresentationSchemaVersion
	stats.LanguageCounts = map[string]int{}
	if err := json.Unmarshal([]byte(languages), &stats.LanguageCounts); err != nil {
		return nil, err
	}
	stats.ObservedAt, err = time.Parse(time.RFC3339Nano, observed)
	if err != nil {
		return nil, err
	}
	return &stats, nil
}

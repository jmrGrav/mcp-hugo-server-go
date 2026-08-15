package db

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
)

type BuildPageChange struct {
	SourceKey   string
	Lang        string
	Draft       bool
	TestContent bool
	Deleted     bool
}

type BuildRun struct {
	BuildID          string
	SourceRevision   string
	OutputRevision   string
	HugoVersion      string
	State            string
	SourceDriftCount int
	PublicDriftCount int
	ObservedAt       time.Time
	ReconciledAt     time.Time
}

type sourceBuildFact struct {
	key, lang, revision string
	draft, testContent  bool
}

type priorBuildFact struct {
	lastBuilt, publicRevision, state string
}

func buildIdentity(key, lang string) string {
	return normaliseSourceKey(key) + "\x00" + normaliseContentLang(lang)
}

func sourceBuildFacts(idx *site.Index, srcIdx *hugosite.SourceIndex) map[string]sourceBuildFact {
	out := map[string]sourceBuildFact{}
	if srcIdx == nil {
		return out
	}
	defaultLang := ""
	if idx != nil {
		defaultLang = normaliseContentLang(idx.SiteInfo()["lang"])
	}
	for _, page := range srcIdx.ListPages(0, 0) {
		lang := normaliseContentLang(page.Lang)
		if lang == "" {
			lang = defaultLang
		}
		fact := sourceBuildFact{
			key: normaliseSourceKey(page.Slug), lang: lang,
			revision: sourcePageRevision(page), draft: page.Draft,
			testContent: truthyBuildFrontmatter(page.FrontmatterRaw["test_content"]),
		}
		out[buildIdentity(fact.key, fact.lang)] = fact
	}
	return out
}

func truthyBuildFrontmatter(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(strings.TrimSpace(typed), "true")
	default:
		return false
	}
}

func (d *DB) latestCompletedBuildPages(tx queryer) (map[string]priorBuildFact, error) {
	rows, err := tx.Query(`SELECT p.source_key,p.lang,p.last_built_source_revision,p.public_revision,p.publication_state
		FROM build_pages p JOIN build_runs r ON r.build_id=p.build_id
		WHERE r.build_id=(SELECT build_id FROM build_runs
			WHERE state IN ('ok','partial_success')
			ORDER BY observed_at DESC,rowid DESC LIMIT 1)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]priorBuildFact{}
	for rows.Next() {
		var key, lang string
		var fact priorBuildFact
		if err := rows.Scan(&key, &lang, &fact.lastBuilt, &fact.publicRevision, &fact.state); err != nil {
			return nil, err
		}
		out[buildIdentity(key, lang)] = fact
	}
	return out, rows.Err()
}

type queryer interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

// BeginBuildRun snapshots the actual source bytes under the caller-held
// content lock and compares them with the most recent completed per-page build
// facts. It returns the deterministic changed/deleted set used by build_site.
func (d *DB) BeginBuildRun(buildID string, idx *site.Index, srcIdx *hugosite.SourceIndex, observedAt time.Time) ([]BuildPageChange, error) {
	if strings.TrimSpace(buildID) == "" {
		return nil, fmt.Errorf("build run: build_id is required")
	}
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}
	if err := d.reconcileContentRepresentations(idx, srcIdx, true, false); err != nil {
		return nil, err
	}
	current := sourceBuildFacts(idx, srcIdx)
	tx, err := d.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck
	previous, err := d.latestCompletedBuildPages(tx)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`INSERT INTO build_runs(build_id,state,observed_at) VALUES(?,?,?)`, buildID, "in_progress", observedAt.UTC().Format(time.RFC3339Nano)); err != nil {
		return nil, err
	}
	changes := make([]BuildPageChange, 0)
	identities := make([]string, 0, len(current))
	for identity := range current {
		identities = append(identities, identity)
	}
	sort.Strings(identities)
	for _, identity := range identities {
		fact := current[identity]
		prior := previous[identity]
		state := "unchanged"
		if fact.draft || fact.testContent {
			state = "intentionally_unpublished"
		}
		needsPublicationRepair := prior.state != "" && prior.state != "published" && prior.state != "intentionally_unpublished"
		if prior.lastBuilt != fact.revision || needsPublicationRepair {
			changes = append(changes, BuildPageChange{SourceKey: fact.key, Lang: fact.lang, Draft: fact.draft, TestContent: fact.testContent})
			if state == "unchanged" {
				state = "pending"
			}
		}
		if _, err := tx.Exec(`INSERT INTO build_pages(build_id,source_key,lang,source_revision,last_built_source_revision,public_revision,publication_state,observed_at) VALUES(?,?,?,?,?,?,?,?)`,
			buildID, fact.key, fact.lang, fact.revision, prior.lastBuilt, prior.publicRevision, state, observedAt.UTC().Format(time.RFC3339Nano)); err != nil {
			return nil, err
		}
	}
	for identity, prior := range previous {
		if _, found := current[identity]; found {
			continue
		}
		if prior.state == "deleted" {
			parts := strings.Split(identity, "\x00")
			if _, err := tx.Exec(`INSERT INTO build_pages(build_id,source_key,lang,last_built_source_revision,publication_state,observed_at) VALUES(?,?,?,?,?,?)`,
				buildID, parts[0], parts[1], prior.lastBuilt, "deleted", observedAt.UTC().Format(time.RFC3339Nano)); err != nil {
				return nil, err
			}
			continue
		}
		parts := strings.Split(identity, "\x00")
		changes = append(changes, BuildPageChange{SourceKey: parts[0], Lang: parts[1], Deleted: true})
		if _, err := tx.Exec(`INSERT INTO build_pages(build_id,source_key,lang,last_built_source_revision,public_revision,publication_state,observed_at) VALUES(?,?,?,?,?,?,?)`,
			buildID, parts[0], parts[1], prior.lastBuilt, prior.publicRevision, "deletion_pending", observedAt.UTC().Format(time.RFC3339Nano)); err != nil {
			return nil, err
		}
	}
	sort.Slice(changes, func(i, j int) bool {
		return buildIdentity(changes[i].SourceKey, changes[i].Lang) < buildIdentity(changes[j].SourceKey, changes[j].Lang)
	})
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return changes, nil
}

// CompleteBuildRun records the build fingerprints and advances per-page
// last-built facts only after the public output and indexes have reconciled.
func (d *DB) CompleteBuildRun(m PublicationManifest) error {
	if strings.TrimSpace(m.BuildID) == "" {
		return fmt.Errorf("build run: build_id is required")
	}
	if m.ObservedAt.IsZero() {
		m.ObservedAt = time.Now().UTC()
	}
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	result, err := tx.Exec(`UPDATE build_runs SET source_revision=?,output_revision=?,hugo_version=?,state=?,observed_at=? WHERE build_id=?`,
		m.SourceRevision, m.OutputRevision, m.HugoVersion, m.Status, m.ObservedAt.UTC().Format(time.RFC3339Nano), m.BuildID)
	if err != nil {
		return err
	}
	if changed, err := result.RowsAffected(); err != nil {
		return err
	} else if changed != 1 {
		return fmt.Errorf("build run: unknown build_id %q", m.BuildID)
	}
	rows, err := tx.Query(`SELECT source_key,lang,source_revision,last_built_source_revision,publication_state FROM build_pages WHERE build_id=?`, m.BuildID)
	if err != nil {
		return err
	}
	type update struct{ key, lang, sourceRevision, lastBuilt, state, publicRevision string }
	var updates []update
	for rows.Next() {
		var u update
		if err := rows.Scan(&u.key, &u.lang, &u.sourceRevision, &u.lastBuilt, &u.state); err != nil {
			rows.Close()
			return err
		}
		updates = append(updates, u)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, u := range updates {
		_ = tx.QueryRow(`SELECT revision FROM content_representations_v1 WHERE source_key=? AND lang=? AND representation='public'`, u.key, u.lang).Scan(&u.publicRevision)
		switch {
		case u.state == "deletion_pending" && u.publicRevision == "":
			u.state = "deleted"
		case u.state == "deletion_pending":
			u.state = "deletion_incomplete"
		case u.state == "deleted" && u.publicRevision == "":
		case u.state == "deleted":
			u.state = "deletion_incomplete"
		case u.state == "intentionally_unpublished":
		case u.publicRevision == "":
			u.state = "missing_public"
		default:
			u.state = "published"
		}
		nextLastBuilt := u.sourceRevision
		if u.state == "deleted" || u.state == "deletion_incomplete" {
			nextLastBuilt = u.lastBuilt
		}
		if _, err := tx.Exec(`UPDATE build_pages SET last_built_source_revision=?,public_revision=?,publication_state=?,observed_at=? WHERE build_id=? AND source_key=? AND lang=?`,
			nextLastBuilt, u.publicRevision, u.state, m.ObservedAt.UTC().Format(time.RFC3339Nano), m.BuildID, u.key, u.lang); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (d *DB) FailBuildRun(buildID, state string, observedAt time.Time) error {
	if strings.TrimSpace(buildID) == "" {
		return fmt.Errorf("build run: build_id is required")
	}
	if strings.TrimSpace(state) == "" {
		state = "failed"
	}
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}
	_, err := d.db.Exec(`UPDATE build_runs SET state=?,observed_at=? WHERE build_id=?`, state, observedAt.UTC().Format(time.RFC3339Nano), buildID)
	return err
}

// ReconcileLatestBuild recomputes drift from fresh source/public facts. It is
// deterministic and safe to run at startup and after every completed build.
func (d *DB) ReconcileLatestBuild(idx *site.Index, srcIdx *hugosite.SourceIndex) (*BuildRun, error) {
	if err := d.reconcileContentRepresentations(idx, srcIdx, true, true); err != nil {
		return nil, err
	}
	run, err := d.LatestBuildRun()
	if err != nil || run == nil {
		return run, err
	}
	current := sourceBuildFacts(idx, srcIdx)
	rows, err := d.db.Query(`SELECT source_key,lang,last_built_source_revision,public_revision,publication_state FROM build_pages WHERE build_id=?`, run.BuildID)
	if err != nil {
		return nil, err
	}
	type builtFact struct {
		key, lang, sourceRevision, publicRevision, state string
	}
	var built []builtFact
	for rows.Next() {
		var fact builtFact
		if err := rows.Scan(&fact.key, &fact.lang, &fact.sourceRevision, &fact.publicRevision, &fact.state); err != nil {
			rows.Close()
			return nil, err
		}
		built = append(built, fact)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	sourceDrift, publicDrift := 0, 0
	for _, builtFact := range built {
		identity := buildIdentity(builtFact.key, builtFact.lang)
		seen[identity] = true
		fact, found := current[identity]
		if (!found && builtFact.state != "deleted") || (found && fact.revision != builtFact.sourceRevision) {
			sourceDrift++
		}
		var currentPublic string
		err := d.db.QueryRow(`SELECT revision FROM content_representations_v1 WHERE source_key=? AND lang=? AND representation='public'`, builtFact.key, builtFact.lang).Scan(&currentPublic)
		if err != nil && err != sql.ErrNoRows {
			return nil, err
		}
		switch builtFact.state {
		case "missing_public", "deletion_incomplete":
			publicDrift++
		case "deleted", "intentionally_unpublished":
			if currentPublic != "" {
				publicDrift++
			}
		default:
			if currentPublic != builtFact.publicRevision {
				publicDrift++
			}
		}
	}
	for identity := range current {
		if !seen[identity] {
			sourceDrift++
		}
	}
	now := time.Now().UTC()
	if _, err := d.db.Exec(`UPDATE build_runs SET source_drift_count=?,public_drift_count=?,reconciled_at=? WHERE build_id=?`, sourceDrift, publicDrift, now.Format(time.RFC3339Nano), run.BuildID); err != nil {
		return nil, err
	}
	run.SourceDriftCount = sourceDrift
	run.PublicDriftCount = publicDrift
	run.ReconciledAt = now
	return run, nil
}

func (d *DB) LatestBuildRun() (*BuildRun, error) {
	var run BuildRun
	var observed, reconciled string
	err := d.db.QueryRow(`SELECT build_id,source_revision,output_revision,hugo_version,state,source_drift_count,public_drift_count,observed_at,reconciled_at FROM build_runs WHERE state IN ('ok','partial_success') ORDER BY observed_at DESC LIMIT 1`).Scan(
		&run.BuildID, &run.SourceRevision, &run.OutputRevision, &run.HugoVersion, &run.State, &run.SourceDriftCount, &run.PublicDriftCount, &observed, &reconciled)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	run.ObservedAt, err = time.Parse(time.RFC3339Nano, observed)
	if err != nil {
		return nil, err
	}
	if reconciled != "" {
		run.ReconciledAt, err = time.Parse(time.RFC3339Nano, reconciled)
		if err != nil {
			return nil, err
		}
	}
	return &run, nil
}

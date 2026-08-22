package admin

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/buildinfo"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/buildstatus"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/changeset"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/db"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/fileutil"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/gitutil"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugoruntime"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/toolcontract"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// probeTimeout bounds every host command (hugo version, git rev-parse, ...)
// this tool shells out to, so a hung or missing binary can't stall the call.
const probeTimeout = 5 * time.Second

// processStartedAt is intentionally process-scoped. Runtime status does not
// claim to recover a build history across restarts; LastBuildPersistence makes
// that boundary explicit to callers.
var processStartedAt = time.Now().UTC()

type getRuntimeStatusInput struct {
	// IncludeRevisions opts into hashing the full content_root/site_root trees
	// for source_revision/public_revision. Off by default: hashing a large
	// public/ output tree on every call would make this "compact status"
	// tool expensive to poll. build_site already emits output_revision once
	// per build; prefer that for the public tree when it's available.
	IncludeRevisions bool `json:"include_revisions,omitempty"`
	// ChangeSetID (#1142) selects which change-set data.publication_safety
	// is reported for, resolved exactly the way every mutation tool
	// resolves it (blank -> this caller's implicit default bucket, see
	// changeset.DefaultID). Ignored (no error) when the shared change-set
	// registry isn't wired at all, same as change_set_id on other tools
	// when the feature is absent; an explicit id that doesn't resolve for
	// this caller (unknown, or owned by someone else) is a normal
	// invalid_params error, matching build_site's own resolution.
	ChangeSetID string `json:"change_set_id,omitempty"`
}

type hugoRuntimeStatus struct {
	Available bool   `json:"available"`
	Version   string `json:"version,omitempty"`
	Extended  bool   `json:"extended"`
	Error     string `json:"error,omitempty"`
}

type gitRuntimeStatus struct {
	BaselineMode      string `json:"baseline_mode"`
	Available         bool   `json:"available"`
	Branch            string `json:"branch,omitempty"`
	HeadCommit        string `json:"head_commit,omitempty"`
	Dirty             bool   `json:"dirty"`
	ChangedFilesCount int    `json:"changed_files_count,omitempty"`
	// DirtyClasses (#864) is a coarse, safe classification of WHAT KIND of
	// resource changed — not WHICH file (paths are never exposed, #775) and
	// not WHO changed it (mcp-vs-external attribution was deliberately not
	// shipped, see the comment in probeGitBaseline). Each entry is one of the
	// stable class labels in the dirtyClass* constants. This answers the
	// audit's "which resource class caused the baseline delta?" without
	// leaking file contents or host paths. Sorted, de-duplicated; omitted
	// when the tree is clean.
	DirtyClasses []string `json:"dirty_classes,omitempty"`
	Error        string   `json:"error,omitempty"`
}

// Stable resource-class labels for gitRuntimeStatus.DirtyClasses (#864).
// These classify a changed path by KIND, derived from the path shape only;
// the path itself is never exposed. external_unknown is the safe default for
// anything not confidently recognized — it is deliberately not a guess at a
// more specific class.
const (
	dirtyClassContentSource   = "content_source"
	dirtyClassGeneratedAsset  = "generated_asset"
	dirtyClassPreviewResidue  = "preview_residue"
	dirtyClassExternalUnknown = "external_unknown"
)

type siteRuntimeStatus struct {
	ContentRootConfigured bool                    `json:"content_root_configured"`
	HugoRootConfigured    bool                    `json:"hugo_root_configured"`
	SourceRevision        string                  `json:"source_revision,omitempty"`
	PublicRevision        string                  `json:"public_revision,omitempty"`
	OverdueTestContent    []StaleTestContentEntry `json:"overdue_test_content,omitempty"`
}

// lastBuildRuntimeStatus reports the latest build fact. In a deployment with
// db_path it can be restored from the durable publication manifest after a
// restart; otherwise it is the most recent in-process build_site attempt
// (#467). Omitted entirely when neither source has a build to report.
type lastBuildRuntimeStatus struct {
	BuildID    string `json:"build_id,omitempty"`
	Status     string `json:"status"`
	ErrorClass string `json:"error_class,omitempty"`
	At         string `json:"at"`
}

type contentIndexShadowRuntimeStatus struct {
	SchemaVersion int `json:"schema_version"`
	TotalRows     int `json:"total_rows"`
	SourceRows    int `json:"source_rows"`
	PublicRows    int `json:"public_rows"`
	// MissingCounterparts (#1181) counts rows lacking a source<->public
	// counterpart for the same source_key+lang — a publication-state gap
	// (drafts, unpublished, test_content), NOT a missing-translation gap.
	// A bilingual site with a lot of disposable test_content can show a
	// large ratio here and still be a healthy, fully-paired bilingual
	// site; this field says nothing about language pairing on its own.
	MissingCounterparts int            `json:"missing_counterparts"`
	LegacyMismatches    int            `json:"legacy_mismatches"`
	MismatchDigest      string         `json:"mismatch_digest,omitempty"`
	LanguageCounts      map[string]int `json:"language_counts"`
	ObservedAt          string         `json:"observed_at"`
}

type buildReconciliationRuntimeStatus struct {
	BuildID          string `json:"build_id"`
	SourceDriftCount int    `json:"source_drift_count"`
	PublicDriftCount int    `json:"public_drift_count"`
	ReconciledAt     string `json:"reconciled_at,omitempty"`
	SourceOfTruth    string `json:"source_of_truth"`
}

// mutationJournalRuntimeStatus reports idempotent-replay journal retention
// facts only — it never describes unpublished/pending work. See
// ActiveEntries' own doc comment (#1165).
type mutationJournalRuntimeStatus struct {
	// ActiveEntries is the count of mutation results still retained for
	// idempotent replay (RememberMutation), not the count of changes still
	// pending publication — those are unrelated axes. An entry stays here
	// until its retention window prunes it, regardless of whether the
	// underlying page was later published, edited again, or deleted; a
	// freshly-deployed, fully-published site can report a large nonzero
	// ActiveEntries. For "how much unpublished work exists," read
	// publication_safety/unpublished_changes_count instead (#1142).
	ActiveEntries     int    `json:"active_entries"`
	LastPrunedAt      string `json:"last_pruned_at,omitempty"`
	LastPrunedEntries int    `json:"last_pruned_entries"`
}

// currentChangeSetRuntimeStatus is the change-set publicationSafetyRuntimeStatus
// resolved for this call — see computePublicationSafety's own doc comment
// for how it's chosen.
type currentChangeSetRuntimeStatus struct {
	ID      string `json:"id"`
	Changes int    `json:"changes"`
	// DeclaredUntrustedDerivation (#1226) surfaces this change-set's
	// self-reported untrusted-derivation state, set optionally at
	// create_change_set time. Unverified by this server — an audit signal
	// only, never a basis for SafeToPublish below. See
	// docs/mcp-contract.md §6.27. Deliberately a bool only: the paired
	// free-text declared_untrusted_note is caller-supplied text this
	// package carries no content_provenance tagging for (unlike
	// internal/tools/read/internal/tools/anonymous) — echoing it back
	// here would open an untagged channel for arbitrary
	// attacker-influenceable text into an admin-scope response that looks
	// like server metadata. The note is still recorded (create_change_set
	// echoes it to the same caller that supplied it, and it's queryable
	// directly from SQLite's change_sets table for an operator who needs
	// it) — it is just not replayed through this surface.
	DeclaredUntrustedDerivation bool `json:"declared_untrusted_derivation,omitempty"`
}

type otherChangeSetsRuntimeStatus struct {
	Count   int `json:"count"`
	Changes int `json:"changes"`
}

// publicationSafetyRuntimeStatus is #1142: it answers "can I publish right
// now without risking someone else's in-flight work?" for one specific
// change-set (CurrentChangeSet — resolved exactly the way build_site's own
// `change_set_id` input resolves it, see getRuntimeStatusInput.ChangeSetID),
// by attributing every currently-pending page (via
// changeset.Registry.OwnerOfSourceKey, the same lookup #1140's build/publish
// guard uses) to whichever change-set most recently touched it. Deliberately
// separate from the existing top-level `publication_state` (a coarse
// pending/clean/drift enum unrelated to change-set ownership) to avoid a
// JSON key collision and because that field predates change-sets entirely.
type publicationSafetyRuntimeStatus struct {
	// UnpublishedChangesCount is CurrentChangeSet.Changes +
	// OtherChangeSets.Changes + ExternalUnknownChanges — every pending page
	// this view knows about, regardless of owner. Previously could disagree
	// with the top-level data.unpublished_changes_count/data.source_ahead_reason
	// (which also folds in resolver-based out-of-band-drift reconciliation)
	// for exactly the reason ExternalUnknownChanges' own fix below closes;
	// the two now describe the same underlying drift, computed two
	// different ways, and should not diverge in the way live production
	// once showed (safe_to_publish:true alongside
	// source_ahead_reason:out_of_band_source_drift).
	UnpublishedChangesCount int                           `json:"unpublished_changes_count"`
	ActiveChangeSets        int                           `json:"active_change_sets"`
	CurrentChangeSet        currentChangeSetRuntimeStatus `json:"current_change_set"`
	OtherChangeSets         otherChangeSetsRuntimeStatus  `json:"other_change_sets"`
	// ExternalUnknownChanges counts pending pages no change-set this
	// process has tracked a mutation for — direct filesystem/SSH edits, or
	// edits made before this process last restarted (see
	// changeset.Registry.OwnerOfSourceKey's own doc comment on this blind
	// spot), PLUS any out-of-band source drift the resolver-based
	// reconciliation detects that no change-set can attribute (a fix: this
	// field's own name and doc always claimed to cover direct filesystem/SSH
	// edits, but before this fix it only ever consulted
	// srcIdx.PendingPages() — the in-memory BuildPending-flag set this
	// process's own write tools populate — so a page that drifted via a
	// direct external edit, never touched by this process's own writes,
	// never carried that flag and was structurally invisible here, even
	// when data.source_ahead_reason on the very same response already
	// reported out_of_band_source_drift). Unlike guardForeignChangeSet,
	// which allows these through (it cannot tell "untracked" apart from
	// "genuinely nobody else's"), SafeToPublish here treats them as unsafe:
	// this field exists purely to inform an agent, and "an untracked change
	// might not be mine" is exactly the risk #1142 asks this surface to
	// name.
	ExternalUnknownChanges int  `json:"external_unknown_changes"`
	SafeToPublish          bool `json:"safe_to_publish"`
}

type runtimeStatusData struct {
	// ReleaseVersion — see the comment on toolcontract.ResponseMeta.ReleaseVersion.
	// Named ServerVersion/server_version through v1.5.7; renamed (#563).
	ReleaseVersion          string                            `json:"release_version"`
	SchemaVersion           string                            `json:"schema_version"`
	Commit                  string                            `json:"commit,omitempty"`
	CommitTime              string                            `json:"commit_time,omitempty"`
	BuildChannel            string                            `json:"build_channel,omitempty"`
	BuildDirty              bool                              `json:"build_dirty"`
	BinaryBuildDirty        bool                              `json:"binary_build_dirty"`
	SiteWorktreeDirty       bool                              `json:"site_worktree_dirty"`
	SourceAheadOfPublic     bool                              `json:"source_ahead_of_public"`
	UnpublishedChangesCount int                               `json:"unpublished_changes_count"`
	SourceAheadReason       string                            `json:"source_ahead_reason"`
	PublicationState        string                            `json:"publication_state"`
	ProcessStartedAt        string                            `json:"process_started_at"`
	LastBuildPersistence    string                            `json:"last_build_persistence"`
	Hugo                    hugoRuntimeStatus                 `json:"hugo"`
	Git                     gitRuntimeStatus                  `json:"git"`
	Site                    siteRuntimeStatus                 `json:"site"`
	LastBuild               *lastBuildRuntimeStatus           `json:"last_build,omitempty"`
	ContentIndexShadow      *contentIndexShadowRuntimeStatus  `json:"content_index_shadow,omitempty"`
	BuildReconciliation     *buildReconciliationRuntimeStatus `json:"build_reconciliation,omitempty"`
	MutationJournal         *mutationJournalRuntimeStatus     `json:"mutation_journal,omitempty"`
	PublicationSafety       *publicationSafetyRuntimeStatus   `json:"publication_safety,omitempty"`
	Degraded                []string                          `json:"degraded,omitempty"`
}

type getRuntimeStatusOutput struct {
	toolcontract.ToolResponse[runtimeStatusData]
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// RegisterRuntimeStatus wires get_runtime_status (site.admin scope).
func RegisterRuntimeStatus(s *mcp.Server, cfg config.Config, srcIdx *hugosite.SourceIndex, publicIndexes ...*site.Index) {
	registerRuntimeStatus(s, cfg, srcIdx, nil, nil, publicIndexes...)
}

// RegisterRuntimeStatusWithChangeSets additionally wires the shared
// changeset.Registry so `data.publication_safety` (#1142) can be reported;
// used by admin.Register, which already threads the same registry into
// build_site/publish_changes for #1140's guard.
func RegisterRuntimeStatusWithChangeSets(s *mcp.Server, cfg config.Config, srcIdx *hugosite.SourceIndex, changeSets *changeset.Registry, publicIndexes ...*site.Index) {
	registerRuntimeStatus(s, cfg, srcIdx, nil, changeSets, publicIndexes...)
}

// RegisterRuntimeStatusWithDB is the production registration path when the
// optional derived SQLite database is configured. It retains the public
// RegisterRuntimeStatus signature for focused tool tests while allowing a
// restarted process to report the most recently persisted build fact.
func RegisterRuntimeStatusWithDB(s *mcp.Server, cfg config.Config, srcIdx *hugosite.SourceIndex, siteDB *db.DB, changeSets *changeset.Registry, publicIndexes ...*site.Index) {
	registerRuntimeStatus(s, cfg, srcIdx, siteDB, changeSets, publicIndexes...)
}

func registerRuntimeStatus(s *mcp.Server, cfg config.Config, srcIdx *hugosite.SourceIndex, siteDB *db.DB, changeSets *changeset.Registry, publicIndexes ...*site.Index) {
	if s == nil {
		return
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:  "get_runtime_status",
		Title: "Get runtime status",
		Description: "Report the actual runtime/build/git/site status of this server in one compact structured surface: " +
			"server version and build commit, whether the hugo and git binaries are usable, the outcome of the most " +
			"recent build_site attempt (`last_build`: persisted across restart when `db_path` is configured, otherwise process-memory only), and " +
			"(opt-in via include_revisions, since hashing the full content/public trees is not cheap) source/public " +
			"revision hashes. When disposable `test_content` pages are overdue, `data.site.overdue_test_content[]` " +
			"surfaces a deterministic machine-readable list (`slug`, `owner?`, `expires_at`, `overdue_seconds`, `reason`) " +
			"so cleanup does not depend on remembering to run a build first. When the git baseline is dirty, `data.git.dirty_classes` " +
			"(#864) classifies WHAT KIND of resource changed — a safe, coarse set drawn from `content_source`, `generated_asset`, " +
			"`preview_residue`, `external_unknown` — so an operator can tell expected residue apart from unexpected drift without the " +
			"tool ever exposing file paths or contents (it deliberately does not attribute changes to mcp-vs-external, and `external_unknown` " +
			"is the honest default for anything not confidently recognized). `source_ahead_of_public` and " +
			"`unpublished_changes_count` report server-known source changes awaiting publication. `source_ahead_reason` distinguishes " +
			"`pending_mcp_changes`, `out_of_band_source_drift`, `generated_asset_drift`, and `none`; `publication_state` " +
			"is `pending`, `source_drift_only`, `generated_asset_drift`, or `clean` so Git worktree dirtiness is not confused with incomplete public output. `process_started_at` " +
			"and `last_build_persistence` make restart behavior explicit. When SQLite shadow migration is active, `content_index_shadow` reports aggregate-only " +
			"language/representation counts, counterpart gaps, and legacy mismatch facts. `content_index_shadow.missing_counterparts` (#1181) counts rows lacking a source<->public counterpart for the same source_key+lang — a publication-state gap (drafts, unpublished, test_content) — NOT a missing-translation/bilingual-pairing gap; a bilingual site with disposable test_content can show a large value here and still be fully paired across languages. `build_reconciliation` reports aggregate source/public drift recomputed from filesystem fingerprints rather than volatile BuildPending flags. No page identity or body is exposed. When SQLite is configured, " +
			"`mutation_journal` reports only aggregate retention facts; `last_pruned_entries` is the number removed by the most recent successful maintenance " +
			"transaction. IMPORTANT (#1165): `mutation_journal.active_entries` counts results retained for idempotent replay, NOT changes still pending " +
			"publication — a fully-published site can report a large nonzero `active_entries`. For unpublished work, read `publication_safety`/`unpublished_changes_count` " +
			"instead. When the shared change-set registry is wired (#1135/#1140), `publication_safety` (#1142) previews whether a build_site/publish_changes call " +
			"with the same optional `change_set_id` would trip the foreign_change_set_present guard: `safe_to_publish` is false if `other_change_sets` (a different " +
			"change-set's pending work) or `external_unknown_changes` (pending pages no change-set this process has tracked — common right after a restart) is " +
			"nonzero — note `external_unknown_changes` alone does NOT actually block build_site itself; confirm those changes are expected, then build (see " +
			"docs/mcp-contract.md §6.18 for the full field breakdown and remedy). `external_unknown_changes` also now folds in pre-existing out-of-band source drift " +
			"detected via resolver reconciliation, not just pages this process's own write tools flagged — the same drift class `source_ahead_reason:out_of_band_source_drift` " +
			"reports at the top level of this response; the two are no longer able to disagree the way they once could. Read-only; resolving `change_set_id` here never updates its last-used bookkeeping. " +
			"Does not expose secrets or arbitrary host inventory. Use this instead of inferring environment health from error messages on other tools.",
		InputSchema:  tools.MustSchema[getRuntimeStatusInput](),
		OutputSchema: tools.MustSchema[getRuntimeStatusOutput](),
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    true,
			DestructiveHint: fileutil.BoolPtr(false),
			IdempotentHint:  true,
			OpenWorldHint:   fileutil.BoolPtr(true),
		},
	}, toolcontract.WrapTool(func(ctx context.Context, _ *mcp.CallToolRequest, in getRuntimeStatusInput) (*mcp.CallToolResult, getRuntimeStatusOutput, error) {
		data := runtimeStatusData{
			ReleaseVersion:       buildinfo.Version,
			SchemaVersion:        buildinfo.SchemaVersion,
			Commit:               buildinfo.Commit,
			CommitTime:           buildinfo.CommitTime,
			BuildChannel:         buildinfo.EffectiveBuildChannel(),
			BuildDirty:           buildinfo.Dirty,
			BinaryBuildDirty:     buildinfo.Dirty,
			ProcessStartedAt:     processStartedAt.Format(time.RFC3339),
			LastBuildPersistence: "process_memory",
			Site: siteRuntimeStatus{
				ContentRootConfigured: strings.TrimSpace(cfg.ContentRoot) != "",
				HugoRootConfigured:    strings.TrimSpace(cfg.HugoRoot) != "",
			},
		}

		data.Hugo = probeHugo(ctx, cfg)
		data.Git = probeGitBaseline(ctx, cfg)
		data.SiteWorktreeDirty = data.Git.Dirty
		pendingPages := 0
		if srcIdx != nil {
			hugosite.ContentMu.RLock()
			pendingPages = srcIdx.PendingCount()
			hugosite.ContentMu.RUnlock()
		}
		// BuildPending is useful for this process's own writes, but it is
		// intentionally volatile. When the public index is available, reconcile
		// every source page against its resolved output too: that detects an
		// out-of-band source change after restart and clears it after a full
		// successful Hugo build (#1066).
		externalPending := 0
		// unattributedExternalPending feeds computePublicationSafety's
		// out-of-band-drift detection (a fix — see the issue tracking this):
		// srcIdx.PendingPages() (computePublicationSafety's own "external"
		// count, BuildPending-flagged pages with no change-set owner) only
		// carries pages this *process* flagged via its own
		// create_page/update_page/delete_page writes, so a page that
		// drifted via a direct filesystem/git edit — this process's
		// BuildPending flag never set for it at all — was structurally
		// invisible to that check, even when data.SourceAheadReason on the
		// very same response already reported out_of_band_source_drift.
		//
		// Deliberately restricted to !source.BuildPending, disjoint from
		// computePublicationSafety's own "external" count (which already
		// covers the BuildPending-flagged-and-unowned case) — double
		// counting the same page in both would be one failure mode, but the
		// bigger one this guards against is a resolved-pending state that
		// is NOT genuine drift at all: a page this process just created
		// (registry-attributed) whose resolved public output simply hasn't
		// caught up yet (e.g. immediately after a build_site call whose
		// underlying Hugo run produced no real output — a real possibility
		// in tests, not just hypothetical). Registry attribution survives a
		// build (only the in-memory BuildPending flag clears, mutation
		// history does not), so requiring "no owner at all" here — on top
		// of "this process's own BuildPending flag was never even set" —
		// is what tells "MCP already knows whose work this was, it just
		// hasn't rebuilt yet" apart from "nobody tracked in the registry
		// ever touched this page", which is the actual signal #1142's
		// safety preview needs.
		unattributedExternalPending := 0
		if len(publicIndexes) > 0 && publicIndexes[0] != nil && srcIdx != nil {
			resolver := site.NewPageResolver(publicIndexes[0], srcIdx, cfg)
			for _, source := range srcIdx.ListPages(0, 0) {
				if source.Draft {
					continue
				}
				resolved, ok := resolver.ResolveWithLang(source.Slug, source.Lang)
				if !ok || site.StateForResolvedPage(resolved, cfg.SiteRoot).BuildState == "pending" {
					externalPending++
					if changeSets != nil && !source.BuildPending {
						if _, owned := changeSets.OwnerOfSourceKey(source.Slug); !owned {
							unattributedExternalPending++
						}
					}
				}
			}
		}
		data.UnpublishedChangesCount = max(pendingPages, externalPending)
		fallbackGitDrift := len(publicIndexes) == 0 && data.Git.Dirty && containsString(data.Git.DirtyClasses, dirtyClassContentSource)
		data.SourceAheadOfPublic = data.UnpublishedChangesCount > 0 || fallbackGitDrift
		switch {
		case pendingPages > 0:
			data.SourceAheadReason, data.PublicationState = "pending_mcp_changes", "pending"
		case externalPending > 0 || fallbackGitDrift:
			data.SourceAheadReason, data.PublicationState = "out_of_band_source_drift", "source_drift_only"
		case data.Git.Dirty && containsString(data.Git.DirtyClasses, dirtyClassGeneratedAsset):
			data.SourceAheadReason, data.PublicationState = "generated_asset_drift", "generated_asset_drift"
		default:
			data.SourceAheadReason, data.PublicationState = "none", "clean"
		}

		if !data.Hugo.Available {
			data.Degraded = append(data.Degraded, "build_site/preview_build: hugo binary is unavailable — "+data.Hugo.Error)
		}
		if !data.Git.Available {
			data.Degraded = append(data.Degraded, "diff_page: git-backed source diffs are unavailable — "+data.Git.Error)
		}

		snap := buildstatus.Last()
		if snap.Attempted {
			data.LastBuild = &lastBuildRuntimeStatus{
				Status:     snap.Status,
				ErrorClass: snap.ErrorClass,
				At:         snap.At.UTC().Format(time.RFC3339),
			}
			if snap.Status == "failed" {
				data.Degraded = append(data.Degraded, "build_site: last attempt failed ("+snap.ErrorClass+") at "+data.LastBuild.At)
			}
		}
		// The manifest is deliberately only a durable observation. The
		// source/public revision checks below still read the filesystem; an
		// out-of-band edit can make a persisted build fact stale. Persistence
		// is reported independently of whether this process itself already
		// holds a fresher in-memory snapshot (#1096 runtime shadow
		// investigation): a build attempted in this process is just as
		// durable as one recovered after restart when db_path is configured,
		// so LastBuildPersistence must not default to "process_memory" just
		// because the process has since run its own build.
		if siteDB != nil {
			manifest, err := siteDB.LatestPublicationManifest()
			if err != nil {
				data.Degraded = append(data.Degraded, "publication manifest unavailable: "+err.Error())
			} else if manifest != nil && (!snap.Attempted || !manifest.ObservedAt.Before(snap.At)) {
				data.LastBuildPersistence = "sqlite_manifest"
				if !snap.Attempted {
					data.LastBuild = &lastBuildRuntimeStatus{
						BuildID: manifest.BuildID,
						Status:  manifest.Status,
						At:      manifest.ObservedAt.UTC().Format(time.RFC3339),
					}
				}
			}
		}
		if siteDB != nil {
			buildRun, err := siteDB.LatestBuildRun()
			if err != nil {
				data.Degraded = append(data.Degraded, "build reconciliation unavailable: "+err.Error())
			} else if buildRun != nil {
				data.BuildReconciliation = &buildReconciliationRuntimeStatus{
					BuildID: buildRun.BuildID, SourceDriftCount: buildRun.SourceDriftCount,
					PublicDriftCount: buildRun.PublicDriftCount,
					SourceOfTruth:    "filesystem_fingerprints",
				}
				if !buildRun.ReconciledAt.IsZero() {
					data.BuildReconciliation.ReconciledAt = buildRun.ReconciledAt.UTC().Format(time.RFC3339)
				}
			}
			shadow, err := siteDB.LatestContentShadowStats()
			if err != nil {
				data.Degraded = append(data.Degraded, "content index shadow diagnostics unavailable: "+err.Error())
			} else if shadow != nil {
				data.ContentIndexShadow = &contentIndexShadowRuntimeStatus{
					SchemaVersion: shadow.SchemaVersion, TotalRows: shadow.TotalRows,
					SourceRows: shadow.SourceRows, PublicRows: shadow.PublicRows,
					MissingCounterparts: shadow.MissingCounterparts, LegacyMismatches: shadow.LegacyMismatches,
					MismatchDigest: shadow.MismatchDigest, LanguageCounts: shadow.LanguageCounts,
					ObservedAt: shadow.ObservedAt.UTC().Format(time.RFC3339),
				}
			}
			stats, err := siteDB.MutationJournalStats()
			if err != nil {
				data.Degraded = append(data.Degraded, "mutation journal unavailable: "+err.Error())
			} else {
				data.MutationJournal = &mutationJournalRuntimeStatus{
					ActiveEntries:     stats.ActiveEntries,
					LastPrunedEntries: stats.LastPrunedEntries,
				}
				if !stats.LastPrunedAt.IsZero() {
					data.MutationJournal.LastPrunedAt = stats.LastPrunedAt.UTC().Format(time.RFC3339)
				}
			}
		}

		if in.IncludeRevisions {
			if strings.TrimSpace(cfg.ContentRoot) != "" {
				if rev, err := hashTree(cfg.ContentRoot); err == nil {
					data.Site.SourceRevision = rev
				}
			}
			if strings.TrimSpace(cfg.SiteRoot) != "" {
				if rev, err := hashTree(cfg.SiteRoot); err == nil {
					data.Site.PublicRevision = rev
				}
			}
		}
		data.Site.OverdueTestContent = CollectStaleTestContent(srcIdx, cfg.StaleTestContentThresholdHours, time.Now())

		if changeSets != nil && srcIdx != nil {
			safety, err := computePublicationSafety(ctx, changeSets, srcIdx, in.ChangeSetID, unattributedExternalPending)
			if err != nil {
				return nil, getRuntimeStatusOutput{}, err
			}
			data.PublicationSafety = safety
		}

		meta := toolcontract.NewMeta(buildinfo.Version, time.Now())
		return nil, getRuntimeStatusOutput{ToolResponse: toolcontract.Success(data, meta)}, nil
	}))
}

// computePublicationSafety is #1142: attribute every currently-pending page
// to whichever change-set most recently touched it (the same
// changeset.Registry.OwnerOfSourceKey lookup #1140's build/publish guard
// uses), then report whether a build_site/publish_changes call made right
// now with this same requestedChangeSetID would be safe — i.e. would NOT
// hit #1140's foreign_change_set_present guard — without an agent having to
// attempt the call and parse the error.
//
// requestedChangeSetID is resolved via changeSets.Resolve exactly the way
// build_site's own change_set_id input resolves it: blank becomes this
// caller's implicit default bucket, and any other value must already be
// owned by this caller or Resolve returns invalid_params. This is
// deliberately the same resolution rule guardForeignChangeSet's own
// acknowledgment set uses, so "am I safe to build with change_set_id=X"
// here and "would build_site with change_set_id=X succeed" give the same
// answer for the single-id case (the change_set_ids plural escape hatch is
// intentionally not modeled here — this is one specific change-set's view).
//
// externalOutOfBandPending is the caller's own unattributedExternalPending
// count (computed once, just above this call, from a resolver walk
// comparing every source page against its resolved public output) — a fix
// for a defect this field was blind to before (see the issue tracking
// this): srcIdx.PendingPages() only carries pages this *process* flagged
// via its own create_page/update_page/delete_page writes, so a page that
// drifted via a direct filesystem/git edit never gets a BuildPending flag
// and was structurally invisible here, even when data.SourceAheadReason on
// the same response already reported out_of_band_source_drift. The caller
// deliberately restricts this count to pages with BuildPending==false and
// no change-set owner at all, so it is disjoint from this function's own
// "external" count below (BuildPending-flagged-and-unowned) — safe to add
// directly, no further attribution subtraction needed here.
func computePublicationSafety(ctx context.Context, changeSets *changeset.Registry, srcIdx *hugosite.SourceIndex, requestedChangeSetID string, externalOutOfBandPending int) (*publicationSafetyRuntimeStatus, error) {
	// Peek, not Resolve: get_runtime_status carries ReadOnlyHint:true and
	// must not mutate change-set LastUsedAt bookkeeping merely by being
	// asked about it.
	current, err := changeSets.Peek(ctx, requestedChangeSetID)
	if err != nil {
		return nil, err
	}

	hugosite.ContentMu.RLock()
	pending := srcIdx.PendingPages()
	hugosite.ContentMu.RUnlock()

	changesByOwner := make(map[string]int)
	seen := make(map[string]bool)
	external := 0
	for _, p := range pending {
		if seen[p.Slug] {
			continue
		}
		seen[p.Slug] = true
		ownerID, ok := changeSets.OwnerOfSourceKey(p.Slug)
		if !ok {
			external++
			continue
		}
		changesByOwner[ownerID]++
	}

	declared, _ := changeSets.DeclaredUntrustedDerivation(current)
	result := &publicationSafetyRuntimeStatus{
		CurrentChangeSet: currentChangeSetRuntimeStatus{
			ID:                          current,
			Changes:                     changesByOwner[current],
			DeclaredUntrustedDerivation: declared,
		},
	}
	ownerIDs := make([]string, 0, len(changesByOwner))
	for id := range changesByOwner {
		ownerIDs = append(ownerIDs, id)
	}
	sort.Strings(ownerIDs)
	result.ActiveChangeSets = len(ownerIDs)

	for _, id := range ownerIDs {
		if id == current {
			continue
		}
		result.OtherChangeSets.Count++
		result.OtherChangeSets.Changes += changesByOwner[id]
	}
	result.ExternalUnknownChanges = external + externalOutOfBandPending
	result.UnpublishedChangesCount = result.CurrentChangeSet.Changes + result.OtherChangeSets.Changes + result.ExternalUnknownChanges
	result.SafeToPublish = result.OtherChangeSets.Changes == 0 && result.ExternalUnknownChanges == 0
	return result, nil
}

// HugoVersionString reports the resolved Hugo binary's semantic version
// (e.g. "v0.150.0"), or "" if it could not be determined (hugo not on
// PATH, timed out, unparseable output). Exported for #1151's template
// fingerprint, which needs the same "did the binary itself change"
// signal probeHugo already computes for get_runtime_status, without
// duplicating the probe/parse logic.
func HugoVersionString(ctx context.Context, cfg config.Config) string {
	return hugoruntime.VersionString(ctx, cfg)
}

// probeHugo shells out to `hugo version` with a bounded environment and
// timeout, and parses the semantic version and extended-build flag out of
// output like "hugo v0.150.0+extended linux/amd64 BuildDate=...".
func probeHugo(ctx context.Context, cfg config.Config) hugoRuntimeStatus {
	status := hugoruntime.Probe(ctx, cfg)
	return hugoRuntimeStatus{Available: status.Available, Version: status.Version, Extended: status.Extended, Error: status.Error}
}

// probeGitBaseline resolves the runtime Git baseline honoring
// git_baseline.mode (see docs/git-baseline-model.md). It intentionally never
// returns an absolute host path: only branch/commit/dirty are exposed.
func probeGitBaseline(ctx context.Context, cfg config.Config) gitRuntimeStatus {
	status := gitRuntimeStatus{BaselineMode: cfg.GitBaseline.Mode}
	if status.BaselineMode == "" {
		status.BaselineMode = "auto"
	}
	if status.BaselineMode == "disabled" {
		status.Error = "git baseline is disabled by configuration"
		return status
	}

	root := strings.TrimSpace(cfg.GitBaseline.RepoPath)
	if status.BaselineMode != "configured" || root == "" {
		root = strings.TrimSpace(cfg.ContentRoot)
	}
	if root == "" {
		status.Error = "no content root or git_baseline.repo_path configured"
		return status
	}

	// Discovered via a pure filesystem walk (gitutil.DiscoverRoot), not by
	// invoking git: git's own root-discovery command is itself blocked by
	// the dubious-ownership check this indirection routes around (a
	// content checkout owned by an interactive user, read by a dedicated
	// service account, is exactly the case that check exists to flag).
	gitRoot, err := gitutil.DiscoverRoot(root)
	if err != nil {
		status.Error = sanitiseGitError(err, cfg, root, gitRoot)
		return status
	}

	tctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	branch, err := gitStatusOutput(tctx, gitRoot, "rev-parse", "--abbrev-ref", "HEAD")
	if err == nil {
		status.Branch = branch
	}
	head, err := gitStatusOutput(tctx, gitRoot, "rev-parse", "--short", "HEAD")
	if err != nil {
		status.Error = sanitiseGitError(err, cfg, root, gitRoot)
		return status
	}
	status.HeadCommit = head

	porcelain, err := gitStatusOutput(tctx, gitRoot, "status", "--porcelain")
	if err == nil {
		porcelainTrimmed := strings.TrimSpace(porcelain)
		status.Dirty = porcelainTrimmed != ""
		// Count non-empty lines in porcelain output; each line represents one changed file.
		// changed_files_count is a safe, reliable aggregate that never exposes paths or content.
		// A dirty_reason (mcp-vs-external) classifier was considered per #775, but the closest
		// existing signal — index_staleness.likely_source's mcp_pending_build/external_or_unknown
		// (#583/#617) — documents itself as a coarse, best-effort hint, not per-caller
		// attribution. Reusing that same best-effort standard here risked exactly the "looks
		// precise but isn't trustworthy" outcome #775 warns against, so dirty_reason was
		// deliberately deferred rather than shipped on a shakier guarantee.
		if status.Dirty {
			lines := strings.Split(porcelainTrimmed, "\n")
			status.ChangedFilesCount = len(lines)
			// #864: classify the changed paths by resource class and expose
			// ONLY the class labels — the paths themselves are parsed here but
			// never leave this function, preserving #775's no-path guarantee.
			status.DirtyClasses = classifyDirtyPorcelain(lines)
		}
	}

	status.Available = true
	return status
}

// classifyDirtyPorcelain turns `git status --porcelain` lines into the sorted,
// de-duplicated set of resource-class labels present (#864). It parses each
// path only to classify it by shape and never returns or logs the path. The
// porcelain line format is "XY <path>" (and "XY <old> -> <new>" for renames);
// for renames the destination path is what matters for classification.
func classifyDirtyPorcelain(lines []string) []string {
	seen := map[string]bool{}
	for _, line := range lines {
		if len(line) < 4 {
			continue
		}
		// Drop the 2-char status code and the following space.
		p := strings.TrimSpace(line[3:])
		if idx := strings.LastIndex(p, " -> "); idx >= 0 {
			p = p[idx+len(" -> "):]
		}
		p = strings.Trim(p, `"`)
		if p == "" {
			continue
		}
		seen[classifyDirtyPath(p)] = true
	}
	out := make([]string, 0, len(seen))
	for c := range seen {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// classifyDirtyPath maps one repo-relative path to a resource class by shape
// only. Order matters: a generated hero image lives under static/images and
// would otherwise be swept up by broader rules, so it is checked first.
func classifyDirtyPath(p string) string {
	slash := strings.ReplaceAll(p, "\\", "/")
	base := slash
	if i := strings.LastIndex(slash, "/"); i >= 0 {
		base = slash[i+1:]
	}
	switch {
	case strings.Contains(slash, "static/images/") && strings.HasSuffix(base, HeroImageSuffix):
		return dirtyClassGeneratedAsset
	case strings.Contains(slash, "mcp-preview-") || strings.Contains(slash, "/preview/"):
		return dirtyClassPreviewResidue
	case strings.HasPrefix(slash, "content/") || strings.Contains(slash, "/content/") || strings.HasSuffix(base, ".md"):
		return dirtyClassContentSource
	default:
		return dirtyClassExternalUnknown
	}
}

// sanitiseGitError redacts every absolute host path this probe might have
// touched (hugo_root, site_root, the resolved baseline root, and the
// discovered git toplevel) before an error reaches the response, so a git
// error message echoing its working directory can't leak host filesystem
// layout the way sanitiseStderr alone (hugo_root/site_root only) would miss.
func sanitiseGitError(err error, cfg config.Config, roots ...string) string {
	msg := err.Error()
	for _, root := range roots {
		if root != "" {
			msg = strings.ReplaceAll(msg, root, "<git_root>")
		}
	}
	return sanitiseStderr([]byte(msg), cfg.HugoRoot, cfg.SiteRoot)
}

func gitStatusOutput(ctx context.Context, dir string, args ...string) (string, error) {
	return gitutil.Output(ctx, dir, args...)
}

package admin

import (
	"context"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/buildinfo"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/buildstatus"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/db"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/fileutil"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/gitutil"
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
	SchemaVersion       int            `json:"schema_version"`
	TotalRows           int            `json:"total_rows"`
	SourceRows          int            `json:"source_rows"`
	PublicRows          int            `json:"public_rows"`
	MissingCounterparts int            `json:"missing_counterparts"`
	LegacyMismatches    int            `json:"legacy_mismatches"`
	MismatchDigest      string         `json:"mismatch_digest,omitempty"`
	LanguageCounts      map[string]int `json:"language_counts"`
	ObservedAt          string         `json:"observed_at"`
}

type mutationJournalRuntimeStatus struct {
	ActiveEntries     int    `json:"active_entries"`
	LastPrunedAt      string `json:"last_pruned_at,omitempty"`
	LastPrunedEntries int    `json:"last_pruned_entries"`
}

type runtimeStatusData struct {
	// ReleaseVersion — see the comment on toolcontract.ResponseMeta.ReleaseVersion.
	// Named ServerVersion/server_version through v1.5.7; renamed (#563).
	ReleaseVersion          string                           `json:"release_version"`
	SchemaVersion           string                           `json:"schema_version"`
	Commit                  string                           `json:"commit,omitempty"`
	CommitTime              string                           `json:"commit_time,omitempty"`
	BuildChannel            string                           `json:"build_channel,omitempty"`
	BuildDirty              bool                             `json:"build_dirty"`
	BinaryBuildDirty        bool                             `json:"binary_build_dirty"`
	SiteWorktreeDirty       bool                             `json:"site_worktree_dirty"`
	SourceAheadOfPublic     bool                             `json:"source_ahead_of_public"`
	UnpublishedChangesCount int                              `json:"unpublished_changes_count"`
	SourceAheadReason       string                           `json:"source_ahead_reason"`
	PublicationState        string                           `json:"publication_state"`
	ProcessStartedAt        string                           `json:"process_started_at"`
	LastBuildPersistence    string                           `json:"last_build_persistence"`
	Hugo                    hugoRuntimeStatus                `json:"hugo"`
	Git                     gitRuntimeStatus                 `json:"git"`
	Site                    siteRuntimeStatus                `json:"site"`
	LastBuild               *lastBuildRuntimeStatus          `json:"last_build,omitempty"`
	ContentIndexShadow      *contentIndexShadowRuntimeStatus `json:"content_index_shadow,omitempty"`
	MutationJournal         *mutationJournalRuntimeStatus    `json:"mutation_journal,omitempty"`
	Degraded                []string                         `json:"degraded,omitempty"`
}

type getRuntimeStatusOutput struct {
	toolcontract.ToolResponse[runtimeStatusData]
}

var hugoVersionPattern = regexp.MustCompile(`v(\d+\.\d+\.\d+(?:-\S+)?)`)

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
	registerRuntimeStatus(s, cfg, srcIdx, nil, publicIndexes...)
}

// RegisterRuntimeStatusWithDB is the production registration path when the
// optional derived SQLite database is configured. It retains the public
// RegisterRuntimeStatus signature for focused tool tests while allowing a
// restarted process to report the most recently persisted build fact.
func RegisterRuntimeStatusWithDB(s *mcp.Server, cfg config.Config, srcIdx *hugosite.SourceIndex, siteDB *db.DB, publicIndexes ...*site.Index) {
	registerRuntimeStatus(s, cfg, srcIdx, siteDB, publicIndexes...)
}

func registerRuntimeStatus(s *mcp.Server, cfg config.Config, srcIdx *hugosite.SourceIndex, siteDB *db.DB, publicIndexes ...*site.Index) {
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
			"language/representation counts, counterpart gaps, and legacy mismatch facts; no page identity or body is exposed. When SQLite is configured, " +
			"`mutation_journal` reports only aggregate retention facts; `last_pruned_entries` is the number removed by the most recent successful maintenance " +
			"transaction. Read-only; does not expose secrets or arbitrary " +
			"host inventory. Use this instead of inferring environment health from error messages on other tools.",
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
		if len(publicIndexes) > 0 && publicIndexes[0] != nil && srcIdx != nil {
			resolver := site.NewPageResolver(publicIndexes[0], srcIdx, cfg)
			for _, source := range srcIdx.ListPages(0, 0) {
				if source.Draft {
					continue
				}
				resolved, ok := resolver.ResolveWithLang(source.Slug, source.Lang)
				if !ok || site.StateForResolvedPage(resolved, cfg.SiteRoot).BuildState == "pending" {
					externalPending++
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

		if snap := buildstatus.Last(); snap.Attempted {
			data.LastBuild = &lastBuildRuntimeStatus{
				Status:     snap.Status,
				ErrorClass: snap.ErrorClass,
				At:         snap.At.UTC().Format(time.RFC3339),
			}
			if snap.Status == "failed" {
				data.Degraded = append(data.Degraded, "build_site: last attempt failed ("+snap.ErrorClass+") at "+data.LastBuild.At)
			}
		} else if siteDB != nil {
			// The manifest is deliberately only a durable observation. The
			// source/public revision checks below still read the filesystem;
			// an out-of-band edit can make a persisted build fact stale.
			manifest, err := siteDB.LatestPublicationManifest()
			if err != nil {
				data.Degraded = append(data.Degraded, "publication manifest unavailable: "+err.Error())
			} else if manifest != nil {
				data.LastBuildPersistence = "sqlite_manifest"
				data.LastBuild = &lastBuildRuntimeStatus{
					BuildID: manifest.BuildID,
					Status:  manifest.Status,
					At:      manifest.ObservedAt.UTC().Format(time.RFC3339),
				}
			}
		}
		if siteDB != nil {
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

		meta := toolcontract.NewMeta(buildinfo.Version, time.Now())
		return nil, getRuntimeStatusOutput{ToolResponse: toolcontract.Success(data, meta)}, nil
	}))
}

// probeHugo shells out to `hugo version` with a bounded environment and
// timeout, and parses the semantic version and extended-build flag out of
// output like "hugo v0.150.0+extended linux/amd64 BuildDate=...".
func probeHugo(ctx context.Context, cfg config.Config) hugoRuntimeStatus {
	tctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	cmd := exec.CommandContext(tctx, "hugo", "version")
	cmd.Env = boundedCommandEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		reason := strings.TrimSpace(string(out))
		if reason == "" {
			reason = err.Error()
		}
		return hugoRuntimeStatus{Available: false, Error: sanitiseStderr([]byte(reason), cfg.HugoRoot, cfg.SiteRoot)}
	}
	text := strings.TrimSpace(string(out))
	status := hugoRuntimeStatus{Available: true, Extended: strings.Contains(text, "+extended")}
	if m := hugoVersionPattern.FindStringSubmatch(text); len(m) == 2 {
		status.Version = m[1]
	}
	return status
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

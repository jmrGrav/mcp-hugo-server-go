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
	"github.com/jmrGrav/mcp-hugo-server-go/internal/fileutil"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/gitutil"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/toolcontract"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// probeTimeout bounds every host command (hugo version, git rev-parse, ...)
// this tool shells out to, so a hung or missing binary can't stall the call.
const probeTimeout = 5 * time.Second

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

// lastBuildRuntimeStatus reports the outcome of the most recent build_site
// attempt in this process (#467), so an agent can notice a broken publish
// pipeline from a read-only status check instead of only discovering it by
// calling build_site itself at the end of a write cycle. Omitted entirely
// (via omitempty on the pointer) until build_site has been called at least
// once in this process's lifetime — there is no attempt to report yet.
type lastBuildRuntimeStatus struct {
	Status     string `json:"status"`
	ErrorClass string `json:"error_class,omitempty"`
	At         string `json:"at"`
}

type runtimeStatusData struct {
	// ReleaseVersion — see the comment on toolcontract.ResponseMeta.ReleaseVersion.
	// Named ServerVersion/server_version through v1.5.7; renamed (#563).
	ReleaseVersion    string                  `json:"release_version"`
	SchemaVersion     string                  `json:"schema_version"`
	Commit            string                  `json:"commit,omitempty"`
	CommitTime        string                  `json:"commit_time,omitempty"`
	BuildChannel      string                  `json:"build_channel,omitempty"`
	BuildDirty        bool                    `json:"build_dirty"`
	BinaryBuildDirty  bool                    `json:"binary_build_dirty"`
	SiteWorktreeDirty bool                    `json:"site_worktree_dirty"`
	Hugo              hugoRuntimeStatus       `json:"hugo"`
	Git               gitRuntimeStatus        `json:"git"`
	Site              siteRuntimeStatus       `json:"site"`
	LastBuild         *lastBuildRuntimeStatus `json:"last_build,omitempty"`
	Degraded          []string                `json:"degraded,omitempty"`
}

type getRuntimeStatusOutput struct {
	toolcontract.ToolResponse[runtimeStatusData]
}

var hugoVersionPattern = regexp.MustCompile(`v(\d+\.\d+\.\d+(?:-\S+)?)`)

// RegisterRuntimeStatus wires get_runtime_status (site.admin scope).
func RegisterRuntimeStatus(s *mcp.Server, cfg config.Config, srcIdx *hugosite.SourceIndex) {
	if s == nil {
		return
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:  "get_runtime_status",
		Title: "Get runtime status",
		Description: "Report the actual runtime/build/git/site status of this server in one compact structured surface: " +
			"server version and build commit, whether the hugo and git binaries are usable, the outcome of the most " +
			"recent build_site attempt (last_build, omitted until build_site has been called at least once), and " +
			"(opt-in via include_revisions, since hashing the full content/public trees is not cheap) source/public " +
			"revision hashes. When disposable `test_content` pages are overdue, `data.site.overdue_test_content[]` " +
			"surfaces a deterministic machine-readable list (`slug`, `owner?`, `expires_at`, `overdue_seconds`, `reason`) " +
			"so cleanup does not depend on remembering to run a build first. When the git baseline is dirty, `data.git.dirty_classes` " +
			"(#864) classifies WHAT KIND of resource changed — a safe, coarse set drawn from `content_source`, `generated_asset`, " +
			"`preview_residue`, `external_unknown` — so an operator can tell expected residue apart from unexpected drift without the " +
			"tool ever exposing file paths or contents (it deliberately does not attribute changes to mcp-vs-external, and `external_unknown` " +
			"is the honest default for anything not confidently recognized). Read-only; does not expose secrets or arbitrary " +
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
			ReleaseVersion:   buildinfo.Version,
			SchemaVersion:    buildinfo.SchemaVersion,
			Commit:           buildinfo.Commit,
			CommitTime:       buildinfo.CommitTime,
			BuildChannel:     buildinfo.EffectiveBuildChannel(),
			BuildDirty:       buildinfo.Dirty,
			BinaryBuildDirty: buildinfo.Dirty,
			Site: siteRuntimeStatus{
				ContentRootConfigured: strings.TrimSpace(cfg.ContentRoot) != "",
				HugoRootConfigured:    strings.TrimSpace(cfg.HugoRoot) != "",
			},
		}

		data.Hugo = probeHugo(ctx, cfg)
		data.Git = probeGitBaseline(ctx, cfg)
		data.SiteWorktreeDirty = data.Git.Dirty

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

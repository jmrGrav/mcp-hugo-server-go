package admin

import (
	"context"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/buildinfo"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/buildstatus"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/fileutil"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/gitutil"
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
	BaselineMode string `json:"baseline_mode"`
	Available    bool   `json:"available"`
	Branch       string `json:"branch,omitempty"`
	HeadCommit   string `json:"head_commit,omitempty"`
	Dirty        bool   `json:"dirty"`
	Error        string `json:"error,omitempty"`
}

type siteRuntimeStatus struct {
	ContentRootConfigured bool   `json:"content_root_configured"`
	HugoRootConfigured    bool   `json:"hugo_root_configured"`
	SourceRevision        string `json:"source_revision,omitempty"`
	PublicRevision        string `json:"public_revision,omitempty"`
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
	// ServerVersion already carries the release identity by itself — see the
	// comment on toolcontract.ResponseMeta.ServerVersion for why a separate
	// release_version field was removed here too (#560).
	ServerVersion string                  `json:"server_version"`
	SchemaVersion string                  `json:"schema_version"`
	Commit        string                  `json:"commit,omitempty"`
	CommitTime    string                  `json:"commit_time,omitempty"`
	BuildChannel  string                  `json:"build_channel,omitempty"`
	BuildDirty    bool                    `json:"build_dirty"`
	Hugo          hugoRuntimeStatus       `json:"hugo"`
	Git           gitRuntimeStatus        `json:"git"`
	Site          siteRuntimeStatus       `json:"site"`
	LastBuild     *lastBuildRuntimeStatus `json:"last_build,omitempty"`
	Degraded      []string                `json:"degraded,omitempty"`
}

type getRuntimeStatusOutput struct {
	toolcontract.ToolResponse[runtimeStatusData]
}

var hugoVersionPattern = regexp.MustCompile(`v(\d+\.\d+\.\d+(?:-\S+)?)`)

// RegisterRuntimeStatus wires get_runtime_status (site.admin scope).
func RegisterRuntimeStatus(s *mcp.Server, cfg config.Config) {
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
			"revision hashes. Read-only; does not expose secrets or arbitrary host inventory. Use this instead of " +
			"inferring environment health from error messages on other tools.",
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
			ServerVersion: buildinfo.Version,
			SchemaVersion: buildinfo.SchemaVersion,
			Commit:        buildinfo.Commit,
			CommitTime:    buildinfo.CommitTime,
			BuildChannel:  buildinfo.EffectiveBuildChannel(),
			BuildDirty:    buildinfo.Dirty,
			Site: siteRuntimeStatus{
				ContentRootConfigured: strings.TrimSpace(cfg.ContentRoot) != "",
				HugoRootConfigured:    strings.TrimSpace(cfg.HugoRoot) != "",
			},
		}

		data.Hugo = probeHugo(ctx, cfg)
		data.Git = probeGitBaseline(ctx, cfg)

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
		status.Dirty = strings.TrimSpace(porcelain) != ""
	}

	status.Available = true
	return status
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

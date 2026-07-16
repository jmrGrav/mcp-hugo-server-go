package admin

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/buildinfo"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/fileutil"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/toolcontract"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// probeTimeout bounds every host command (hugo version, git rev-parse, ...)
// this tool shells out to, so a hung or missing binary can't stall the call.
const probeTimeout = 5 * time.Second

type getRuntimeStatusInput struct{}

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

type runtimeStatusData struct {
	ServerVersion string            `json:"server_version"`
	SchemaVersion string            `json:"schema_version"`
	Commit        string            `json:"commit,omitempty"`
	CommitTime    string            `json:"commit_time,omitempty"`
	BuildDirty    bool              `json:"build_dirty"`
	Hugo          hugoRuntimeStatus `json:"hugo"`
	Git           gitRuntimeStatus  `json:"git"`
	Site          siteRuntimeStatus `json:"site"`
	Degraded      []string          `json:"degraded,omitempty"`
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
			"server version and build commit, whether the hugo and git binaries are usable, and best-effort source/public " +
			"revision hashes. Read-only; does not expose secrets or arbitrary host inventory. Use this instead of inferring " +
			"environment health from error messages on other tools.",
		InputSchema:  tools.MustSchema[getRuntimeStatusInput](),
		OutputSchema: tools.MustSchema[getRuntimeStatusOutput](),
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    true,
			DestructiveHint: fileutil.BoolPtr(false),
			IdempotentHint:  true,
			OpenWorldHint:   fileutil.BoolPtr(true),
		},
	}, toolcontract.WrapTool(func(ctx context.Context, _ *mcp.CallToolRequest, _ getRuntimeStatusInput) (*mcp.CallToolResult, getRuntimeStatusOutput, error) {
		data := runtimeStatusData{
			ServerVersion: buildinfo.Version,
			SchemaVersion: buildinfo.SchemaVersion,
			Commit:        buildinfo.Commit,
			CommitTime:    buildinfo.CommitTime,
			BuildDirty:    buildinfo.Dirty,
			Site: siteRuntimeStatus{
				ContentRootConfigured: strings.TrimSpace(cfg.ContentRoot) != "",
				HugoRootConfigured:    strings.TrimSpace(cfg.HugoRoot) != "",
			},
		}

		data.Hugo = probeHugo(ctx, cfg)
		data.Git = probeGitBaseline(ctx, cfg)

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

		if !data.Hugo.Available {
			data.Degraded = append(data.Degraded, "build_site/preview_build: hugo binary is unavailable — "+data.Hugo.Error)
		}
		if !data.Git.Available {
			data.Degraded = append(data.Degraded, "diff_page: git-backed source diffs are unavailable — "+data.Git.Error)
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

	tctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	gitRoot, err := gitStatusOutput(tctx, root, "rev-parse", "--show-toplevel")
	if err != nil {
		status.Error = sanitiseStderr([]byte(err.Error()), cfg.HugoRoot, cfg.SiteRoot)
		return status
	}

	branch, err := gitStatusOutput(tctx, gitRoot, "rev-parse", "--abbrev-ref", "HEAD")
	if err == nil {
		status.Branch = branch
	}
	head, err := gitStatusOutput(tctx, gitRoot, "rev-parse", "--short", "HEAD")
	if err != nil {
		status.Error = sanitiseStderr([]byte(err.Error()), cfg.HugoRoot, cfg.SiteRoot)
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

func gitStatusOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

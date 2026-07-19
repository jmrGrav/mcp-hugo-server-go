package read

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/contentmodel"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/fileutil"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/gitutil"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/toolcontract"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type diffPageInput struct {
	Slug         string `json:"slug"`
	ResponseMode string `json:"response_mode,omitempty"`
}

type diffPageData struct {
	Slug          string              `json:"slug"`
	SourceKey     string              `json:"source_key,omitempty"`
	Path          string              `json:"path"`
	ResolvedLang  string              `json:"resolved_lang"`
	ResolvedPath  string              `json:"resolved_source_path"`
	State         site.LifecycleState `json:"state"`
	Status        string              `json:"status"`
	DiffAvailable bool                `json:"diff_available"`
	FallbackMode  string              `json:"fallback_mode,omitempty"`
	BaseCommit    string              `json:"base_commit"`
	HeadCommit    string              `json:"head_commit"`
	Diff          string              `json:"diff"`
	SourceContent string              `json:"source_content,omitempty"`
}

type diffPageOutput struct {
	toolcontract.ToolResponse[diffPageData]
}

func RegisterDiffPage(s *mcp.Server, idx *site.Index, srcIdx *hugosite.SourceIndex, cfg config.Config) {
	if s == nil {
		return
	}
	addReadOnlyTool(s, "diff_page", "Diff page", "Show a read-only diff for a Hugo source page against the current Git HEAD. Requires a local Git repository and a configured content root. Reader tool: on OAuth-enabled deployments, call it with a read Bearer token. The response includes lifecycle `state` so agents can tell whether the source is already built/public or still ahead of the public site. Use this before editing or reviewing a page.",
		func(ctx context.Context, _ *mcp.CallToolRequest, in diffPageInput) (*mcp.CallToolResult, diffPageOutput, error) {
			if site.IsReaderProfile(ctx) {
				return nil, diffPageOutput{}, fmt.Errorf("content_not_public: reader profile cannot access source git diagnostics")
			}
			if srcIdx == nil {
				return nil, diffPageOutput{}, fmt.Errorf("git_metadata_unavailable: source index not initialized")
			}
			if strings.TrimSpace(in.Slug) == "" {
				return nil, diffPageOutput{}, fmt.Errorf("invalid_params: slug must not be empty")
			}
			resolver := site.NewPageResolver(idx, srcIdx, cfg)
			resolved, ok := resolver.Resolve(in.Slug)
			if !ok || resolved.Source == nil {
				return nil, diffPageOutput{}, fmt.Errorf("content_not_found: page not found for slug %q", in.Slug)
			}
			contentRoot := strings.TrimSpace(cfg.ContentRoot)
			if contentRoot == "" {
				return nil, diffPageOutput{}, fmt.Errorf("git_metadata_unavailable: content root not configured")
			}
			unavailableFallback := func(reason string) diffPageOutput {
				relPath := resolved.Source.Slug
				if resolved.SourcePath != "" {
					if rel, relErr := filepath.Rel(contentRoot, resolved.SourcePath); relErr == nil {
						relPath = rel
					}
				}
				resp := newDiffPageOutput(diffPageData{
					Slug:          canonicalResolvedSlug(resolved),
					SourceKey:     contentmodel.SourceKeyFromLogicalPath(resolvedLogicalPath(contentRoot, resolved.SourcePath, relPath)),
					Path:          relPath,
					ResolvedLang:  resolved.Source.Lang,
					ResolvedPath:  resolvedLogicalPath(contentRoot, resolved.SourcePath, relPath),
					State:         resolvedState(resolved, cfg.SiteRoot),
					Status:        "git_unavailable",
					DiffAvailable: false,
					FallbackMode:  "source_content",
					HeadCommit:    "working-tree",
					SourceContent: resolved.Source.Body,
				}, time.Now().UTC())
				resp.Warnings = []string{fmt.Sprintf("Git repository metadata is unavailable (%s); returning source content without a diff.", reason)}
				return resp
			}
			if cfg.GitBaseline.Mode == "disabled" {
				return nil, unavailableFallback("git baseline is disabled by configuration"), nil
			}
			gitRoot, err := findGitRoot(ctx, contentRoot)
			if err != nil {
				return nil, unavailableFallback(strings.TrimSpace(err.Error())), nil
			}
			absPath := resolved.SourcePath
			if absPath == "" {
				return nil, diffPageOutput{}, fmt.Errorf("content_not_found: page not readable for slug %q", in.Slug)
			}
			relPath, err := filepath.Rel(contentRoot, absPath)
			if err != nil {
				return nil, diffPageOutput{}, fmt.Errorf("content_not_found: page not found for slug %q", in.Slug)
			}
			relRepoPath, err := filepath.Rel(gitRoot, absPath)
			if err != nil || strings.HasPrefix(relRepoPath, "..") {
				return nil, diffPageOutput{}, fmt.Errorf("git_metadata_unavailable: source page is outside the repository root")
			}
			headCommit, err := gitOutput(ctx, gitRoot, "rev-parse", "--short", "HEAD")
			if err != nil {
				return nil, diffPageOutput{}, fmt.Errorf("git_metadata_unavailable: unable to read repository HEAD")
			}
			baseContent, baseExists, err := gitShowFile(ctx, gitRoot, relRepoPath)
			if err != nil && !isGitPathMissing(err) {
				return nil, diffPageOutput{}, fmt.Errorf("git_metadata_unavailable: unable to read tracked version")
			}
			if !baseExists {
				baseContent = nil
			}
			currentContent, err := os.ReadFile(absPath)
			if err != nil {
				return nil, diffPageOutput{}, fmt.Errorf("content_not_found: page not readable for slug %q", in.Slug)
			}
			status := diffStatus(baseExists, currentContent, baseContent)
			if status == "git_untracked" {
				resp := newDiffPageOutput(diffPageData{
					Slug:          canonicalResolvedSlug(resolved),
					SourceKey:     contentmodel.SourceKeyFromLogicalPath(resolvedLogicalPath(contentRoot, absPath, relPath)),
					Path:          relPath,
					ResolvedLang:  resolved.Source.Lang,
					ResolvedPath:  resolvedLogicalPath(contentRoot, absPath, relPath),
					State:         resolvedState(resolved, cfg.SiteRoot),
					Status:        status,
					DiffAvailable: false,
					FallbackMode:  "source_content",
					BaseCommit:    strings.TrimSpace(headCommit),
					HeadCommit:    "working-tree",
					SourceContent: string(currentContent),
				}, time.Now().UTC())
				resp.Warnings = []string{"File is new and not yet tracked by git — showing full source instead of diff."}
				return nil, resp, nil
			}
			diffText, err := unifiedDiff(relPath, baseContent, currentContent)
			if err != nil {
				return nil, diffPageOutput{}, fmt.Errorf("git_metadata_unavailable: unable to compute diff")
			}
			return nil, newDiffPageOutput(diffPageData{
				Slug:          canonicalResolvedSlug(resolved),
				SourceKey:     contentmodel.SourceKeyFromLogicalPath(resolvedLogicalPath(contentRoot, absPath, relPath)),
				Path:          relPath,
				ResolvedLang:  resolved.Source.Lang,
				ResolvedPath:  resolvedLogicalPath(contentRoot, absPath, relPath),
				State:         resolvedState(resolved, cfg.SiteRoot),
				Status:        status,
				DiffAvailable: true,
				BaseCommit:    strings.TrimSpace(headCommit),
				HeadCommit:    "working-tree",
				Diff:          diffText,
			}, time.Now().UTC()), nil
		})
}

func newDiffPageOutput(data diffPageData, now time.Time) diffPageOutput {
	return diffPageOutput{ToolResponse: successEnvelope(data, now)}
}

func resolvedLogicalPath(contentRoot, absPath, relPath string) string {
	if logical := fileutil.LogicalContentPath(contentRoot, absPath); logical != "" {
		return logical
	}
	return filepath.ToSlash(filepath.Join("content", relPath))
}

// findGitRoot discovers the repository root via a pure filesystem walk
// (gitutil.DiscoverRoot), not by invoking git — see gitutil's package
// comment for why: git's own discovery command is itself blocked by the
// dubious-ownership check this indirection exists to route around.
func findGitRoot(_ context.Context, start string) (string, error) {
	return gitutil.DiscoverRoot(start)
}

func gitShowFile(ctx context.Context, gitRoot, relPath string) ([]byte, bool, error) {
	out, err := gitutil.Bytes(ctx, gitRoot, "show", "HEAD:"+filepath.ToSlash(relPath))
	if err == nil {
		return out, true, nil
	}
	if isGitPathMissing(err) {
		return nil, false, nil
	}
	return nil, false, err
}

func gitOutput(ctx context.Context, gitRoot string, args ...string) (string, error) {
	return gitutil.Output(ctx, gitRoot, args...)
}

func isGitPathMissing(err error) bool {
	if err == nil {
		return false
	}
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr) && exitErr.ExitCode() == 128
}

func diffStatus(baseExists bool, current, base []byte) string {
	switch {
	case baseExists && bytes.Equal(current, base):
		return "unchanged"
	case baseExists:
		return "modified"
	case len(current) == 0:
		return "deleted"
	default:
		return "git_untracked"
	}
}

func unifiedDiff(relPath string, base, current []byte) (string, error) {
	baseFile, err := os.CreateTemp("", "mcp-diff-base-*.md")
	if err != nil {
		return "", err
	}
	defer os.Remove(baseFile.Name())
	defer baseFile.Close()

	currentFile, err := os.CreateTemp("", "mcp-diff-current-*.md")
	if err != nil {
		return "", err
	}
	defer os.Remove(currentFile.Name())
	defer currentFile.Close()

	if _, err := baseFile.Write(base); err != nil {
		return "", err
	}
	if _, err := currentFile.Write(current); err != nil {
		return "", err
	}
	if err := baseFile.Close(); err != nil {
		return "", err
	}
	if err := currentFile.Close(); err != nil {
		return "", err
	}

	cmd := exec.Command("git", "diff", "--no-index", "--unified=3", "--no-renames", "--", baseFile.Name(), currentFile.Name())
	out, err := cmd.CombinedOutput()
	if err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			return "", err
		}
		if exitErr.ExitCode() != 1 {
			return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
		}
	}
	diff := string(out)
	diff = strings.ReplaceAll(diff, baseFile.Name(), "a/"+filepath.ToSlash(relPath))
	diff = strings.ReplaceAll(diff, currentFile.Name(), "b/"+filepath.ToSlash(relPath))
	return diff, nil
}

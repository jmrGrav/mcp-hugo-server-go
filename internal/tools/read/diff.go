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
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type diffPageInput struct {
	Slug string `json:"slug"`
}

type diffPageData struct {
	Slug          string `json:"slug"`
	Path          string `json:"path"`
	ResolvedLang  string `json:"resolved_lang"`
	ResolvedPath  string `json:"resolved_source_path"`
	Status        string `json:"status"`
	BaseCommit    string `json:"base_commit"`
	HeadCommit    string `json:"head_commit"`
	Diff          string `json:"diff"`
	SourceContent string `json:"source_content,omitempty"`
}

type diffPageOutput struct {
	Success     bool         `json:"success"`
	Version     string       `json:"version"`
	GeneratedAt string       `json:"generated_at"`
	Data        diffPageData `json:"data"`
	Warnings    []string     `json:"warnings"`
	Errors      []string     `json:"errors"`
}

func RegisterDiffPage(s *mcp.Server, idx *site.Index, srcIdx *hugosite.SourceIndex, cfg config.Config) {
	if s == nil {
		return
	}
	addReadOnlyTool(s, "diff_page", "Diff page", "Show a read-only diff for a Hugo source page against the current Git HEAD. Requires a local Git repository, a configured content root, and content.read. Use this before editing or reviewing a page.",
		func(ctx context.Context, _ *mcp.CallToolRequest, in diffPageInput) (*mcp.CallToolResult, diffPageOutput, error) {
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
			gitRoot, err := findGitRoot(ctx, contentRoot)
			if err != nil {
				relPath := resolved.Source.Slug
				if resolved.SourcePath != "" {
					if rel, relErr := filepath.Rel(contentRoot, resolved.SourcePath); relErr == nil {
						relPath = rel
					}
				}
				return nil, diffPageOutput{
					Success:     true,
					Version:     toolResultVersion,
					GeneratedAt: time.Now().UTC().Format(time.RFC3339),
					Data: diffPageData{
						Slug:          resolved.Source.Slug,
						Path:          relPath,
						ResolvedLang:  resolved.Source.Lang,
						ResolvedPath:  absPathOrResolved(resolved.SourcePath, relPath),
						Status:        "git_not_available",
						HeadCommit:    "working-tree",
						SourceContent: resolved.Source.Body,
					},
					Warnings: []string{"Git repository metadata is unavailable; returning source content without a diff."},
					Errors:   []string{},
				}, nil
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
			diffText, err := unifiedDiff(relPath, baseContent, currentContent)
			if err != nil {
				return nil, diffPageOutput{}, fmt.Errorf("git_metadata_unavailable: unable to compute diff")
			}
			return nil, diffPageOutput{
				Success:     true,
				Version:     toolResultVersion,
				GeneratedAt: time.Now().UTC().Format(time.RFC3339),
				Data: diffPageData{
					Slug:         resolved.Source.Slug,
					Path:         relPath,
					ResolvedLang: resolved.Source.Lang,
					ResolvedPath: absPathOrResolved(absPath, relPath),
					Status:       status,
					BaseCommit:   strings.TrimSpace(headCommit),
					HeadCommit:   "working-tree",
					Diff:         diffText,
				},
				Warnings: []string{},
				Errors:   []string{},
			}, nil
		})
}

func absPathOrResolved(absPath, relPath string) string {
	if strings.TrimSpace(absPath) != "" {
		return absPath
	}
	return filepath.ToSlash(relPath)
}

func findGitRoot(ctx context.Context, start string) (string, error) {
	return gitOutput(ctx, start, "rev-parse", "--show-toplevel")
}

func gitShowFile(ctx context.Context, gitRoot, relPath string) ([]byte, bool, error) {
	out, err := gitBytes(ctx, gitRoot, "show", "HEAD:"+filepath.ToSlash(relPath))
	if err == nil {
		return out, true, nil
	}
	if isGitPathMissing(err) {
		return nil, false, nil
	}
	return nil, false, err
}

func gitOutput(ctx context.Context, gitRoot string, args ...string) (string, error) {
	out, err := gitBytes(ctx, gitRoot, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func gitBytes(ctx context.Context, gitRoot string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", gitRoot}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return out, nil
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
		return "added"
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

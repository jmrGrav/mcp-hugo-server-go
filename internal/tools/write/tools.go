package write

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/fileutil"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/oauth"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/security"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/time/rate"
	"gopkg.in/yaml.v3"
)

type createPageInput struct {
	Slug       string   `json:"slug"`
	Title      string   `json:"title"`
	Body       string   `json:"body"`
	Tags       []string `json:"tags"`
	Categories []string `json:"categories"`
}

type createPageOutput struct {
	Slug string `json:"slug"`
	Path string `json:"path"`
}

type updatePageInput struct {
	Slug  string `json:"slug"`
	Title string `json:"title,omitempty"`
	Body  string `json:"body,omitempty"`
}

type updatePageOutput struct {
	Slug string `json:"slug"`
}

type deletePageInput struct {
	Slug string `json:"slug"`
}

type deletePageOutput struct {
	Slug string `json:"slug"`
}

var reservedSlugs = map[string]bool{
	"_index": true,
	"index":  true,
}

// deleteCallerKey builds a rate-limit key that isolates delete budgets by caller IP.
// Falls back to "unknown" when context carries no IP (e.g. in tests).
func deleteCallerKey(ctx context.Context) string {
	ip, _ := ctx.Value(oauth.CtxCallerIP).(string)
	if ip == "" {
		ip = "unknown"
	}
	return ip
}

// deleteCallerLimiter returns (or creates) a per-caller rate.Limiter for deletions.
func deleteCallerLimiter(mu *sync.Mutex, m map[string]*rate.Limiter, key string) *rate.Limiter {
	mu.Lock()
	defer mu.Unlock()
	if l, ok := m[key]; ok {
		return l
	}
	l := rate.NewLimiter(rate.Every(time.Minute/5), 5)
	m[key] = l
	return l
}

func Register(s *mcp.Server, pg *security.PathGuard, idx *hugosite.SourceIndex, cfg config.Config) {
	if s == nil {
		return
	}

	var deleteMu sync.Mutex
	deleteLimiters := make(map[string]*rate.Limiter)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "create_page",
		Title:       "Publish page",
		Description: "Create a new Hugo content page at {slug}/index.md with front matter and body content. Use this when drafting a new page.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: fileutil.BoolPtr(false),
			IdempotentHint:  false,
			OpenWorldHint:   fileutil.BoolPtr(false),
		},
	}, func(_ context.Context, _ *mcp.CallToolRequest, in createPageInput) (*mcp.CallToolResult, createPageOutput, error) {
		if in.Slug == "" {
			return nil, createPageOutput{}, fmt.Errorf("invalid_params: slug must not be empty")
		}
		if in.Title == "" {
			return nil, createPageOutput{}, fmt.Errorf("invalid_params: title must not be empty")
		}
		if reservedSlugs[in.Slug] {
			return nil, createPageOutput{}, fmt.Errorf("invalid_params: slug is reserved")
		}

		dir, err := pg.SafeJoin(in.Slug)
		if err != nil {
			slog.Warn("create_page: path validation failed", "slug", in.Slug, "error", err)
			return nil, createPageOutput{}, fmt.Errorf("invalid_params: path validation failed")
		}

		filePath := filepath.Join(dir, "index.md")
		content := buildFrontmatter(in.Title, in.Tags, in.Categories, in.Body)

		const lockWait = 10 * time.Second
		deadline := time.Now().Add(lockWait)
		for {
			if hugosite.ContentMu.TryLock() {
				slog.Debug("create_page: lock_acquired")
				break
			}
			if time.Now().After(deadline) {
				slog.Error("create_page: lock_timeout", "timeout_s", lockWait.Seconds())
				return nil, createPageOutput{}, fmt.Errorf("build_in_progress: content lock is held, retry in a moment")
			}
			time.Sleep(50 * time.Millisecond)
		}
		defer func() {
			hugosite.ContentMu.Unlock()
			slog.Debug("create_page: lock_released")
		}()

		if err := fileutil.AtomicWrite(filePath, content); err != nil {
			slog.Error("create_page: write failed", "slug", in.Slug, "error", err)
			return nil, createPageOutput{}, fmt.Errorf("write_error: failed to write page")
		}
		idx.Upsert(hugosite.SourcePage{
			Slug:           in.Slug,
			FilePath:       filePath,
			Title:          in.Title,
			Tags:           in.Tags,
			Categories:     in.Categories,
			Body:           in.Body,
			FrontmatterRaw: map[string]any{"title": in.Title},
		})

		return nil, createPageOutput{Slug: in.Slug, Path: filePath}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "update_page",
		Title:       "Update page",
		Description: "Update an existing Hugo content page while preserving existing front matter fields. Use this to revise title or body content.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: fileutil.BoolPtr(false),
			IdempotentHint:  false,
			OpenWorldHint:   fileutil.BoolPtr(false),
		},
	}, func(_ context.Context, _ *mcp.CallToolRequest, in updatePageInput) (*mcp.CallToolResult, updatePageOutput, error) {
		if in.Slug == "" {
			return nil, updatePageOutput{}, fmt.Errorf("invalid_params: slug must not be empty")
		}

		const lockWait = 10 * time.Second
		deadline := time.Now().Add(lockWait)
		for {
			if hugosite.ContentMu.TryLock() {
				slog.Debug("update_page: lock_acquired")
				break
			}
			if time.Now().After(deadline) {
				slog.Error("update_page: lock_timeout", "timeout_s", lockWait.Seconds())
				return nil, updatePageOutput{}, fmt.Errorf("build_in_progress: content lock is held, retry in a moment")
			}
			time.Sleep(50 * time.Millisecond)
		}
		defer func() {
			hugosite.ContentMu.Unlock()
			slog.Debug("update_page: lock_released")
		}()

		existing, ok := idx.GetBySlug(in.Slug)
		if !ok {
			return nil, updatePageOutput{}, fmt.Errorf("not_found: page not found")
		}

		dir, err := pg.SafeJoin(in.Slug)
		if err != nil {
			slog.Warn("update_page: path validation failed", "slug", in.Slug, "error", err)
			return nil, updatePageOutput{}, fmt.Errorf("invalid_params: path validation failed")
		}
		filePath := filepath.Join(dir, "index.md")

		raw, err := os.ReadFile(filePath)
		if err != nil {
			slog.Error("update_page: read failed", "slug", in.Slug, "error", err)
			return nil, updatePageOutput{}, fmt.Errorf("read_error: failed to read page")
		}
		content, err := applyPageUpdates(string(raw), in.Title, in.Body)
		if err != nil {
			slog.Error("update_page: frontmatter update failed", "slug", in.Slug, "error", err)
			return nil, updatePageOutput{}, fmt.Errorf("parse_error: failed to update frontmatter")
		}
		if err := fileutil.AtomicWrite(filePath, content); err != nil {
			slog.Error("update_page: write failed", "slug", in.Slug, "error", err)
			return nil, updatePageOutput{}, fmt.Errorf("write_error: failed to write page")
		}
		updated := *existing
		if in.Title != "" {
			updated.Title = in.Title
			if updated.FrontmatterRaw == nil {
				updated.FrontmatterRaw = make(map[string]any)
			}
			updated.FrontmatterRaw["title"] = in.Title
		}
		if in.Body != "" {
			updated.Body = in.Body
		}
		idx.Upsert(updated)

		return nil, updatePageOutput{Slug: in.Slug}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "delete_page",
		Title:       "Delete page",
		Description: "Delete a Hugo content page. This is destructive and rate limited to 5 deletions per minute.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: fileutil.BoolPtr(true),
			IdempotentHint:  true,
			OpenWorldHint:   fileutil.BoolPtr(false),
		},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in deletePageInput) (*mcp.CallToolResult, deletePageOutput, error) {
		if in.Slug == "" {
			return nil, deletePageOutput{}, fmt.Errorf("invalid_params: slug must not be empty")
		}

		dir, err := pg.SafeJoin(in.Slug)
		if err != nil {
			slog.Warn("delete_page: path validation failed", "slug", in.Slug, "error", err)
			return nil, deletePageOutput{}, fmt.Errorf("invalid_params: path validation failed")
		}

		callerKey := deleteCallerKey(ctx)
		if !deleteCallerLimiter(&deleteMu, deleteLimiters, callerKey).Allow() {
			return nil, deletePageOutput{}, fmt.Errorf("rate_limit_exceeded: delete_page is limited to 5 per minute")
		}

		const lockWait = 10 * time.Second
		deadline := time.Now().Add(lockWait)
		for {
			if hugosite.ContentMu.TryLock() {
				slog.Debug("delete_page: lock_acquired")
				break
			}
			if time.Now().After(deadline) {
				slog.Error("delete_page: lock_timeout", "timeout_s", lockWait.Seconds())
				return nil, deletePageOutput{}, fmt.Errorf("build_in_progress: content lock is held, retry in a moment")
			}
			time.Sleep(50 * time.Millisecond)
		}
		defer func() {
			hugosite.ContentMu.Unlock()
			slog.Debug("delete_page: lock_released")
		}()

		if err := os.RemoveAll(dir); err != nil {
			slog.Error("delete_page: remove failed", "slug", in.Slug, "error", err)
			return nil, deletePageOutput{}, fmt.Errorf("delete_error: failed to delete page")
		}
		idx.Delete(in.Slug)

		auditLog := filepath.Join(cfg.ContentRoot, ".mcp-audit.log")
		entry := fmt.Sprintf("%s DELETE %s\n", time.Now().UTC().Format(time.RFC3339), in.Slug)
		if err := appendAuditLog(auditLog, entry); err != nil {
			slog.Error("delete_page: audit log write failed", "slug", in.Slug, "error", err)
			return nil, deletePageOutput{}, fmt.Errorf("audit_error: failed to write audit log")
		}

		return nil, deletePageOutput(in), nil
	})
}

type frontmatterDoc struct {
	Title      string   `yaml:"title"`
	Date       string   `yaml:"date"`
	Tags       []string `yaml:"tags"`
	Categories []string `yaml:"categories"`
	Draft      bool     `yaml:"draft"`
}

func buildFrontmatter(title string, tags, categories []string, body string) string {
	if tags == nil {
		tags = []string{}
	}
	if categories == nil {
		categories = []string{}
	}
	doc := frontmatterDoc{
		Title:      title,
		Date:       time.Now().UTC().Format(time.RFC3339),
		Tags:       tags,
		Categories: categories,
		Draft:      false,
	}
	raw, _ := yaml.Marshal(doc)
	var sb strings.Builder
	sb.WriteString("---\n")
	sb.Write(raw)
	sb.WriteString("---")
	if body != "" {
		sb.WriteString("\n\n")
		sb.WriteString(body)
	}
	return sb.String()
}

func buildFrontmatterFromMap(fm map[string]any, body string) string {
	raw, _ := yaml.Marshal(fm)
	var sb strings.Builder
	sb.WriteString("---\n")
	sb.Write(raw)
	sb.WriteString("---")
	if body != "" {
		sb.WriteString("\n\n")
		sb.WriteString(body)
	}
	return sb.String()
}

// applyPageUpdates applies title and/or body changes to an existing page file
// using the yaml.v3 Node API to preserve field ordering, comments, and YAML
// style in the frontmatter (issue #111).
func applyPageUpdates(fileContent, newTitle, newBody string) (string, error) {
	if !strings.HasPrefix(fileContent, "---\n") {
		return "", fmt.Errorf("no YAML frontmatter delimiter")
	}
	rest := fileContent[4:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", fmt.Errorf("unterminated YAML frontmatter")
	}
	yamlPart := rest[:end]
	bodyPart := rest[end+4:] // everything after the closing ---

	if newTitle != "" {
		var doc yaml.Node
		if err := yaml.Unmarshal([]byte(yamlPart), &doc); err != nil {
			return "", fmt.Errorf("YAML parse: %w", err)
		}
		if len(doc.Content) > 0 {
			setYAMLKey(doc.Content[0], "title", newTitle)
		}
		out, err := yaml.Marshal(doc.Content[0])
		if err != nil {
			return "", fmt.Errorf("YAML marshal: %w", err)
		}
		yamlPart = strings.TrimRight(string(out), "\n")
	}

	if newBody != "" {
		bodyPart = "\n\n" + newBody
	}

	return "---\n" + yamlPart + "\n---" + bodyPart, nil
}

// setYAMLKey updates the value of key in a YAML mapping node in-place,
// appending a new key-value pair when key is absent.
func setYAMLKey(mapping *yaml.Node, key, value string) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content[i+1].SetString(value)
			return
		}
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value},
	)
}

// Defs returns the tool definitions for this package (used to build the global registry).
func Defs() []tools.ToolDef {
	return []tools.ToolDef{
		{Name: "create_page", RequiredScope: "content.write"},
		{Name: "update_page", RequiredScope: "content.write"},
		{Name: "delete_page", RequiredScope: "content.write"},
	}
}

func appendAuditLog(path, entry string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(entry)
	return err
}

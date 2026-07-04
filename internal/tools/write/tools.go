package write

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/fileutil"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
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

func Register(s *mcp.Server, pg *security.PathGuard, idx *hugosite.SourceIndex, cfg config.Config) {
	if s == nil {
		return
	}

	deleteLimiter := rate.NewLimiter(rate.Every(time.Minute/5), 5)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "create_page",
		Title:       "Publish page",
		Description: "[RequiredScope: content.write] Create a new Hugo content page at {slug}/index.md with front matter and body content. Use this when drafting a new page.",
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

		hugosite.ContentMu.Lock()
		defer hugosite.ContentMu.Unlock()

		if err := fileutil.AtomicWrite(filePath, content); err != nil {
			slog.Error("create_page: write failed", "slug", in.Slug, "error", err)
			return nil, createPageOutput{}, fmt.Errorf("write_error: failed to write page")
		}
		idx.Upsert(hugosite.SourcePage{
			Slug:           in.Slug,
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
		Description: "[RequiredScope: content.write] Update an existing Hugo content page while preserving existing front matter fields. Use this to revise title or body content.",
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

		hugosite.ContentMu.Lock()
		defer hugosite.ContentMu.Unlock()

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

		fm := make(map[string]any, len(existing.FrontmatterRaw))
		for k, v := range existing.FrontmatterRaw {
			fm[k] = v
		}
		if in.Title != "" {
			fm["title"] = in.Title
		}

		body := existing.Body
		if in.Body != "" {
			body = in.Body
		}

		content := buildFrontmatterFromMap(fm, body)
		if err := fileutil.AtomicWrite(filePath, content); err != nil {
			slog.Error("update_page: write failed", "slug", in.Slug, "error", err)
			return nil, updatePageOutput{}, fmt.Errorf("write_error: failed to write page")
		}
		updated := *existing
		if in.Title != "" {
			updated.Title = in.Title
			updated.FrontmatterRaw = fm
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
		Description: "[RequiredScope: content.write] Delete a Hugo content page. This is destructive and rate limited to 5 deletions per minute.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: fileutil.BoolPtr(true),
			IdempotentHint:  true,
			OpenWorldHint:   fileutil.BoolPtr(false),
		},
	}, func(_ context.Context, _ *mcp.CallToolRequest, in deletePageInput) (*mcp.CallToolResult, deletePageOutput, error) {
		if in.Slug == "" {
			return nil, deletePageOutput{}, fmt.Errorf("invalid_params: slug must not be empty")
		}

		dir, err := pg.SafeJoin(in.Slug)
		if err != nil {
			slog.Warn("delete_page: path validation failed", "slug", in.Slug, "error", err)
			return nil, deletePageOutput{}, fmt.Errorf("invalid_params: path validation failed")
		}
		filePath := filepath.Join(dir, "index.md")

		if !deleteLimiter.Allow() {
			return nil, deletePageOutput{}, fmt.Errorf("rate_limit_exceeded: delete_page is limited to 5 per minute")
		}

		hugosite.ContentMu.Lock()
		defer hugosite.ContentMu.Unlock()

		if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
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


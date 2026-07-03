package write

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/security"
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
		Title:       "Create page",
		Description: "[RequiredScope: content.write] Create a new Hugo content page at {slug}/index.md.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: boolPtr(false),
			IdempotentHint:  false,
			OpenWorldHint:   boolPtr(false),
		},
	}, func(_ context.Context, _ *mcp.CallToolRequest, in createPageInput) (*mcp.CallToolResult, createPageOutput, error) {
		if in.Slug == "" {
			return nil, createPageOutput{}, fmt.Errorf("invalid_params: slug must not be empty")
		}
		if in.Title == "" {
			return nil, createPageOutput{}, fmt.Errorf("invalid_params: title must not be empty")
		}
		if reservedSlugs[in.Slug] {
			return nil, createPageOutput{}, fmt.Errorf("invalid_params: slug %q is reserved", in.Slug)
		}

		dir, err := pg.SafeJoin(in.Slug)
		if err != nil {
			return nil, createPageOutput{}, fmt.Errorf("invalid_params: %w", err)
		}

		filePath := filepath.Join(dir, "index.md")
		content := buildFrontmatter(in.Title, in.Tags, in.Categories, in.Body)

		if err := atomicWrite(filePath, content); err != nil {
			return nil, createPageOutput{}, fmt.Errorf("write_error: %w", err)
		}

		return nil, createPageOutput{Slug: in.Slug, Path: filePath}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "update_page",
		Title:       "Update page",
		Description: "[RequiredScope: content.write] Update an existing Hugo content page. Preserves existing frontmatter fields.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: boolPtr(false),
			IdempotentHint:  false,
			OpenWorldHint:   boolPtr(false),
		},
	}, func(_ context.Context, _ *mcp.CallToolRequest, in updatePageInput) (*mcp.CallToolResult, updatePageOutput, error) {
		if in.Slug == "" {
			return nil, updatePageOutput{}, fmt.Errorf("invalid_params: slug must not be empty")
		}

		existing, ok := idx.GetBySlug(in.Slug)
		if !ok {
			return nil, updatePageOutput{}, fmt.Errorf("not_found: page not found for slug %q", in.Slug)
		}

		dir, err := pg.SafeJoin(in.Slug)
		if err != nil {
			return nil, updatePageOutput{}, fmt.Errorf("invalid_params: %w", err)
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
		if err := atomicWrite(filePath, content); err != nil {
			return nil, updatePageOutput{}, fmt.Errorf("write_error: %w", err)
		}

		return nil, updatePageOutput{Slug: in.Slug}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "delete_page",
		Title:       "Delete page",
		Description: "[RequiredScope: content.write] Delete a Hugo content page. Rate limited to 5 per minute.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: boolPtr(true),
			IdempotentHint:  false,
			OpenWorldHint:   boolPtr(false),
		},
	}, func(_ context.Context, _ *mcp.CallToolRequest, in deletePageInput) (*mcp.CallToolResult, deletePageOutput, error) {
		if !deleteLimiter.Allow() {
			return nil, deletePageOutput{}, fmt.Errorf("rate_limit_exceeded: delete_page is limited to 5 per minute")
		}

		if in.Slug == "" {
			return nil, deletePageOutput{}, fmt.Errorf("invalid_params: slug must not be empty")
		}

		dir, err := pg.SafeJoin(in.Slug)
		if err != nil {
			return nil, deletePageOutput{}, fmt.Errorf("invalid_params: %w", err)
		}
		filePath := filepath.Join(dir, "index.md")

		auditLog := filepath.Join(cfg.ContentRoot, ".mcp-audit.log")
		entry := fmt.Sprintf("%s DELETE %s\n", time.Now().UTC().Format(time.RFC3339), in.Slug)
		if err := appendAuditLog(auditLog, entry); err != nil {
			return nil, deletePageOutput{}, fmt.Errorf("audit_error: %w", err)
		}

		if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
			return nil, deletePageOutput{}, fmt.Errorf("delete_error: %w", err)
		}

		return nil, deletePageOutput{Slug: in.Slug}, nil
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

func atomicWrite(path, content string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
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

func boolPtr(v bool) *bool { return &v }

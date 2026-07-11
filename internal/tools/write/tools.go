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
	Slug        string   `json:"slug"`
	Lang        string   `json:"lang,omitempty"`
	Title       string   `json:"title,omitempty"`
	Body        string   `json:"body,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Categories  []string `json:"categories,omitempty"`
	Draft       *bool    `json:"draft,omitempty"`
	Description string   `json:"description,omitempty"`
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
		Name:  "update_page",
		Title: "Update page",
		Description: "Update an existing Hugo content page while preserving unspecified front matter fields. " +
			"Use title/body to revise content. Use tags/categories/draft/description to update front matter fields. " +
			"For bilingual sites, provide lang (e.g. \"fr\", \"en\") to target the correct language file; " +
			"omitting lang on a page with multiple language files returns an ambiguous_language error listing available langs.",
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

		filePath, langErr := resolveUpdateFilePath(dir, in.Slug, in.Lang)
		if langErr != nil {
			return nil, updatePageOutput{}, langErr
		}

		raw, err := os.ReadFile(filePath)
		if err != nil {
			slog.Error("update_page: read failed", "slug", in.Slug, "path", filePath, "error", err)
			return nil, updatePageOutput{}, fmt.Errorf("read_error: failed to read page")
		}
		opts := pageUpdateOpts{
			Tags:        in.Tags,
			Categories:  in.Categories,
			Draft:       in.Draft,
			Description: in.Description,
		}
		content, err := applyPageUpdates(string(raw), in.Title, in.Body, opts)
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
		if in.Tags != nil {
			updated.Tags = in.Tags
		}
		if in.Categories != nil {
			updated.Categories = in.Categories
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
		if cfg.SiteRoot != "" {
			publicPath := filepath.Join(cfg.SiteRoot, strings.Trim(in.Slug, "/"))
			if rmErr := os.RemoveAll(publicPath); rmErr != nil {
				slog.Warn("delete_page: could not remove public dir", "path", publicPath, "error", rmErr)
			}
		}

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

type pageUpdateOpts struct {
	Tags        []string
	Categories  []string
	Draft       *bool
	Description string
}

// applyPageUpdates applies title, body, and optional front matter field changes
// to an existing page file using the yaml.v3 Node API to preserve field
// ordering, comments, and YAML style (issue #111).
func applyPageUpdates(fileContent, newTitle, newBody string, opts pageUpdateOpts) (string, error) {
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

	needsYAML := newTitle != "" || opts.Tags != nil || opts.Categories != nil ||
		opts.Draft != nil || opts.Description != ""

	if needsYAML {
		var doc yaml.Node
		if err := yaml.Unmarshal([]byte(yamlPart), &doc); err != nil {
			return "", fmt.Errorf("YAML parse: %w", err)
		}
		if len(doc.Content) > 0 {
			mapping := doc.Content[0]
			if newTitle != "" {
				setYAMLKey(mapping, "title", newTitle)
			}
			if opts.Tags != nil {
				setYAMLSeq(mapping, "tags", opts.Tags)
			}
			if opts.Categories != nil {
				setYAMLSeq(mapping, "categories", opts.Categories)
			}
			if opts.Draft != nil {
				setYAMLBool(mapping, "draft", *opts.Draft)
			}
			if opts.Description != "" {
				setYAMLKey(mapping, "description", opts.Description)
			}
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

// setYAMLSeq sets a sequence (list) value in a YAML mapping node in-place,
// appending a new key-sequence pair when key is absent.
func setYAMLSeq(mapping *yaml.Node, key string, values []string) {
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, v := range values {
		seq.Content = append(seq.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v})
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content[i+1] = seq
			return
		}
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		seq,
	)
}

// setYAMLBool sets a boolean value in a YAML mapping node in-place,
// appending a new key-value pair when key is absent.
func setYAMLBool(mapping *yaml.Node, key string, value bool) {
	v := "false"
	if value {
		v = "true"
	}
	node := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: v}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content[i+1] = node
			return
		}
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		node,
	)
}

// resolveUpdateFilePath discovers the correct index file to update for a given
// content bundle directory. Returns ambiguous_language error when multiple
// language files exist and no lang is specified.
func resolveUpdateFilePath(dir, slug, lang string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		slog.Error("update_page: cannot read bundle dir", "slug", slug, "dir", dir, "error", err)
		return "", fmt.Errorf("read_error: failed to read content directory for slug %q", slug)
	}

	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if name == "index.md" {
			files = append(files, filepath.Join(dir, name))
			continue
		}
		if strings.HasPrefix(name, "index.") && strings.HasSuffix(name, ".md") {
			mid := strings.TrimSuffix(strings.TrimPrefix(name, "index."), ".md")
			if len(mid) >= 2 && len(mid) <= 8 && !strings.Contains(mid, ".") {
				files = append(files, filepath.Join(dir, name))
			}
		}
	}

	if len(files) == 0 {
		return "", fmt.Errorf("not_found: no index file found for slug %q", slug)
	}

	if lang != "" {
		target := filepath.Join(dir, "index."+lang+".md")
		for _, f := range files {
			if f == target {
				return f, nil
			}
		}
		return "", fmt.Errorf("not_found: no index.%s.md for slug %q; available: %s",
			lang, slug, strings.Join(bundleLangs(files, dir), ", "))
	}

	if len(files) == 1 {
		return files[0], nil
	}

	return "", fmt.Errorf("ambiguous_language: page %q has multiple language files; specify lang (available: %s)",
		slug, strings.Join(bundleLangs(files, dir), ", "))
}

// bundleLangs extracts language codes from a list of index file paths.
func bundleLangs(files []string, dir string) []string {
	langs := make([]string, 0, len(files))
	for _, f := range files {
		base := filepath.Base(f)
		if base == "index.md" {
			langs = append(langs, "default")
		} else {
			lang := strings.TrimSuffix(strings.TrimPrefix(base, "index."), ".md")
			langs = append(langs, lang)
		}
	}
	return langs
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

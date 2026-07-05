package hugosite

import (
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// ContentMu is a global read-write mutex that serializes all content mutations
// (create, update, delete) and site builds. Write operations must acquire the
// write lock; build operations must also acquire the write lock so that Hugo
// always sees a consistent snapshot. See issues #35 and #36.
var ContentMu sync.RWMutex

type SourcePage struct {
	Slug           string
	FilePath       string
	Title          string
	Date           string
	Draft          bool
	Tags           []string
	Categories     []string
	Body           string
	FrontmatterRaw map[string]any
}

type SourceIndex struct {
	pages  []SourcePage
	bySlug map[string]int
}

func NewSourceIndex(contentRoot string) (*SourceIndex, error) {
	info, err := os.Stat(contentRoot)
	if err != nil {
		return nil, fmt.Errorf("hugosite: content root: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("hugosite: content root is not a directory: %s", contentRoot)
	}

	var pages []SourcePage

	err = filepath.WalkDir(contentRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		rel, err := filepath.Rel(contentRoot, path)
		if err != nil {
			return nil
		}
		slug := SlugFromRel(rel)
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		fm, body := splitFrontmatter(raw)
		page := SourcePage{
			Slug:           slug,
			FilePath:       path,
			Title:          stringVal(fm["title"]),
			Date:           stringVal(fm["date"]),
			Draft:          boolVal(fm["draft"]),
			Tags:           stringSlice(fm["tags"]),
			Categories:     stringSlice(fm["categories"]),
			Body:           body,
			FrontmatterRaw: fm,
		}
		pages = append(pages, page)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("hugosite: walk: %w", err)
	}

	sort.Slice(pages, func(i, j int) bool {
		return pages[i].Slug < pages[j].Slug
	})

	bySlug := make(map[string]int, len(pages))
	for i, p := range pages {
		if prev, exists := bySlug[p.Slug]; exists {
			slog.Warn("hugosite source index: duplicate slug detected, last-write wins",
				"slug", p.Slug,
				"prev_index", prev,
				"current_index", i)
		}
		bySlug[p.Slug] = i
	}

	return &SourceIndex{pages: pages, bySlug: bySlug}, nil
}

func (idx *SourceIndex) GetBySlug(slug string) (*SourcePage, bool) {
	i, ok := idx.bySlug[slug]
	if !ok {
		return nil, false
	}
	p := idx.pages[i]
	return &p, true
}

func (idx *SourceIndex) AllSlugs() []string {
	slugs := make([]string, len(idx.pages))
	for i, p := range idx.pages {
		slugs[i] = p.Slug
	}
	return slugs
}

func (idx *SourceIndex) ListPages(limit, offset int) []SourcePage {
	total := len(idx.pages)
	if offset >= total {
		return []SourcePage{}
	}
	end := offset + limit
	if limit <= 0 || end > total {
		end = total
	}
	result := make([]SourcePage, end-offset)
	copy(result, idx.pages[offset:end])
	return result
}

func (idx *SourceIndex) AllTags() []string {
	if idx == nil {
		return nil
	}
	return uniqueSortedSourceStrings(func(p SourcePage) []string { return p.Tags }, idx.pages)
}

func (idx *SourceIndex) AllCategories() []string {
	if idx == nil {
		return nil
	}
	return uniqueSortedSourceStrings(func(p SourcePage) []string { return p.Categories }, idx.pages)
}

func uniqueSortedSourceStrings(values func(SourcePage) []string, pages []SourcePage) []string {
	seen := map[string]string{}
	for _, page := range pages {
		for _, raw := range values(page) {
			v := strings.TrimSpace(raw)
			if v == "" {
				continue
			}
			key := strings.ToLower(v)
			if _, ok := seen[key]; !ok {
				seen[key] = v
			}
		}
	}
	out := make([]string, 0, len(seen))
	for _, v := range seen {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// Upsert adds or replaces the index entry for page. It must be called while
// ContentMu is held for writing, so callers (create_page, update_page) must
// acquire the write lock before the filesystem write and index update.
func (idx *SourceIndex) Upsert(page SourcePage) {
	if i, ok := idx.bySlug[page.Slug]; ok {
		idx.pages[i] = page
		return
	}
	idx.pages = append(idx.pages, page)
	idx.bySlug[page.Slug] = len(idx.pages) - 1
}

// Delete removes the index entry for slug. It must be called while ContentMu
// is held for writing.
func (idx *SourceIndex) Delete(slug string) {
	i, ok := idx.bySlug[slug]
	if !ok {
		return
	}
	last := len(idx.pages) - 1
	if i != last {
		idx.pages[i] = idx.pages[last]
		idx.bySlug[idx.pages[i].Slug] = i
	}
	idx.pages = idx.pages[:last]
	delete(idx.bySlug, slug)
}

func SlugFromRel(rel string) string {
	rel = filepath.ToSlash(rel)
	// Standard branch bundle: posts/slug/index.md → posts/slug
	if strings.HasSuffix(rel, "/index.md") {
		return strings.TrimSuffix(rel, "/index.md")
	}
	// Multilingual branch bundle: posts/slug/index.en.md → posts/slug
	if i := strings.LastIndex(rel, "/index."); i >= 0 {
		after := rel[i+len("/index."):]
		// after is e.g. "en.md", "fr.md", "en-US.md"
		if strings.HasSuffix(after, ".md") {
			lang := strings.TrimSuffix(after, ".md")
			if len(lang) >= 2 && len(lang) <= 5 {
				return rel[:i]
			}
		}
	}
	return strings.TrimSuffix(rel, ".md")
}

func splitFrontmatter(raw []byte) (map[string]any, string) {
	content := string(raw)
	if !strings.HasPrefix(content, "---") {
		return map[string]any{}, content
	}
	parts := strings.SplitN(content, "---", 3)
	if len(parts) < 3 {
		return map[string]any{}, content
	}
	fm := map[string]any{}
	if err := yaml.NewDecoder(strings.NewReader(parts[1])).Decode(&fm); err != nil {
		fm = map[string]any{}
	}
	if fm == nil {
		fm = map[string]any{}
	}
	return fm, strings.TrimSpace(parts[2])
}

func stringVal(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case time.Time:
		return x.UTC().Format(time.RFC3339)
	case nil:
		return ""
	default:
		return fmt.Sprint(x)
	}
}

func boolVal(v any) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	return false
}

func stringSlice(v any) []string {
	switch x := v.(type) {
	case []string:
		if x == nil {
			return []string{}
		}
		return x
	case []any:
		out := make([]string, 0, len(x))
		for _, item := range x {
			out = append(out, stringVal(item))
		}
		return out
	default:
		return []string{}
	}
}

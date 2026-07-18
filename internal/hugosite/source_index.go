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

	"github.com/jmrGrav/mcp-hugo-server-go/internal/taxonomy"
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
	Lang           string
	Title          string
	Date           string
	Draft          bool
	PublishDate    time.Time // zero means no restriction
	ExpiryDate     time.Time // zero means no restriction
	Tags           []string
	Categories     []string
	Body           string
	FrontmatterRaw map[string]any
	BuildPending   bool
}

type SourceIndex struct {
	pages      []SourcePage
	bySlug     map[string]int
	bySlugLang map[string]int
	byDefault  map[string]int
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
			Lang:           langFromRel(rel),
			Title:          stringVal(fm["title"]),
			Date:           stringVal(fm["date"]),
			Draft:          boolVal(fm["draft"]),
			PublishDate:    timeVal(fm["publishDate"]),
			ExpiryDate:     timeVal(fm["expiryDate"]),
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

	idx := &SourceIndex{pages: pages}
	idx.rebuildMaps()
	return idx, nil
}

func (idx *SourceIndex) GetBySlug(slug string) (*SourcePage, bool) {
	i, ok := idx.bySlug[slug]
	if !ok {
		return nil, false
	}
	p := idx.pages[i]
	return &p, true
}

func (idx *SourceIndex) GetBySlugLang(slug, lang string) (*SourcePage, bool) {
	if idx == nil {
		return nil, false
	}
	i, ok := idx.bySlugLang[sourceLangKey(slug, lang)]
	if !ok {
		return nil, false
	}
	p := idx.pages[i]
	return &p, true
}

func (idx *SourceIndex) GetDefaultBySlug(slug string) (*SourcePage, bool) {
	if idx == nil {
		return nil, false
	}
	i, ok := idx.byDefault[slug]
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
	var all []string
	for _, page := range pages {
		all = append(all, values(page)...)
	}
	return taxonomy.DeduplicateRaw(all)
}

// Upsert adds or replaces the index entry for page. It must be called while
// ContentMu is held for writing, so callers (create_page, update_page) must
// acquire the write lock before the filesystem write and index update.
func (idx *SourceIndex) Upsert(page SourcePage) {
	if idx == nil {
		return
	}
	if page.Lang != "" {
		if i, ok := idx.bySlugLang[sourceLangKey(page.Slug, page.Lang)]; ok {
			idx.pages[i] = page
			idx.rebuildMaps()
			return
		}
	} else if i, ok := idx.byDefault[page.Slug]; ok {
		idx.pages[i] = page
		idx.rebuildMaps()
		return
	}
	idx.pages = append(idx.pages, page)
	idx.rebuildMaps()
}

// Delete removes the index entry for slug. It must be called while ContentMu
// is held for writing.
func (idx *SourceIndex) Delete(slug string) {
	if idx == nil || len(idx.pages) == 0 {
		return
	}
	filtered := idx.pages[:0]
	for _, page := range idx.pages {
		if page.Slug == slug {
			continue
		}
		filtered = append(filtered, page)
	}
	idx.pages = filtered
	idx.rebuildMaps()
}

func (idx *SourceIndex) ClearAllBuildPending() {
	if idx == nil {
		return
	}
	for i := range idx.pages {
		idx.pages[i].BuildPending = false
	}
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

func sourceLangKey(slug, lang string) string {
	return slug + "\x00" + lang
}

func (idx *SourceIndex) rebuildMaps() {
	idx.bySlug = make(map[string]int, len(idx.pages))
	idx.bySlugLang = make(map[string]int, len(idx.pages))
	idx.byDefault = make(map[string]int, len(idx.pages))
	for i, p := range idx.pages {
		idx.bySlug[p.Slug] = i
		if p.Lang != "" {
			key := sourceLangKey(p.Slug, p.Lang)
			if prev, exists := idx.bySlugLang[key]; exists {
				slog.Warn("hugosite source index: duplicate slug/lang detected, last-write wins",
					"slug", p.Slug, "lang", p.Lang, "prev_index", prev, "current_index", i)
			}
			idx.bySlugLang[key] = i
			continue
		}
		if prev, exists := idx.byDefault[p.Slug]; exists {
			slog.Warn("hugosite source index: duplicate default-language slug detected, last-write wins",
				"slug", p.Slug, "prev_index", prev, "current_index", i)
		}
		idx.byDefault[p.Slug] = i
	}
}

// langFromRel extracts the language code from a bundle-style filename
// ("index.<lang>.md", possibly nested — "posts/slug/index.en.md") or a Hugo
// section-index filename ("_index.<lang>.md", including at content root for
// the site's homepage — "_index.en.md"). Both prefixes are checked against
// the basename only, so this works regardless of directory depth.
func langFromRel(rel string) string {
	rel = filepath.ToSlash(rel)
	base := rel
	if i := strings.LastIndex(rel, "/"); i >= 0 {
		base = rel[i+1:]
	}
	var prefix string
	switch {
	case strings.HasPrefix(base, "index."):
		prefix = "index."
	case strings.HasPrefix(base, "_index."):
		prefix = "_index."
	default:
		return ""
	}
	after := strings.TrimPrefix(base, prefix)
	if strings.HasSuffix(after, ".md") {
		lang := strings.TrimSuffix(after, ".md")
		if len(lang) >= 2 && len(lang) <= 5 {
			return lang
		}
	}
	return ""
}

// ParseFrontmatterFile reads path and returns its front matter as a map,
// discarding the body. Exported for callers outside the content tree that
// need front matter from a Hugo-conventioned file — e.g. an archetypes/*.md
// template, which uses the identical --- delimited format as source pages
// but is never part of a SourceIndex.
func ParseFrontmatterFile(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	fm, _ := splitFrontmatter(raw)
	return fm, nil
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

func timeVal(v any) time.Time {
	switch x := v.(type) {
	case time.Time:
		return x
	case string:
		if x == "" {
			return time.Time{}
		}
		for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"} {
			if t, err := time.Parse(layout, x); err == nil {
				return t
			}
		}
	}
	return time.Time{}
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

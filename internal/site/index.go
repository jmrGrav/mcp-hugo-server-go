package site

import (
	"bytes"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"golang.org/x/net/html"
)

type entry struct {
	page       Page
	parsedDate time.Time
}

type Index struct {
	entries           []entry
	bySlug            map[string]int
	tags              []string
	categories        []string
	info              map[string]string
	contentClassifier *ContentClassifier
}

func NewIndex(cfg config.Config) (*Index, error) {
	root := strings.TrimSpace(cfg.SiteRoot)
	if root == "" {
		return &Index{
			bySlug: map[string]int{},
			info:   map[string]string{"name": cfg.SiteName, "url": cfg.SiteURL, "lang": cfg.DefaultLanguage},
		}, nil
	}

	canonicalRoot, err := canonicalDir(root)
	if err != nil {
		return nil, err
	}

	maxEntries := cfg.MaxIndexEntries
	if maxEntries <= 0 {
		maxEntries = 5000
	}
	defaultLang := cfg.DefaultLanguage
	if defaultLang == "" {
		defaultLang = "en"
	}

	idx := &Index{
		bySlug: map[string]int{},
		info:   map[string]string{"name": cfg.SiteName, "url": cfg.SiteURL, "lang": defaultLang},
	}

	tagSet := map[string]struct{}{}
	catSet := map[string]struct{}{}

	err = filepath.WalkDir(canonicalRoot, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if p == canonicalRoot {
			return nil
		}
		rel, err := filepath.Rel(canonicalRoot, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)

		if cfg.RejectHiddenPath && isHiddenPath(rel) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			if cfg.RejectSymlinks {
				return fmt.Errorf("symlink rejected: %s", rel)
			}
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}

		base := filepath.Base(rel)
		if !isHTMLFile(base) {
			return nil
		}
		if len(idx.entries) >= maxEntries {
			return fmt.Errorf("index entry limit exceeded: %d", maxEntries)
		}

		info, err := os.Stat(p)
		if err != nil {
			return err
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}

		pg, parsedDate, err := parseHTMLPage(raw, rel, info.ModTime(), cfg.SiteURL, defaultLang)
		if err != nil {
			return err
		}
		if pg.Slug == "" {
			return nil
		}
		if _, exists := idx.bySlug[pg.Slug]; exists {
			slog.Warn("site index: duplicate slug detected, skipping", "slug", pg.Slug, "path", p)
			return nil
		}

		idx.bySlug[pg.Slug] = len(idx.entries)
		idx.entries = append(idx.entries, entry{page: pg, parsedDate: parsedDate})

		for _, tag := range pg.Tags {
			tagSet[tag] = struct{}{}
		}
		for _, cat := range pg.Categories {
			catSet[cat] = struct{}{}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.SliceStable(idx.entries, func(i, j int) bool {
		di := idx.entries[i].parsedDate
		dj := idx.entries[j].parsedDate
		if di.Equal(dj) {
			return idx.entries[i].page.Slug < idx.entries[j].page.Slug
		}
		return di.After(dj)
	})

	idx.bySlug = make(map[string]int, len(idx.entries))
	for i := range idx.entries {
		idx.bySlug[idx.entries[i].page.Slug] = i
	}

	for tag := range tagSet {
		idx.tags = append(idx.tags, tag)
	}
	sort.Strings(idx.tags)

	for cat := range catSet {
		idx.categories = append(idx.categories, cat)
	}
	sort.Strings(idx.categories)

	idx.contentClassifier = NewClassifier(idx)

	return idx, nil
}

func (idx *Index) GetBySlug(slug string) (*Page, bool) {
	if idx == nil || slug == "" {
		return nil, false
	}
	norm := normalizeSlug(slug)
	pos, ok := idx.bySlug[norm]
	if !ok {
		return nil, false
	}
	p := idx.entries[pos].page
	return &p, true
}

func (idx *Index) Search(query string, limit int) []Page {
	if idx == nil {
		return nil
	}
	pages := idx.ContentPages()
	if limit <= 0 || limit > len(pages) {
		limit = len(pages)
	}
	terms := strings.Fields(strings.ToLower(query))
	type scored struct {
		page  Page
		score int
	}
	var results []scored
	for _, page := range pages {
		s := scoreEntry(page, terms)
		if len(terms) == 0 {
			s = 1
		}
		if s > 0 {
			results = append(results, scored{page: page, score: s})
		}
	}
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].score == results[j].score {
			return results[i].page.Date > results[j].page.Date
		}
		return results[i].score > results[j].score
	})
	if len(results) > limit {
		results = results[:limit]
	}
	out := make([]Page, len(results))
	for i, r := range results {
		out[i] = r.page
	}
	return out
}

func (idx *Index) RecentPosts(n int) []Page {
	if idx == nil {
		return nil
	}
	classifier := idx.classifier()
	var posts []Page
	for _, e := range idx.entries {
		if classifier.IsArticle(e.page) {
			posts = append(posts, e.page)
		}
	}
	if n > 0 && len(posts) > n {
		posts = posts[:n]
	}
	return posts
}

func (idx *Index) AllTags() []string {
	if idx == nil {
		return nil
	}
	return idx.tags
}

func (idx *Index) AllCategories() []string {
	if idx == nil {
		return nil
	}
	return idx.categories
}

func (idx *Index) Sitemap() []Page {
	if idx == nil {
		return nil
	}
	out := make([]Page, len(idx.entries))
	for i, e := range idx.entries {
		out[i] = e.page
	}
	return out
}

func (idx *Index) GetFeed(limit int) []Page {
	if idx == nil {
		return nil
	}
	out := idx.ContentPages()
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (idx *Index) SiteInfo() map[string]string {
	if idx == nil {
		return map[string]string{}
	}
	return idx.info
}

func parseHTMLPage(raw []byte, rel string, modTime time.Time, siteURL, defaultLang string) (Page, time.Time, error) {
	doc, err := html.Parse(bytes.NewReader(raw))
	if err != nil {
		return Page{}, time.Time{}, err
	}
	meta := collectMeta(doc)

	slug := meta.canonicalSlug
	if slug == "" {
		slug = slugFromRel(rel)
	}
	if slug == "" {
		return Page{}, time.Time{}, nil
	}

	parsedDate := firstNonZeroTime(meta.published, meta.modified, modTime)
	dateStr := ""
	if !parsedDate.IsZero() {
		dateStr = parsedDate.UTC().Format(time.RFC3339)
	}

	canonicalURL := meta.canonicalURL
	if canonicalURL == "" {
		canonicalURL = joinURL(siteURL, slug)
	}

	tags := uniqueStrs(meta.tags)
	cats := uniqueStrs(meta.categories)

	pg := Page{
		Slug:       slug,
		Title:      firstNonEmptyStr(meta.ogTitle, meta.title, slugTitleFallback(slug)),
		Summary:    firstNonEmptyStr(meta.ogDescription, meta.description),
		Tags:       tags,
		Categories: cats,
		Date:       dateStr,
		URL:        canonicalURL,
		Lang:       firstNonEmptyStr(meta.lang, defaultLang),
		RawHTML:    bodyHTML(raw),
	}
	return pg, parsedDate, nil
}

type htmlMeta struct {
	title         string
	description   string
	canonicalURL  string
	canonicalSlug string
	lang          string
	ogTitle       string
	ogDescription string
	section       string
	published     time.Time
	modified      time.Time
	tags          []string
	categories    []string
}

func collectMeta(doc *html.Node) htmlMeta {
	var meta htmlMeta
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n == nil {
			return
		}
		if n.Type == html.ElementNode {
			switch n.Data {
			case "html":
				if v := nodeAttr(n, "lang"); v != "" {
					meta.lang = v
				}
			case "title":
				if meta.title == "" {
					meta.title = strings.TrimSpace(textContent(n))
				}
			case "meta":
				name := strings.ToLower(nodeAttr(n, "name"))
				prop := strings.ToLower(nodeAttr(n, "property"))
				content := strings.TrimSpace(nodeAttr(n, "content"))
				switch {
				case name == "description" && meta.description == "":
					meta.description = content
				case prop == "og:title" && meta.ogTitle == "":
					meta.ogTitle = content
				case prop == "og:description" && meta.ogDescription == "":
					meta.ogDescription = content
				case prop == "article:section" && meta.section == "":
					meta.section = content
				case prop == "article:published_time" && meta.published.IsZero():
					meta.published = parseRFC3339(content)
				case prop == "article:modified_time" && meta.modified.IsZero():
					meta.modified = parseRFC3339(content)
				case prop == "article:tag" && content != "":
					meta.tags = append(meta.tags, content)
				case prop == "article:category" && content != "":
					meta.categories = append(meta.categories, content)
				case name == "keywords" && content != "":
					meta.categories = append(meta.categories, splitCSV(content)...)
				}
			case "link":
				rel := strings.ToLower(nodeAttr(n, "rel"))
				if strings.Contains(rel, "canonical") && meta.canonicalURL == "" {
					meta.canonicalURL = strings.TrimSpace(nodeAttr(n, "href"))
					meta.canonicalSlug = slugFromCanonical(meta.canonicalURL)
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return meta
}

func bodyHTML(raw []byte) string {
	doc, err := html.Parse(bytes.NewReader(raw))
	if err != nil {
		return string(raw)
	}
	body := findElement(doc, "body")
	if body == nil {
		return string(raw)
	}
	var buf bytes.Buffer
	for c := body.FirstChild; c != nil; c = c.NextSibling {
		_ = html.Render(&buf, c)
	}
	return buf.String()
}

func findElement(n *html.Node, name string) *html.Node {
	var out *html.Node
	var walk func(*html.Node)
	walk = func(cur *html.Node) {
		if cur == nil || out != nil {
			return
		}
		if cur.Type == html.ElementNode && cur.Data == name {
			out = cur
			return
		}
		for c := cur.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return out
}

func nodeAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return a.Val
		}
	}
	return ""
}

func textContent(n *html.Node) string {
	var buf strings.Builder
	var walk func(*html.Node)
	walk = func(cur *html.Node) {
		if cur == nil {
			return
		}
		if cur.Type == html.TextNode {
			buf.WriteString(cur.Data)
			buf.WriteByte(' ')
		}
		for c := cur.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.Join(strings.Fields(buf.String()), " ")
}

func parseRFC3339(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

func slugFromCanonical(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if i := strings.Index(raw, "://"); i >= 0 {
		raw = raw[i+3:]
		if j := strings.IndexByte(raw, '/'); j >= 0 {
			raw = raw[j:]
		} else {
			raw = "/"
		}
	}
	if raw == "" {
		return ""
	}
	if !strings.HasPrefix(raw, "/") {
		raw = "/" + raw
	}
	if decoded, err := url.PathUnescape(raw); err == nil {
		raw = decoded
	}
	return normalizeSlug(raw)
}

func slugFromRel(rel string) string {
	rel = filepath.ToSlash(rel)
	switch {
	case rel == "index.html":
		return "/"
	case strings.HasSuffix(rel, "/index.html"):
		return normalizeSlug("/" + strings.TrimSuffix(rel, "/index.html"))
	case strings.HasSuffix(rel, ".html"):
		return normalizeSlug("/" + strings.TrimSuffix(rel, ".html"))
	default:
		return normalizeSlug("/" + strings.TrimSuffix(rel, filepath.Ext(rel)))
	}
}

func normalizeSlug(raw string) string {
	if raw == "" {
		return "/"
	}
	clean := pathClean(raw)
	if clean == "." {
		return "/"
	}
	if !strings.HasPrefix(clean, "/") {
		clean = "/" + clean
	}
	if strings.HasSuffix(clean, "/index") {
		clean = strings.TrimSuffix(clean, "index")
	}
	if strings.HasSuffix(clean, "/index/") {
		clean = strings.TrimSuffix(clean, "index/")
	}
	if clean != "/" && !strings.HasSuffix(clean, "/") {
		clean = clean + "/"
	}
	return clean
}

func pathClean(raw string) string {
	if raw == "" {
		return "/"
	}
	raw = strings.ReplaceAll(raw, "//", "/")
	if !strings.HasPrefix(raw, "/") {
		raw = "/" + raw
	}
	clean := filepath.ToSlash(filepath.Clean(raw))
	if clean == "." {
		return "/"
	}
	return clean
}

func isHTMLFile(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".html", ".htm":
		return true
	}
	return false
}

func scoreEntry(p Page, terms []string) int {
	if len(terms) == 0 {
		return 1
	}
	fields := []string{
		strings.ToLower(p.Title),
		strings.ToLower(p.Summary),
		strings.ToLower(p.URL),
		strings.ToLower(strings.Join(p.Tags, " ")),
		strings.ToLower(strings.Join(p.Categories, " ")),
	}
	score := 0
	for _, term := range terms {
		for _, field := range fields {
			if strings.Contains(field, term) {
				score++
				break
			}
		}
	}
	return score
}

func joinURL(siteURL, slug string) string {
	siteURL = strings.TrimRight(strings.TrimSpace(siteURL), "/")
	slug = strings.TrimSpace(slug)
	if siteURL == "" {
		return slug
	}
	if slug == "" || slug == "/" {
		return siteURL + "/"
	}
	return siteURL + normalizeSlug(slug)
}

func firstNonEmptyStr(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func firstNonZeroTime(values ...time.Time) time.Time {
	for _, v := range values {
		if !v.IsZero() {
			return v
		}
	}
	return time.Time{}
}

func uniqueStrs(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, raw := range in {
		v := strings.TrimSpace(raw)
		if v == "" {
			continue
		}
		key := strings.ToLower(v)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, v)
	}
	return out
}

func splitCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func slugTitleFallback(slug string) string {
	slug = strings.TrimSuffix(strings.TrimSpace(slug), "/")
	if slug == "" || slug == "/" {
		return "Home"
	}
	parts := strings.Split(slug, "/")
	leaf := parts[len(parts)-1]
	if leaf == "" {
		return "Home"
	}
	leaf = strings.ReplaceAll(leaf, "-", " ")
	leaf = strings.ReplaceAll(leaf, "_", " ")
	return strings.ToUpper(leaf[:1]) + leaf[1:]
}

func canonicalDir(root string) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", fmt.Errorf("empty root")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("root is not a directory: %s", abs)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("root must not be a symlink: %s", abs)
	}
	return abs, nil
}

func isHiddenPath(rel string) bool {
	if rel == "" {
		return false
	}
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		if strings.HasPrefix(part, ".") {
			return true
		}
	}
	return false
}

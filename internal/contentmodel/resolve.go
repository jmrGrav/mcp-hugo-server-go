package contentmodel

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ResolvePageSource resolves slug/lang to a concrete Markdown source file under
// contentDir. It supports both leaf pages (`slug.md`, `slug.<lang>.md`) and
// bundle pages (`slug/index.md`, `slug/index.<lang>.md`).
func ResolvePageSource(slug, lang, contentDir string) (ResolvedSource, error) {
	rawSlug := slug
	slug = normalizeSlug(slug)
	if strings.TrimSpace(rawSlug) == "" {
		return ResolvedSource{}, fmt.Errorf("slug_not_found: slug must not be empty")
	}
	if slug == "" {
		return ResolvedSource{}, fmt.Errorf("invalid_slug: slug must stay under the content root")
	}
	lang = strings.TrimSpace(lang)

	if lang != "" {
		candidates := []candidate{
			{path: filepath.Join(contentDir, slug, "index."+lang+".md"), lang: lang},
			{path: filepath.Join(contentDir, slug+"."+lang+".md"), lang: lang},
		}
		for _, c := range candidates {
			if fileExists(c.path) {
				return ResolvedSource{Slug: slug, Lang: c.lang, SourcePath: c.path}, nil
			}
		}
		return ResolvedSource{}, fmt.Errorf("source_file_not_found: no source file found for slug %q and lang %q", slug, lang)
	}

	matches := collectCandidates(slug, contentDir)
	if len(matches) == 0 {
		return ResolvedSource{}, fmt.Errorf("source_file_not_found: no source file found for slug %q", slug)
	}
	if len(matches) > 1 {
		langs := uniqueNonEmptyLangs(matches)
		if len(langs) == 0 {
			langs = []string{"default"}
		}
		return ResolvedSource{}, fmt.Errorf("ambiguous_language: page %q has multiple language files; specify lang (available: %s)", slug, strings.Join(langs, ", "))
	}
	return ResolvedSource{Slug: slug, Lang: matches[0].lang, SourcePath: matches[0].path}, nil
}

type candidate struct {
	path string
	lang string
}

func collectCandidates(slug, contentDir string) []candidate {
	candidates := []candidate{
		{path: filepath.Join(contentDir, slug+".md")},
		{path: filepath.Join(contentDir, slug, "index.md")},
	}

	langGlobs := []string{
		filepath.Join(contentDir, slug+".*.md"),
		filepath.Join(contentDir, slug, "index.*.md"),
	}
	for _, pattern := range langGlobs {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			continue
		}
		sort.Strings(matches)
		for _, match := range matches {
			if strings.HasSuffix(match, "index.md") {
				continue
			}
			candidates = append(candidates, candidate{path: match, lang: extractLang(match)})
		}
	}

	var out []candidate
	seen := map[string]bool{}
	for _, c := range candidates {
		if !fileExists(c.path) || seen[c.path] {
			continue
		}
		seen[c.path] = true
		out = append(out, c)
	}
	return out
}

func uniqueNonEmptyLangs(candidates []candidate) []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range candidates {
		if c.lang == "" || seen[c.lang] {
			continue
		}
		seen[c.lang] = true
		out = append(out, c.lang)
	}
	sort.Strings(out)
	return out
}

func extractLang(path string) string {
	base := filepath.Base(path)
	switch {
	case strings.HasPrefix(base, "index.") && strings.HasSuffix(base, ".md"):
		return strings.TrimSuffix(strings.TrimPrefix(base, "index."), ".md")
	case strings.Count(base, ".") >= 2 && strings.HasSuffix(base, ".md"):
		parts := strings.Split(base, ".")
		return parts[len(parts)-2]
	default:
		return ""
	}
}

func normalizeSlug(slug string) string {
	slug = strings.Trim(strings.TrimSpace(slug), "/")
	if slug == "" {
		return ""
	}
	if strings.HasPrefix(slug, "/") {
		return ""
	}
	cleaned := filepath.Clean(slug)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return ""
	}
	parts := strings.Split(cleaned, string(filepath.Separator))
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return ""
		}
	}
	return cleaned
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

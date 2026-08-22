// Package assets owns scope-neutral generated-asset naming and path resolution.
package assets

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/security"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
)

// HeroImageSuffix is the filename suffix used for generated hero images.
const HeroImageSuffix = "-featured.jpg"

// HeroImageLocation is the canonical generated-hero-image location for one
// source slug under a Hugo root's static/images tree.
type HeroImageLocation struct {
	AbsPath     string
	LogicalPath string
	Name        string
}

// ResolveHeroImageLocation derives the generated hero-image path using a
// symlink-rejecting guard rooted at static/images.
func ResolveHeroImageLocation(hugoRoot, slug string) (HeroImageLocation, error) {
	imagesRoot := filepath.Join(hugoRoot, "static", "images")
	guard, err := security.New(imagesRoot, true)
	if err != nil {
		return HeroImageLocation{}, err
	}
	target, err := guard.SafeJoin(slug + HeroImageSuffix)
	if err != nil {
		return HeroImageLocation{}, err
	}
	return HeroImageLocation{
		AbsPath:     target,
		LogicalPath: logicalHugoRootPath(hugoRoot, target),
		Name:        filepath.Base(target),
	}, nil
}

// NormalizeHeroImageSlug returns the canonical source-key form accepted by
// generated hero-image operations.
func NormalizeHeroImageSlug(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if strings.HasPrefix(raw, "/") {
		if !strings.HasSuffix(raw, "/") {
			return "", fmt.Errorf("invalid_params: slug must be either a source key like %q or a canonical public slug like %q", "posts/example", "/posts/example/")
		}
		raw = strings.Trim(raw, "/")
	}
	candidates := site.SourceSlugCandidates(raw)
	if len(candidates) == 0 {
		return "", fmt.Errorf("invalid_params: slug must not be empty")
	}
	return candidates[len(candidates)-1], nil
}

func logicalHugoRootPath(hugoRoot, absPath string) string {
	hugoRoot = strings.TrimSpace(hugoRoot)
	absPath = strings.TrimSpace(absPath)
	if absPath == "" {
		return ""
	}
	if hugoRoot != "" {
		if rel, err := filepath.Rel(hugoRoot, absPath); err == nil && rel != "" && rel != "." && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
	}
	if filepath.IsAbs(absPath) {
		return ""
	}
	return filepath.ToSlash(absPath)
}

package read

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools/admin"
)

// ComputeTemplateFingerprint hashes every input that can change a page's
// rendered <head> output without the page's own content changing: the
// site's local layouts/ tree, the resolved theme's own layouts/ tree (a
// separate resolution root — see admin.ResolvedThemeLayoutDirs), the Hugo
// binary version, and the site's config file. Deliberately does NOT use the
// build's output_revision (a hash of the full rendered output): that
// changes on every single content edit too, which would defeat the whole
// point — invalidating the rendered-checks cache on every build regardless
// of whether a template actually changed, reproducing the exact cost #1151
// exists to avoid. Site-wide, not per-page: every page shares the same
// template, so this one value is compared once by the caller, never stored
// per row (#1151).
//
// Over-inclusion here is cheap (one extra full re-scan on a false
// invalidation); under-inclusion silently reproduces #1136's original bug
// one layer down (a template change nobody's fingerprint saw). When in
// doubt, this hashes more inputs, not fewer.
//
// The Hugo binary version is probed here directly (admin.HugoVersionString,
// the same probe get_runtime_status uses) rather than accepted as a
// parameter sourced from the build's own completion event: this is called
// from the same post-build callback pass that computes PostBuildSync's
// forceRenderedRecheck argument, which runs before the "OnBuildComplete"
// callback stage that would otherwise carry the version — see the
// PostBuildCallback doc comment in internal/tools/admin/build.go for that
// ordering. One extra bounded `hugo version` invocation per build is
// negligible next to the build itself.
func ComputeTemplateFingerprint(ctx context.Context, cfg config.Config) (string, error) {
	h := sha256.New()
	fmt.Fprintf(h, "hugo_version:%s\n", admin.HugoVersionString(ctx, cfg))

	if err := hashDirInto(h, filepath.Join(cfg.HugoRoot, "layouts")); err != nil {
		return "", err
	}
	for _, dir := range admin.ResolvedThemeLayoutDirs(ctx, cfg) {
		if err := hashDirInto(h, dir); err != nil {
			return "", err
		}
	}
	if err := hashConfigFileInto(h, cfg.HugoRoot); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// hashDirInto walks dir (if it exists — a missing dir, e.g. a site with no
// local layouts/ override, contributes nothing, not an error) and feeds
// every regular file's path (relative to dir, for stability across
// machines/checkouts) and content into h, in sorted order so the same tree
// always produces the same fingerprint regardless of directory read order.
func hashDirInto(h io.Writer, dir string) error {
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		return nil
	}
	var files []string
	walkErr := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if walkErr != nil {
		return walkErr
	}
	sort.Strings(files)
	for _, path := range files {
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			rel = path
		}
		fmt.Fprintf(h, "file:%s\n", filepath.ToSlash(rel))
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		h.Write(data)
		fmt.Fprint(h, "\n")
	}
	return nil
}

// hugoConfigFileNames covers every config filename/extension Hugo itself
// recognizes at the site root (the legacy config.* names and the current
// hugo.* names). Only the ones actually present contribute to the
// fingerprint; a themeless/config-file-less setup (defaults only) is valid
// and contributes nothing here, same as hashDirInto's missing-dir case.
var hugoConfigFileNames = []string{
	"hugo.toml", "hugo.yaml", "hugo.yml", "hugo.json",
	"config.toml", "config.yaml", "config.yml", "config.json",
}

func hashConfigFileInto(h io.Writer, hugoRoot string) error {
	for _, name := range hugoConfigFileNames {
		data, err := os.ReadFile(filepath.Join(hugoRoot, name))
		if err != nil {
			continue
		}
		fmt.Fprintf(h, "config:%s\n", name)
		h.Write(data)
		fmt.Fprint(h, "\n")
	}
	return nil
}

// RenderedIssueCount runs the exact same head-level/security checks
// inspect_rendered exposes per-page — render errors, missing images,
// missing title, missing canonical, missing meta description, hreflang
// problems, unsafe URLs, inline event handlers, and preview-token leakage,
// the full list #1136 asked get_site_health to aggregate — and returns how
// many are at "fail" status ("warn" is informational and not counted as an
// issue here). Reuses the exact check functions inspect_rendered calls
// live, reading the same on-disk rendered HTML via loadRenderedHTML, so a
// cached count can never disagree with a direct inspect_rendered call
// against the same build.
//
// internal_links is deliberately excluded: it is already surfaced by
// get_site_health's own broken_links_count (#1105), and counting it here
// too would make the same broken link move two different fields.
// featured_image is also excluded: it needs source frontmatter
// (site.ResolvedPage.Source), which a build-time pass over site.Page alone
// does not have without threading the source index through as well — left
// for a future extension, not part of #1136's original check list.
//
// ok is false when the page's rendered HTML could not be read or parsed at
// all; the caller should leave any previously cached count untouched rather
// than overwrite it with a misleading 0.
func RenderedIssueCount(cfg config.Config, idx *site.Index, page site.Page) (issues int, ok bool) {
	doc, raw, err := loadRenderedHTML(cfg, page)
	if err != nil {
		return 0, false
	}
	checks := []renderCheckResult{
		checkTitle(doc),
		checkMetaDescription(doc),
		checkCanonical(doc, cfg.SiteURL, page.Slug),
		checkHreflang(doc, idx, page),
		checkMissingImages(cfg, page, doc),
		checkRenderErrors(raw),
		checkRenderedInlineEventHandlers(doc),
		checkRenderedUnsafeURLs(doc),
		checkRenderedPreviewTokenLeak(raw),
	}
	for _, c := range checks {
		if c.Status == "fail" {
			issues++
		}
	}
	return issues, true
}

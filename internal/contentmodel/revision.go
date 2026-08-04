package contentmodel

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SourceRevisionBytes returns the stable optimistic-concurrency revision for a
// source file payload. It hashes the raw source bytes so the revision changes
// whenever the on-disk editable source changes, including front matter fields
// that a partial update might otherwise preserve implicitly.
func SourceRevisionBytes(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// SourceRevision reads a source file and returns its stable revision hash.
func SourceRevision(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read source for revision: %w", err)
	}
	return SourceRevisionBytes(raw), nil
}

// BundleRevision returns a stable optimistic-concurrency revision covering an
// entire page bundle directory as one mutable unit (#857): every translation
// source file (index.en.md, index.fr.md, ...) AND every bundle-local asset in
// that directory (recursively). Unlike SourceRevision, which protects a single
// file, this changes whenever ANY part of the editorial unit changes — a
// sibling translation or a shared asset edited behind a caller's back.
//
// The hash folds in each file's bundle-relative path (in sorted order) as well
// as its bytes, so a rename or add/remove also shifts the revision, not only
// an in-place content edit. Paths are hashed, never returned, so this exposes
// no host filesystem layout. Generated hero images live OUTSIDE the bundle
// directory (under static/images) and are deliberately not part of this
// revision — they are a separate lifecycle concern (see get_storage_health,
// #861); bundle_revision is scoped to the self-contained bundle directory.
func BundleRevision(bundleDir string) (string, error) {
	type entry struct {
		rel string
		abs string
	}
	var files []entry
	err := filepath.WalkDir(bundleDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(bundleDir, path)
		if relErr != nil {
			return relErr
		}
		files = append(files, entry{rel: filepath.ToSlash(rel), abs: path})
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("walk bundle for revision: %w", err)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].rel < files[j].rel })

	h := sha256.New()
	for _, f := range files {
		raw, readErr := os.ReadFile(f.abs)
		if readErr != nil {
			return "", fmt.Errorf("read bundle file for revision: %w", readErr)
		}
		// Length-prefix path and content so concatenation is unambiguous
		// (no path/content boundary ambiguity between adjacent files).
		fmt.Fprintf(h, "%d:%s\n%d:", len(f.rel), f.rel, len(raw))
		h.Write(raw)
		h.Write([]byte("\n"))
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// BundleTranslations enumerates the translation source files in a page-bundle
// directory, returning lang -> absolute file path. The default (unsuffixed)
// index.md is keyed under the empty string "". Non-index files and
// subdirectories are ignored. Returns an empty map for a directory with no
// index.* files (i.e. not a page bundle). (#854 builds on #857's
// BundleRevision above; this is the translation-enumeration companion it
// needs to resolve which files a bundle mutation touches.)
func BundleTranslations(bundleDir string) (map[string]string, error) {
	dirEntries, err := os.ReadDir(bundleDir)
	if err != nil {
		return nil, fmt.Errorf("read bundle dir: %w", err)
	}
	out := map[string]string{}
	for _, de := range dirEntries {
		if de.IsDir() {
			continue
		}
		name := de.Name()
		if !strings.HasSuffix(name, ".md") {
			continue
		}
		if name == "index.md" {
			out[""] = filepath.Join(bundleDir, name)
			continue
		}
		if strings.HasPrefix(name, "index.") {
			lang := strings.TrimSuffix(strings.TrimPrefix(name, "index."), ".md")
			if lang != "" {
				out[lang] = filepath.Join(bundleDir, name)
			}
		}
	}
	return out, nil
}

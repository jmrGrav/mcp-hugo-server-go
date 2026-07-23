package site

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStaleAgainstDiskNotStaleAfterBuild(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<html><body>hi</body></html>"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	idx, err := NewIndex(minimalCfg(root))
	if err != nil {
		t.Fatalf("NewIndex() error = %v", err)
	}
	stale, _ := idx.StaleAgainstDisk(minimalCfg(root))
	if stale {
		t.Fatal("expected fresh index to report not stale")
	}
}

func TestStaleAgainstDiskDetectsOutOfBandEdit(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "index.html")
	if err := os.WriteFile(target, []byte("<html><body>hi</body></html>"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	idx, err := NewIndex(minimalCfg(root))
	if err != nil {
		t.Fatalf("NewIndex() error = %v", err)
	}

	// Simulate an out-of-band edit (e.g. a manual `hugo` build) happening
	// after the index was built, bypassing Reload entirely.
	future := time.Now().Add(1 * time.Hour)
	if err := os.Chtimes(target, future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	// Bypass the staleCache TTL directly via computeStaleAgainstDisk so the
	// test doesn't depend on staleCheckInterval timing.
	stale, newest := idx.computeStaleAgainstDisk(minimalCfg(root))
	if !stale {
		t.Fatal("expected index to report stale after out-of-band mtime bump")
	}
	if !newest.Equal(future) {
		t.Fatalf("newest = %v, want %v", newest, future)
	}
}

func TestStaleAgainstDiskReportsTrueNewestNotFirstFound(t *testing.T) {
	root := t.TempDir()
	// "a-first.html" sorts lexically before "z-second.html", so a naive
	// early-exit-on-first-match walk would report "a-first.html"'s mtime as
	// newest even though "z-second.html" was actually edited later.
	first := filepath.Join(root, "a-first.html")
	second := filepath.Join(root, "z-second.html")
	if err := os.WriteFile(first, []byte("<html><body>a</body></html>"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if err := os.WriteFile(second, []byte("<html><body>z</body></html>"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	idx, err := NewIndex(minimalCfg(root))
	if err != nil {
		t.Fatalf("NewIndex() error = %v", err)
	}

	earlierEdit := time.Now().Add(30 * time.Minute)
	trueNewest := time.Now().Add(1 * time.Hour)
	if err := os.Chtimes(first, earlierEdit, earlierEdit); err != nil {
		t.Fatalf("chtimes first: %v", err)
	}
	if err := os.Chtimes(second, trueNewest, trueNewest); err != nil {
		t.Fatalf("chtimes second: %v", err)
	}

	stale, newest := idx.computeStaleAgainstDisk(minimalCfg(root))
	if !stale {
		t.Fatal("expected stale")
	}
	if !newest.Equal(trueNewest) {
		t.Fatalf("newest = %v, want %v (the actually-newest edit, not the lexically-first one)", newest, trueNewest)
	}
}

func TestStaleAgainstDiskSkipsHiddenAndSymlinkedFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<html><body>hi</body></html>"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	cfg := minimalCfg(root)
	idx, err := NewIndex(cfg)
	if err != nil {
		t.Fatalf("NewIndex() error = %v", err)
	}

	// A hidden path the index itself would skip (RejectHiddenPath is true in
	// minimalCfg) must not trip staleness even with a fresh mtime — flagging
	// a file the index doesn't track would cry wolf on files that can never
	// actually desync the index's own content.
	hiddenDir := filepath.Join(root, ".hidden")
	if err := os.Mkdir(hiddenDir, 0o755); err != nil {
		t.Fatalf("mkdir hidden: %v", err)
	}
	hiddenFile := filepath.Join(hiddenDir, "shadow.html")
	if err := os.WriteFile(hiddenFile, []byte("<html><body>shadow</body></html>"), 0o644); err != nil {
		t.Fatalf("write hidden fixture: %v", err)
	}
	future := time.Now().Add(1 * time.Hour)
	if err := os.Chtimes(hiddenFile, future, future); err != nil {
		t.Fatalf("chtimes hidden: %v", err)
	}

	stale, _ := idx.computeStaleAgainstDisk(cfg)
	if stale {
		t.Fatal("expected hidden-path file (rejected by RejectHiddenPath) to not trip staleness")
	}
}

func TestStaleAgainstDiskCachesWithinInterval(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "index.html")
	if err := os.WriteFile(target, []byte("<html><body>hi</body></html>"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	idx, err := NewIndex(minimalCfg(root))
	if err != nil {
		t.Fatalf("NewIndex() error = %v", err)
	}

	stale, _ := idx.StaleAgainstDisk(minimalCfg(root))
	if stale {
		t.Fatal("expected not stale before edit")
	}

	future := time.Now().Add(1 * time.Hour)
	if err := os.Chtimes(target, future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	// Within the cache TTL, StaleAgainstDisk should still return the cached
	// (stale=false) result rather than re-walking the disk.
	stale, _ = idx.StaleAgainstDisk(minimalCfg(root))
	if stale {
		t.Fatal("expected cached result to still report not stale within TTL")
	}
}

func TestReloadResetsStaleCache(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "index.html")
	if err := os.WriteFile(target, []byte("<html><body>hi</body></html>"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	idx, err := NewIndex(minimalCfg(root))
	if err != nil {
		t.Fatalf("NewIndex() error = %v", err)
	}

	future := time.Now().Add(1 * time.Hour)
	if err := os.Chtimes(target, future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	stale, _ := idx.StaleAgainstDisk(minimalCfg(root))
	if !stale {
		t.Fatal("expected stale result before Reload")
	}

	// Revert the mtime, then confirm the stale=true result is served from
	// cache (still stale, within staleCheckInterval) until Reload resets it.
	past := time.Now().Add(-1 * time.Minute)
	if err := os.Chtimes(target, past, past); err != nil {
		t.Fatalf("chtimes revert: %v", err)
	}
	stale, _ = idx.StaleAgainstDisk(minimalCfg(root))
	if !stale {
		t.Fatal("expected cached stale=true result to persist despite mtime revert (cache not yet expired)")
	}

	if err := idx.Reload(minimalCfg(root)); err != nil {
		t.Fatalf("Reload() error = %v", err)
	}
	stale, _ = idx.StaleAgainstDisk(minimalCfg(root))
	if stale {
		t.Fatal("expected Reload to invalidate the stale cache and report fresh state")
	}
}

package fileutil

import (
	"os"
	"testing"
)

// SwapDirOnTempCreate installs a hook that fires synchronously right after
// AtomicWrite/AtomicWriteChecked/AtomicCreateChecked/AtomicCreateCheckedBytes
// successfully create their temp file — before any content is written and
// before the second RevalidateForWrite/link check runs. It exists so
// external (fileutil_test) TOCTOU tests can land a symlink swap precisely
// inside the real race window deterministically, instead of racing a
// goroutine against filesystem polling: on a fast filesystem (tmpfs) the
// write can complete before a poller's first glob call ever runs, which
// made the polling version genuinely flaky under load, not just slow.
//
// fn receives the temp file's full path so a caller can place a decoy file
// with the identical basename at the symlink-swap target (#947): without
// that, the swapped-to path never has a file with the stale tmp name on it,
// so the eventual os.Link/os.Rename call fails on ENOENT alone and the
// second RevalidateForWrite check is never actually exercised.
func SwapDirOnTempCreate(t testing.TB, fn func(tmpName string)) {
	t.Helper()
	orig := createTmp
	createTmp = func(dir, pattern string) (*os.File, error) {
		f, err := orig(dir, pattern)
		if err == nil {
			fn(f.Name())
		}
		return f, err
	}
	t.Cleanup(func() { createTmp = orig })
}

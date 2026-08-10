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
func SwapDirOnTempCreate(t testing.TB, fn func()) {
	t.Helper()
	orig := createTmp
	createTmp = func(dir, pattern string) (*os.File, error) {
		f, err := orig(dir, pattern)
		if err == nil {
			fn()
		}
		return f, err
	}
	t.Cleanup(func() { createTmp = orig })
}

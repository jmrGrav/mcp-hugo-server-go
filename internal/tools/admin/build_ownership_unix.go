//go:build !windows

package admin

import (
	"os"
	"syscall"
)

// ownershipMismatch reports whether dir's owning uid differs from euid.
// chtimes on pre-existing files requires the process to own them (POSIX);
// a directory owned by a different uid means its existing files will
// trigger EPERM later, so checkBuildWritable surfaces that up front as a
// clear preflight error instead of a confusing mid-build failure.
func ownershipMismatch(fi os.FileInfo, euid int) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) != euid
}

//go:build windows

package admin

import "os"

// ownershipMismatch is always false on Windows: there's no POSIX uid to
// compare, and Windows' own ACL model isn't equivalent enough to fake one
// safely. The write-access preflight in checkBuildWritable (the actual
// CreateTemp probe) still runs on every platform and is what catches real
// permission failures — this ownership check was always a supplementary
// POSIX-only diagnostic on top of that, not the only safety net.
func ownershipMismatch(fi os.FileInfo, euid int) bool {
	return false
}

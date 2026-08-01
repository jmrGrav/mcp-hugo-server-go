//go:build windows

package admin

import (
	"os/exec"
	"strconv"
)

// setNewProcessGroup is a no-op on Windows: POSIX process groups (setpgid)
// don't exist there. killProcessGroup below uses taskkill /T instead, which
// terminates the whole process tree without needing one pre-arranged.
func setNewProcessGroup(cmd *exec.Cmd) {}

// killProcessGroup shells out to taskkill /F /T, which terminates the
// target process and its full descendant tree — the Windows equivalent of
// killing a POSIX process group, used for the same reason: a timed-out hugo
// build must not leave orphaned child processes behind (#240/#243).
func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
}

//go:build !windows

package admin

import (
	"os/exec"
	"syscall"
)

// setNewProcessGroup puts cmd in its own process group so killProcessGroup
// can later terminate it and every child process it spawned (e.g. hugo
// invoking a shortcode's own subprocess) in one signal, rather than leaving
// orphans behind when a build times out (#240/#243).
func setNewProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessGroup sends SIGKILL to the negative PID, which POSIX treats as
// "every process in this process group" — this is why setNewProcessGroup
// must have been called first.
func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}

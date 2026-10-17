//go:build !windows

package opencodex

import (
	"errors"
	"os/exec"
	"syscall"
)

// detach puts OpenCodex in its own process group, so a Ctrl+C in the
// terminal running MomentForge does not stop it too.
func detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

func alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

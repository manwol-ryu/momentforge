//go:build !windows

package cliproxy

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// detach puts the program in its own process group, so a Ctrl+C in the
// terminal running MomentForge does not stop it too.
func detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

func alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// runs reports whether the process's executable has one of these names, so
// a pid file left from an old run never stops another program.
func runs(pid int, names []string) bool {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "comm=").Output()
	if err != nil {
		return false
	}
	base := filepath.Base(strings.TrimSpace(string(out)))
	for _, n := range names {
		if base == n {
			return true
		}
	}
	return false
}

func terminate(pid int) error { return syscall.Kill(pid, syscall.SIGTERM) }

//go:build windows

package cliproxy

import (
	"os"
	"os/exec"
)

func detach(*exec.Cmd) {}

func alive(pid int) bool {
	p, err := os.FindProcess(pid) // fails when there is no such process
	if err != nil {
		return false
	}
	p.Release()
	return true
}

// runs cannot check a process's program here, so only the CLIProxyAPI this
// run of MomentForge started can be stopped.
func runs(int, []string) bool { return false }

func terminate(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Kill()
}

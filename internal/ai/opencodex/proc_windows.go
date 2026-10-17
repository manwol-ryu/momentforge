//go:build windows

package opencodex

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

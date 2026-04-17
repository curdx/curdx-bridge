//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

// Windows creation flags. Defined here so we don't pull in golang.org/x/sys.
const (
	createNewProcessGroup = 0x00000200
	detachedProcess       = 0x00000008
)

// setSysProcAttr detaches the child from the parent's console and process
// group so it survives parent exit — the Windows analogue of Setsid on Unix.
func setSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNewProcessGroup | detachedProcess,
	}
}

//go:build windows

package terminal

import (
	"os/exec"
	"syscall"
)

// setSysProcAttr sets CREATE_NO_WINDOW on Windows to prevent visible CMD windows.
func setSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW
	}
}

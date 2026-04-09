//go:build !windows

package askcli

import (
	"os"
	"os/exec"
	"syscall"
)

func startDetachedProcess(entry string) bool {
	cmd := exec.Command(entry)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}
	// Close fds
	cmd.Env = os.Environ()
	if err := cmd.Start(); err != nil {
		return false
	}
	// Detach: don't wait
	go cmd.Wait()
	return true
}

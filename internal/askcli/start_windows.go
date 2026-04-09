//go:build windows

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
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}
	cmd.Env = os.Environ()
	if err := cmd.Start(); err != nil {
		return false
	}
	go cmd.Wait()
	return true
}

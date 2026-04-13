//go:build !windows

package server

import (
	"errors"
	"os"
	"syscall"
)

// isPIDAlive checks whether the given PID is still running using signal 0.
func isPIDAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = process.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	// EPERM means the process exists but we lack permission to signal it —
	// it is still alive.
	return errors.Is(err, syscall.EPERM)
}

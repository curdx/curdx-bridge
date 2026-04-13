//go:build !windows

package main

import (
	"errors"
	"os"
	"syscall"
)

func isPIDRunning(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	if errors.Is(err, syscall.EPERM) {
		return true
	}
	return err == nil
}

//go:build !windows

package main

import "syscall"

func isProcessAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

func terminateProcess(pid int) error {
	return syscall.Kill(pid, syscall.SIGTERM)
}

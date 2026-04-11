//go:build windows

package main

import (
	"fmt"
	"syscall"
)

var (
	modkernel32          = syscall.NewLazyDLL("kernel32.dll")
	procOpenProcess      = modkernel32.NewProc("OpenProcess")
	procTerminateProcess = modkernel32.NewProc("TerminateProcess")
)

const (
	processQueryLimited = 0x1000
	processTerminate    = 0x0001
)

func isProcessAlive(pid int) bool {
	h, _, _ := procOpenProcess.Call(processQueryLimited, 0, uintptr(pid))
	if h == 0 {
		return false
	}
	syscall.CloseHandle(syscall.Handle(h))
	return true
}

func terminateProcess(pid int) error {
	h, _, err := procOpenProcess.Call(processTerminate, 0, uintptr(pid))
	if h == 0 {
		return fmt.Errorf("OpenProcess failed for PID %d: %w", pid, err)
	}
	handle := syscall.Handle(h)
	defer syscall.CloseHandle(handle)

	r, _, err := procTerminateProcess.Call(uintptr(handle), 1)
	if r == 0 {
		return fmt.Errorf("TerminateProcess failed for PID %d: %w", pid, err)
	}
	return nil
}

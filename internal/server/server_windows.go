//go:build windows

package server

import (
	"syscall"
	"unsafe"
)

var (
	modkernel32     = syscall.NewLazyDLL("kernel32.dll")
	procOpenProcess = modkernel32.NewProc("OpenProcess")
)

const processQueryLimited = 0x1000

// isPIDAlive checks whether the given PID is still running using OpenProcess.
func isPIDAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, _, _ := procOpenProcess.Call(processQueryLimited, 0, uintptr(pid))
	if h == 0 {
		return false
	}
	syscall.CloseHandle(syscall.Handle(unsafe.Pointer(h)))
	return true
}

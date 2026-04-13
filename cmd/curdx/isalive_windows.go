//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

var (
	modkernel32     = syscall.NewLazyDLL("kernel32.dll")
	procOpenProcess = modkernel32.NewProc("OpenProcess")
)

const processQueryLimited = 0x1000

func isPIDRunning(pid int) bool {
	h, _, _ := procOpenProcess.Call(processQueryLimited, 0, uintptr(pid))
	if h == 0 {
		return false
	}
	syscall.CloseHandle(syscall.Handle(unsafe.Pointer(h)))
	return true
}

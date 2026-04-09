//go:build windows

package processlock

import (
	"os"
	"syscall"
	"unsafe"
)

var (
	modkernel32      = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx   = modkernel32.NewProc("LockFileEx")
	procUnlockFileEx = modkernel32.NewProc("UnlockFileEx")
	procOpenProcess  = modkernel32.NewProc("OpenProcess")
)

const (
	lockfileExclusiveLock   = 0x02
	lockfileFailImmediately = 0x01
	processQueryLimited     = 0x1000
)

// isPIDAlive checks if a process with the given PID is still running.
func isPIDAlive(pid int) bool {
	h, _, _ := procOpenProcess.Call(processQueryLimited, 0, uintptr(pid))
	if h == 0 {
		return false
	}
	syscall.CloseHandle(syscall.Handle(h))
	return true
}

// lockFile acquires an exclusive non-blocking lock on f using LockFileEx.
func lockFile(f *os.File) bool {
	var overlapped syscall.Overlapped
	r, _, _ := procLockFileEx.Call(
		f.Fd(),
		lockfileExclusiveLock|lockfileFailImmediately,
		0,
		1, 0,
		uintptr(unsafe.Pointer(&overlapped)),
	)
	return r != 0
}

// unlockFile releases the lock on f using UnlockFileEx.
func unlockFile(f *os.File) {
	var overlapped syscall.Overlapped
	procUnlockFileEx.Call(
		f.Fd(),
		0,
		1, 0,
		uintptr(unsafe.Pointer(&overlapped)),
	)
}

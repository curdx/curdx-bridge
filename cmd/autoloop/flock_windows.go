//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

var (
	modkernel32      = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx   = modkernel32.NewProc("LockFileEx")
	procUnlockFileEx = modkernel32.NewProc("UnlockFileEx")
)

const (
	lockfileExclusiveLock   = 0x02
	lockfileFailImmediately = 0x01
)

type fileLock struct {
	f    *os.File
	path string
}

func acquireLock(lockPath string) (*fileLock, error) {
	dir := filepath.Dir(lockPath)
	_ = os.MkdirAll(dir, 0o755)

	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o666)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}

	var overlapped syscall.Overlapped
	r, _, _ := procLockFileEx.Call(
		f.Fd(),
		lockfileExclusiveLock|lockfileFailImmediately,
		0,
		1, 0,
		uintptr(unsafe.Pointer(&overlapped)),
	)
	if r == 0 {
		f.Close()
		return nil, fmt.Errorf("lock held by another process")
	}

	// Write PID
	_ = f.Truncate(0)
	_, _ = f.Seek(0, 0)
	fmt.Fprintf(f, "%d", os.Getpid())

	return &fileLock{f: f, path: lockPath}, nil
}

func (fl *fileLock) release() {
	if fl == nil || fl.f == nil {
		return
	}
	var overlapped syscall.Overlapped
	procUnlockFileEx.Call(
		fl.f.Fd(),
		0,
		1, 0,
		uintptr(unsafe.Pointer(&overlapped)),
	)
	fl.f.Close()
	fl.f = nil
}

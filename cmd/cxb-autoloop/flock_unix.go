//go:build !windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

type fileLock struct {
	fd   int
	path string
}

func acquireLock(lockPath string) (*fileLock, error) {
	dir := filepath.Dir(lockPath)
	_ = os.MkdirAll(dir, 0o755)

	fd, err := syscall.Open(lockPath, os.O_CREATE|os.O_RDWR, 0o666)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		syscall.Close(fd)
		return nil, fmt.Errorf("lock held by another process")
	}
	// Write PID
	pidBytes := fmt.Appendf(nil, "%d", os.Getpid())
	_, _ = syscall.Seek(fd, 0, 0)
	_, _ = syscall.Write(fd, pidBytes)
	_ = syscall.Ftruncate(fd, int64(len(pidBytes)))

	return &fileLock{fd: fd, path: lockPath}, nil
}

func (fl *fileLock) release() {
	if fl == nil || fl.fd < 0 {
		return
	}
	_ = syscall.Flock(fl.fd, syscall.LOCK_UN)
	syscall.Close(fl.fd)
	fl.fd = -1
}

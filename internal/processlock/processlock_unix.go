//go:build !windows

package processlock

import (
	"os"
	"syscall"
)

// isPIDAlive checks if a process with the given PID is still running.
func isPIDAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// lockFile acquires an exclusive non-blocking flock on f.
func lockFile(f *os.File) bool {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil
}

// unlockFile releases the flock on f.
func unlockFile(f *os.File) {
	syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}

// Package processlock provides per-provider, per-directory file locking.
// Source: claude_code_bridge/lib/process_lock.py
package processlock

import (
	"crypto/md5"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// isPIDAlive checks if a process with the given PID is still running (Unix).
func isPIDAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On Unix, FindProcess always succeeds. Use signal 0 to check liveness.
	err = proc.Signal(syscall.Signal(0))
	return err == nil
}

// ProviderLock is a per-provider, per-directory file lock to serialize
// request-response cycles.
//
// Lock files are stored in ~/.ccb/run/{provider}-{cwd_hash}.lock
type ProviderLock struct {
	Provider string
	Timeout  float64
	LockDir  string
	LockFile string
	fd       int
	acquired bool
}

// NewProviderLock creates a new ProviderLock for the given provider and working directory.
func NewProviderLock(provider string, timeout float64, cwd string) *ProviderLock {
	if cwd == "" {
		cwd, _ = os.Getwd()
	}

	home, _ := os.UserHomeDir()
	lockDir := filepath.Join(home, ".ccb", "run")

	h := md5.Sum([]byte(cwd))
	cwdHash := fmt.Sprintf("%x", h)[:8]
	lockFile := filepath.Join(lockDir, fmt.Sprintf("%s-%s.lock", provider, cwdHash))

	return &ProviderLock{
		Provider: provider,
		Timeout:  timeout,
		LockDir:  lockDir,
		LockFile: lockFile,
		fd:       -1,
		acquired: false,
	}
}

// tryAcquireOnce attempts to acquire the lock once without blocking.
func (pl *ProviderLock) tryAcquireOnce() bool {
	err := syscall.Flock(pl.fd, syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		return false
	}

	// Write PID for debugging and stale lock detection
	pidBytes := []byte(fmt.Sprintf("%d\n", os.Getpid()))
	syscall.Seek(pl.fd, 0, 0)
	syscall.Write(pl.fd, pidBytes)
	syscall.Ftruncate(pl.fd, int64(len(pidBytes)))
	pl.acquired = true
	return true
}

// checkStaleLock checks if the current lock holder is dead, allowing us to take over.
func (pl *ProviderLock) checkStaleLock() bool {
	data, err := os.ReadFile(pl.LockFile)
	if err != nil {
		return false
	}
	content := strings.TrimSpace(string(data))
	if content == "" {
		return false
	}
	pid, err := strconv.Atoi(content)
	if err != nil {
		return false
	}
	if !isPIDAlive(pid) {
		// Stale lock - remove it
		os.Remove(pl.LockFile)
		return true
	}
	return false
}

// TryAcquire tries to acquire the lock without blocking. Returns immediately.
func (pl *ProviderLock) TryAcquire() bool {
	os.MkdirAll(pl.LockDir, 0o755)

	fd, err := syscall.Open(pl.LockFile, os.O_CREATE|os.O_RDWR, 0o666)
	if err != nil {
		return false
	}
	pl.fd = fd

	if pl.tryAcquireOnce() {
		return true
	}

	// Check for stale lock
	if pl.checkStaleLock() {
		syscall.Close(pl.fd)
		fd, err = syscall.Open(pl.LockFile, os.O_CREATE|os.O_RDWR, 0o666)
		if err != nil {
			pl.fd = -1
			return false
		}
		pl.fd = fd
		if pl.tryAcquireOnce() {
			return true
		}
	}

	// Failed - close fd
	syscall.Close(pl.fd)
	pl.fd = -1
	return false
}

// Acquire acquires the lock, waiting up to Timeout seconds.
func (pl *ProviderLock) Acquire() bool {
	os.MkdirAll(pl.LockDir, 0o755)

	fd, err := syscall.Open(pl.LockFile, os.O_CREATE|os.O_RDWR, 0o666)
	if err != nil {
		return false
	}
	pl.fd = fd

	deadline := time.Now().Add(time.Duration(pl.Timeout * float64(time.Second)))
	staleChecked := false

	for time.Now().Before(deadline) {
		if pl.tryAcquireOnce() {
			return true
		}

		// Check for stale lock once after first failure
		if !staleChecked {
			staleChecked = true
			if pl.checkStaleLock() {
				// Lock file was stale, reopen and retry
				syscall.Close(pl.fd)
				fd, err = syscall.Open(pl.LockFile, os.O_CREATE|os.O_RDWR, 0o666)
				if err != nil {
					pl.fd = -1
					return false
				}
				pl.fd = fd
				if pl.tryAcquireOnce() {
					return true
				}
			}
		}

		time.Sleep(100 * time.Millisecond)
	}

	// Timeout - close fd
	if pl.fd >= 0 {
		syscall.Close(pl.fd)
		pl.fd = -1
	}
	return false
}

// Release releases the lock.
func (pl *ProviderLock) Release() {
	if pl.fd < 0 {
		return
	}
	if pl.acquired {
		syscall.Flock(pl.fd, syscall.LOCK_UN)
	}
	syscall.Close(pl.fd)
	pl.fd = -1
	pl.acquired = false
}

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
	"time"
)

// ProviderLock is a per-provider, per-directory file lock to serialize
// request-response cycles.
//
// Lock files are stored in ~/.ccb/run/{provider}-{cwd_hash}.lock
type ProviderLock struct {
	Provider string
	Timeout  float64
	LockDir  string
	LockFile string
	file     *os.File
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
		file:     nil,
		acquired: false,
	}
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
		os.Remove(pl.LockFile)
		return true
	}
	return false
}

// writePID writes the current PID into the lock file.
func (pl *ProviderLock) writePID() {
	if pl.file == nil {
		return
	}
	pl.file.Seek(0, 0)
	pl.file.Truncate(0)
	fmt.Fprintf(pl.file, "%d\n", os.Getpid())
}

// TryAcquire tries to acquire the lock without blocking. Returns immediately.
func (pl *ProviderLock) TryAcquire() bool {
	os.MkdirAll(pl.LockDir, 0o755)

	f, err := os.OpenFile(pl.LockFile, os.O_CREATE|os.O_RDWR, 0o666)
	if err != nil {
		return false
	}
	pl.file = f

	if lockFile(f) {
		pl.writePID()
		pl.acquired = true
		return true
	}

	// Check for stale lock
	if pl.checkStaleLock() {
		pl.file.Close()
		f, err = os.OpenFile(pl.LockFile, os.O_CREATE|os.O_RDWR, 0o666)
		if err != nil {
			pl.file = nil
			return false
		}
		pl.file = f
		if lockFile(f) {
			pl.writePID()
			pl.acquired = true
			return true
		}
	}

	pl.file.Close()
	pl.file = nil
	return false
}

// Acquire acquires the lock, waiting up to Timeout seconds.
func (pl *ProviderLock) Acquire() bool {
	os.MkdirAll(pl.LockDir, 0o755)

	f, err := os.OpenFile(pl.LockFile, os.O_CREATE|os.O_RDWR, 0o666)
	if err != nil {
		return false
	}
	pl.file = f

	deadline := time.Now().Add(time.Duration(pl.Timeout * float64(time.Second)))
	staleChecked := false

	for time.Now().Before(deadline) {
		if lockFile(f) {
			pl.writePID()
			pl.acquired = true
			return true
		}

		if !staleChecked {
			staleChecked = true
			if pl.checkStaleLock() {
				pl.file.Close()
				f, err = os.OpenFile(pl.LockFile, os.O_CREATE|os.O_RDWR, 0o666)
				if err != nil {
					pl.file = nil
					return false
				}
				pl.file = f
				if lockFile(f) {
					pl.writePID()
					pl.acquired = true
					return true
				}
			}
		}

		time.Sleep(100 * time.Millisecond)
	}

	if pl.file != nil {
		pl.file.Close()
		pl.file = nil
	}
	return false
}

// Release releases the lock.
func (pl *ProviderLock) Release() {
	if pl.file == nil {
		return
	}
	if pl.acquired {
		unlockFile(pl.file)
	}
	pl.file.Close()
	pl.file = nil
	pl.acquired = false
}

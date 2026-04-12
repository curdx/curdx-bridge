//go:build !windows

package processlock

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

// execCommand runs a command and returns its combined output.
func execCommand(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}

// isPIDAlive checks if a process with the given PID is still running.
func isPIDAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// isProcessStuck checks if a process is in Stopped (T) or Zombie (Z) state.
// These processes hold file locks but will never release them voluntarily.
func isProcessStuck(pid int) bool {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err == nil {
		// Linux: parse /proc/<pid>/status
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "State:") {
				state := strings.TrimSpace(strings.TrimPrefix(line, "State:"))
				return strings.HasPrefix(state, "T") || strings.HasPrefix(state, "Z")
			}
		}
		return false
	}
	// macOS: use ps to check process state
	out, err := execCommand("ps", "-o", "state=", "-p", fmt.Sprintf("%d", pid))
	if err != nil {
		return false
	}
	state := strings.TrimSpace(string(out))
	// macOS ps state: T = stopped, Z = zombie
	return len(state) > 0 && (state[0] == 'T' || state[0] == 'Z')
}

// killStuckProcess sends SIGKILL to a stuck process and returns true if successful.
func killStuckProcess(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if err := proc.Signal(syscall.SIGKILL); err != nil {
		return false
	}
	// Reap zombie if we can
	proc.Wait()
	return true
}

// lockFile acquires an exclusive non-blocking flock on f.
func lockFile(f *os.File) bool {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil
}

// unlockFile releases the flock on f.
func unlockFile(f *os.File) {
	syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}

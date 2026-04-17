package processlock

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// newTestLock constructs a ProviderLock pointing at a temp dir so tests don't
// collide with the user's real ~/.curdx/run/ lock files.
func newTestLock(t *testing.T, provider string) *ProviderLock {
	t.Helper()
	dir := t.TempDir()
	return &ProviderLock{
		Provider: provider,
		Timeout:  1.0,
		LockDir:  dir,
		LockFile: filepath.Join(dir, provider+".lock"),
	}
}

func TestNewProviderLock_UsesCWDHash(t *testing.T) {
	// Different CWDs must produce different lock-file names so two
	// projects don't serialize each other's ask/pend traffic.
	home := t.TempDir()
	t.Setenv("HOME", home)
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", home)
	}
	l1 := NewProviderLock("codex", 5.0, "/proj/one")
	l2 := NewProviderLock("codex", 5.0, "/proj/two")
	if l1.LockFile == l2.LockFile {
		t.Errorf("lock files collided: %s", l1.LockFile)
	}
	// Same provider + same cwd must yield the same path.
	l3 := NewProviderLock("codex", 5.0, "/proj/one")
	if l1.LockFile != l3.LockFile {
		t.Errorf("same inputs should yield same path: %s vs %s",
			l1.LockFile, l3.LockFile)
	}
	// Provider name is part of the path.
	l4 := NewProviderLock("gemini", 5.0, "/proj/one")
	if l1.LockFile == l4.LockFile {
		t.Errorf("different providers should differ: %s", l1.LockFile)
	}
}

func TestNewProviderLock_DefaultsCWDToGetwd(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", home)
	}
	cwd, _ := os.Getwd()
	l := NewProviderLock("codex", 5.0, "")
	expect := NewProviderLock("codex", 5.0, cwd)
	if l.LockFile != expect.LockFile {
		t.Errorf("empty cwd should default to os.Getwd(): got %s, want %s",
			l.LockFile, expect.LockFile)
	}
}

func TestTryAcquire_FreshLock(t *testing.T) {
	l := newTestLock(t, "codex")
	if !l.TryAcquire() {
		t.Fatal("expected TryAcquire to succeed on fresh file")
	}
	defer l.Release()

	// After acquiring, the lock file should contain the current PID.
	data, err := os.ReadFile(l.LockFile)
	if err != nil {
		t.Fatalf("read lock file: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("lock file content not a PID: %q", data)
	}
	if pid != os.Getpid() {
		t.Errorf("lock file PID %d != os.Getpid() %d", pid, os.Getpid())
	}
}

func TestRelease_IsIdempotent(t *testing.T) {
	l := newTestLock(t, "codex")
	if !l.TryAcquire() {
		t.Fatal("TryAcquire failed")
	}
	l.Release()
	l.Release() // second release must not panic
	l.Release() // third release must not panic
}

func TestRelease_BeforeAcquireIsSafe(t *testing.T) {
	l := newTestLock(t, "codex")
	// Call Release before any Acquire — should be a no-op, not a panic.
	l.Release()
}

func TestTryAcquire_ReleaseCycleReusable(t *testing.T) {
	l := newTestLock(t, "codex")
	for i := 0; i < 5; i++ {
		if !l.TryAcquire() {
			t.Fatalf("iteration %d: TryAcquire failed", i)
		}
		l.Release()
	}
}

func TestAcquire_SucceedsImmediatelyOnFreshLock(t *testing.T) {
	l := newTestLock(t, "codex")
	l.Timeout = 5.0
	start := time.Now()
	if !l.Acquire() {
		t.Fatal("expected Acquire to succeed on fresh file")
	}
	defer l.Release()
	// Fresh lock should be acquired without waiting.
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Errorf("Acquire took %v — should be immediate on fresh file", elapsed)
	}
}

func TestCheckStaleLock_DetectsDeadPID(t *testing.T) {
	l := newTestLock(t, "codex")

	// Manually seed the lock file with a PID that's almost certainly dead.
	// PID 1 is always alive (init). We want a PID that doesn't exist.
	// Finding a dead PID reliably: fork+exit a subprocess and capture
	// its PID — but simpler is to use a very large PID that's unlikely
	// to be in use.
	deadPID := findDeadPID(t)
	if err := os.WriteFile(l.LockFile, fmt.Appendf(nil, "%d\n", deadPID), 0o644); err != nil {
		t.Fatalf("seed lock file: %v", err)
	}

	if !l.checkStaleLock() {
		t.Errorf("expected checkStaleLock to report stale for dead PID %d", deadPID)
	}
	// The stale check removes the lock file.
	if _, err := os.Stat(l.LockFile); !os.IsNotExist(err) {
		t.Errorf("stale lock file should be removed, but stat err: %v", err)
	}
}

func TestCheckStaleLock_LiveCurrentProcess(t *testing.T) {
	l := newTestLock(t, "codex")
	// Seed with the current PID, which is obviously alive and not stuck.
	_ = os.WriteFile(l.LockFile,
		fmt.Appendf(nil, "%d\n", os.Getpid()), 0o644)

	if l.checkStaleLock() {
		t.Error("expected checkStaleLock to return false for live, healthy PID")
	}
	// File must still be present after a non-stale check.
	if _, err := os.Stat(l.LockFile); err != nil {
		t.Errorf("live-PID lock file should remain: %v", err)
	}
}

func TestCheckStaleLock_EmptyFile(t *testing.T) {
	l := newTestLock(t, "codex")
	_ = os.WriteFile(l.LockFile, nil, 0o644)
	if l.checkStaleLock() {
		t.Error("empty lock file should not be considered stale-takeable")
	}
}

func TestCheckStaleLock_MalformedContent(t *testing.T) {
	l := newTestLock(t, "codex")
	_ = os.WriteFile(l.LockFile, []byte("not-a-number"), 0o644)
	if l.checkStaleLock() {
		t.Error("malformed content should not be considered stale-takeable")
	}
}

func TestCheckStaleLock_MissingFile(t *testing.T) {
	l := newTestLock(t, "codex")
	// File doesn't exist — checkStaleLock should report false without erroring.
	if l.checkStaleLock() {
		t.Error("missing lock file should return false")
	}
}

func TestTryAcquire_ReclaimsStaleLock(t *testing.T) {
	l := newTestLock(t, "codex")
	deadPID := findDeadPID(t)
	_ = os.WriteFile(l.LockFile, fmt.Appendf(nil, "%d\n", deadPID), 0o644)

	if !l.TryAcquire() {
		t.Fatal("TryAcquire should reclaim a lock held by a dead PID")
	}
	defer l.Release()

	// After reclaim the lock file should hold OUR pid.
	data, _ := os.ReadFile(l.LockFile)
	if !strings.Contains(string(data), strconv.Itoa(os.Getpid())) {
		t.Errorf("expected our PID in reclaimed lock, got %q", data)
	}
}

func TestTryAcquire_ConcurrentSingleProcess(t *testing.T) {
	// Single-process concurrent TryAcquire on different ProviderLock
	// instances should only let one win (both point at the same file).
	home := t.TempDir()
	dir := filepath.Join(home, "run")
	_ = os.MkdirAll(dir, 0o755)
	file := filepath.Join(dir, "codex.lock")

	mkLock := func() *ProviderLock {
		return &ProviderLock{
			Provider: "codex",
			Timeout:  0.2,
			LockDir:  dir,
			LockFile: file,
		}
	}

	var wins int32
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l := mkLock()
			if l.TryAcquire() {
				mu.Lock()
				wins++
				mu.Unlock()
				time.Sleep(50 * time.Millisecond)
				l.Release()
			}
		}()
	}
	wg.Wait()

	// With flock on a single file, we expect at most 10 wins across
	// sequential attempts but on any single instant only one holder.
	// The loose sanity check: at least one goroutine succeeded.
	mu.Lock()
	defer mu.Unlock()
	if wins == 0 {
		t.Error("expected at least one goroutine to acquire the lock")
	}
}

// findDeadPID tries to find a PID that's not currently running so the
// stale-lock logic has something to detect. It picks a large PID and
// verifies via the Unix FindProcess+Signal(0) dance that it's dead.
func findDeadPID(t *testing.T) int {
	t.Helper()
	// Try PIDs near the kernel's default max; unlikely to collide.
	for _, pid := range []int{4194301, 4194303, 999999, 999997} {
		if !isPIDAlive(pid) {
			return pid
		}
	}
	t.Fatal("could not find a dead PID to seed stale-lock tests")
	return 0 // unreachable
}

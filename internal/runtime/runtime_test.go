package runtime

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// Tests use t.TempDir() + t.Setenv so they never touch the real
// $HOME/.cache/curdx/ or the user's actual daemon state.

// ── RunDir ──

func TestRunDir_RunDirOverrideWins(t *testing.T) {
	override := t.TempDir()
	t.Setenv("CURDX_RUN_DIR", override)
	// Ensure other env doesn't interfere.
	t.Setenv("XDG_CACHE_HOME", filepath.Join(t.TempDir(), "ignored"))
	if got := RunDir(); got != override {
		t.Errorf("expected %q, got %q", override, got)
	}
}

func TestRunDir_ExpandsTildeInOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", home)
	}
	t.Setenv("CURDX_RUN_DIR", "~/custom")
	t.Setenv("XDG_CACHE_HOME", "")

	got := RunDir()
	want := filepath.Join(home, "custom")
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func TestRunDir_XDGCacheHome(t *testing.T) {
	t.Setenv("CURDX_RUN_DIR", "")
	xdg := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", xdg)
	want := filepath.Join(xdg, "curdx")
	if got := RunDir(); got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func TestRunDir_FallsBackToHome(t *testing.T) {
	t.Setenv("CURDX_RUN_DIR", "")
	t.Setenv("XDG_CACHE_HOME", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", home)
	}
	want := filepath.Join(home, ".cache", "curdx")
	if got := RunDir(); got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

// ── StateFilePath / LogPath ──

func TestStateFilePath(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CURDX_RUN_DIR", root)
	t.Setenv("XDG_CACHE_HOME", "")

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"bare name gets .json suffix", "cxb-askd", filepath.Join(root, "cxb-askd.json")},
		{"name with .json preserved", "cxb-askd.json", filepath.Join(root, "cxb-askd.json")},
		{"nested .json treated literally", "prefix.json.json", filepath.Join(root, "prefix.json.json")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := StateFilePath(tc.in); got != tc.want {
				t.Errorf("StateFilePath(%q)=%q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestLogPath(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CURDX_RUN_DIR", root)
	t.Setenv("XDG_CACHE_HOME", "")

	if got := LogPath("cxb-askd"); got != filepath.Join(root, "cxb-askd.log") {
		t.Errorf("bare suffix appended: got %q", got)
	}
	if got := LogPath("cxb-askd.log"); got != filepath.Join(root, "cxb-askd.log") {
		t.Errorf("existing .log preserved: got %q", got)
	}
}

// ── EnsureRunDir ──
//
// EnsureRunDir is the function added by the Tier 2.3 commit; these tests
// pin its contract.

func TestEnsureRunDir_CreatesWith0o700(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "rundir-new")
	t.Setenv("CURDX_RUN_DIR", runDir)
	t.Setenv("XDG_CACHE_HOME", "")

	if err := EnsureRunDir(); err != nil {
		t.Fatalf("EnsureRunDir: %v", err)
	}
	info, err := os.Stat(runDir)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !info.IsDir() {
		t.Errorf("expected directory")
	}
	if runtime.GOOS != "windows" {
		// Windows ignores Unix mode bits; only check on Unix.
		if mode := info.Mode().Perm(); mode != 0o700 {
			t.Errorf("expected mode 0o700, got %o", mode)
		}
	}
}

func TestEnsureRunDir_IdempotentAndPreservesExistingMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only: Windows doesn't honour chmod() on directories")
	}
	root := t.TempDir()
	runDir := filepath.Join(root, "existing")
	if err := os.Mkdir(runDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Setenv("CURDX_RUN_DIR", runDir)
	t.Setenv("XDG_CACHE_HOME", "")

	if err := EnsureRunDir(); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if err := EnsureRunDir(); err != nil {
		t.Fatalf("second call: %v", err)
	}
	info, _ := os.Stat(runDir)
	// Intentionally must NOT tighten an existing user-owned dir.
	if mode := info.Mode().Perm(); mode != 0o755 {
		t.Errorf("EnsureRunDir overrode existing mode: got %o, want 0755", mode)
	}
}

func TestEnsureRunDir_ReturnsErrorIfPathIsFile(t *testing.T) {
	root := t.TempDir()
	fake := filepath.Join(root, "rundir-is-file")
	if err := os.WriteFile(fake, []byte("not a dir"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CURDX_RUN_DIR", fake)
	t.Setenv("XDG_CACHE_HOME", "")

	err := EnsureRunDir()
	if err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("expected 'not a directory' error, got %v", err)
	}
}

// ── WriteLog / maybeShrinkLog ──

func TestWriteLog_AppendsAndCreatesDir(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "nested", "dir", "app.log")

	WriteLog(path, "first line")
	WriteLog(path, "second line\n")  // trailing newline should be normalized
	WriteLog(path, "  third   \t\r") // trailing whitespace trimmed

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	got := string(data)
	want := "first line\nsecond line\n  third\n"
	if got != want {
		t.Errorf("log content mismatch:\nwant: %q\ngot:  %q", want, got)
	}

	if runtime.GOOS != "windows" {
		info, _ := os.Stat(path)
		if mode := info.Mode().Perm(); mode != 0o600 {
			t.Errorf("log file mode=%o, want 0o600", mode)
		}
	}
}

func TestWriteLog_DoesNotPanicOnBadPath(t *testing.T) {
	// WriteLog intentionally swallows all errors (Python parity).
	// Passing a path that can't be opened must not panic.
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("WriteLog panicked: %v", r)
		}
	}()
	// \x00 is invalid in unix paths and windows paths; open() fails.
	WriteLog("/dev/full/\x00bogus", "whatever")
}

func TestMaybeShrinkLog_TruncatesOversizeFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.log")

	// Seed with 10 KB
	big := strings.Repeat("A", 10*1024)
	if err := os.WriteFile(path, []byte(big), 0o600); err != nil {
		t.Fatal(err)
	}

	// Force a 1 KB cap and zero interval so the shrink runs.
	t.Setenv("CURDX_LOG_MAX_BYTES", "1024")
	t.Setenv("CURDX_LOG_SHRINK_CHECK_INTERVAL_S", "0")

	maybeShrinkLog(path)

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Size() > 1024 {
		t.Errorf("expected truncation to <=1024 bytes, got %d", info.Size())
	}
}

func TestMaybeShrinkLog_RespectsIntervalThrottle(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "throttled.log")

	// Reset package-level interval-throttle state so prior tests don't
	// leak the "last check" timestamp for this path.
	lastLogShrinkCheckMu.Lock()
	delete(lastLogShrinkCheck, path)
	lastLogShrinkCheckMu.Unlock()

	// First call records "now" in the throttle map and DOES shrink.
	t.Setenv("CURDX_LOG_MAX_BYTES", "100")
	t.Setenv("CURDX_LOG_SHRINK_CHECK_INTERVAL_S", "60") // 60s throttle window

	big := strings.Repeat("B", 1024)
	if err := os.WriteFile(path, []byte(big), 0o600); err != nil {
		t.Fatal(err)
	}
	maybeShrinkLog(path)
	info, _ := os.Stat(path)
	firstSize := info.Size()
	if firstSize > 100 {
		t.Fatalf("first call should shrink, got %d", firstSize)
	}

	// Write another 2 KB — throttle window means the second call skips
	// shrinking and the file should remain at 2 KB.
	if err := os.WriteFile(path, []byte(strings.Repeat("C", 2048)), 0o600); err != nil {
		t.Fatal(err)
	}
	maybeShrinkLog(path)
	info, _ = os.Stat(path)
	if info.Size() != 2048 {
		t.Errorf("throttle failed; second shrink ran (size=%d, want 2048)", info.Size())
	}
}

// ── RandomToken ──

func TestRandomToken_FormatAndUniqueness(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 20; i++ {
		tok := RandomToken()
		if len(tok) != 32 {
			t.Errorf("token length=%d, want 32", len(tok))
		}
		if _, err := hex.DecodeString(tok); err != nil {
			t.Errorf("token not valid hex: %q (%v)", tok, err)
		}
		if seen[tok] {
			t.Errorf("duplicate token across iterations: %q", tok)
		}
		seen[tok] = true
	}
}

func TestRandomToken_ConcurrentSafe(t *testing.T) {
	// crypto/rand is goroutine-safe; document that assumption via -race.
	var wg sync.WaitGroup
	var mu sync.Mutex
	seen := make(map[string]bool)
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tok := RandomToken()
			mu.Lock()
			if seen[tok] {
				t.Errorf("concurrent duplicate: %q", tok)
			}
			seen[tok] = true
			mu.Unlock()
		}()
	}
	wg.Wait()
}

// ── NormalizeConnectHost ──

func TestNormalizeConnectHost(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"", "127.0.0.1"},
		{"0.0.0.0", "127.0.0.1"},
		{"::", "::1"},
		{"[::]", "::1"},
		{"127.0.0.1", "127.0.0.1"},
		{"192.168.1.10", "192.168.1.10"},
		{"  10.0.0.1  ", "10.0.0.1"}, // trimmed
		{"example.com", "example.com"},
	}
	for _, tc := range tests {
		t.Run(strings.ReplaceAll(tc.in, " ", "_"), func(t *testing.T) {
			if got := NormalizeConnectHost(tc.in); got != tc.want {
				t.Errorf("NormalizeConnectHost(%q)=%q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// ── GetDaemonWorkDir ──

func TestGetDaemonWorkDir_Valid(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CURDX_RUN_DIR", root)
	t.Setenv("XDG_CACHE_HOME", "")

	state := map[string]any{"work_dir": "/some/work/dir"}
	data, _ := json.Marshal(state)
	if err := os.WriteFile(filepath.Join(root, "cxb-askd.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	if got := GetDaemonWorkDir(""); got != "/some/work/dir" {
		t.Errorf("empty name should default to cxb-askd.json, got %q", got)
	}
	if got := GetDaemonWorkDir("cxb-askd.json"); got != "/some/work/dir" {
		t.Errorf("explicit name mismatch, got %q", got)
	}
}

func TestGetDaemonWorkDir_MissingField(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CURDX_RUN_DIR", root)
	t.Setenv("XDG_CACHE_HOME", "")
	_ = os.WriteFile(filepath.Join(root, "cxb-askd.json"), []byte(`{"host":"x"}`), 0o600)
	if got := GetDaemonWorkDir(""); got != "" {
		t.Errorf("expected empty for missing field, got %q", got)
	}
}

func TestGetDaemonWorkDir_EmptyStringField(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CURDX_RUN_DIR", root)
	t.Setenv("XDG_CACHE_HOME", "")
	_ = os.WriteFile(filepath.Join(root, "cxb-askd.json"),
		[]byte(`{"work_dir":"   "}`), 0o600)
	if got := GetDaemonWorkDir(""); got != "" {
		t.Errorf("whitespace-only field should be treated as empty, got %q", got)
	}
}

func TestGetDaemonWorkDir_MissingFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CURDX_RUN_DIR", root)
	t.Setenv("XDG_CACHE_HOME", "")
	if got := GetDaemonWorkDir("does-not-exist"); got != "" {
		t.Errorf("missing file should yield empty, got %q", got)
	}
}

func TestGetDaemonWorkDir_MalformedJSON(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CURDX_RUN_DIR", root)
	t.Setenv("XDG_CACHE_HOME", "")
	_ = os.WriteFile(filepath.Join(root, "cxb-askd.json"), []byte("{not json"), 0o600)
	if got := GetDaemonWorkDir(""); got != "" {
		t.Errorf("bad JSON should yield empty, got %q", got)
	}
}

func TestGetDaemonWorkDir_WrongType(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CURDX_RUN_DIR", root)
	t.Setenv("XDG_CACHE_HOME", "")
	// work_dir present but wrong type — must not panic, must return empty.
	_ = os.WriteFile(filepath.Join(root, "cxb-askd.json"),
		[]byte(`{"work_dir":42}`), 0o600)
	if got := GetDaemonWorkDir(""); got != "" {
		t.Errorf("wrong-type field should yield empty, got %q", got)
	}
}

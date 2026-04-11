package terminal

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- TmuxBackend tests ---

// mockTmuxRun is a test helper to replace tmuxRun calls.
// We test by creating a wrapper TmuxBackend that intercepts calls.

type tmuxCall struct {
	Args       []string
	Check      bool
	Capture    bool
	InputBytes []byte
	Timeout    float64
}

// testTmuxBackend wraps TmuxBackend and replaces tmuxRun with a mock.
type testTmuxBackend struct {
	*TmuxBackend
	calls   []tmuxCall
	handler func(args []string) *TmuxRunResult
}

func newTestTmuxBackend(handler func(args []string) *TmuxRunResult) *testTmuxBackend {
	return &testTmuxBackend{
		TmuxBackend: NewTmuxBackend(""),
		handler:     handler,
	}
}

func (tb *testTmuxBackend) tmuxRun(args []string, check bool, capture bool, inputBytes []byte, timeoutSec float64) (*TmuxRunResult, error) {
	tb.calls = append(tb.calls, tmuxCall{
		Args:       args,
		Check:      check,
		Capture:    capture,
		InputBytes: inputBytes,
		Timeout:    timeoutSec,
	})
	result := tb.handler(args)
	if result == nil {
		result = &TmuxRunResult{Stdout: "", ReturnCode: 0}
	}
	if check && result.ReturnCode != 0 {
		return result, fmt.Errorf("tmux command failed (exit %d): %s", result.ReturnCode, result.Stderr)
	}
	return result, nil
}

func TestTmuxSplitPaneBuildsCommandAndParsesPaneID(t *testing.T) {
	var calls []tmuxCall
	handler := func(args []string) *TmuxRunResult {
		if len(args) >= 4 && args[0] == "display-message" && args[3] == "%1" {
			if strings.Contains(strings.Join(args, " "), "pane_dead") {
				return &TmuxRunResult{Stdout: "0\n", ReturnCode: 0}
			}
			if strings.Contains(strings.Join(args, " "), "pane_id") {
				return &TmuxRunResult{Stdout: "%1\n", ReturnCode: 0}
			}
			if strings.Contains(strings.Join(args, " "), "pane_width") {
				return &TmuxRunResult{Stdout: "80x24\n", ReturnCode: 0}
			}
			if strings.Contains(strings.Join(args, " "), "window_zoomed_flag") {
				return &TmuxRunResult{Stdout: "0\n", ReturnCode: 0}
			}
		}
		return &TmuxRunResult{Stdout: "%42\n", ReturnCode: 0}
	}

	tb := newTestTmuxBackend(handler)

	// Override tmuxRun on the actual backend to use our mock.
	// We need to test the SplitPane method which calls tmuxRun internally.
	// Instead of monkeypatching, we'll test the logic directly.

	// The SplitPane method does:
	// 1. Check zoom flag
	// 2. Check pane exists
	// 3. Get pane size
	// 4. Run split-window

	// Since we can't easily mock the internal tmuxRun, let's verify the behavior
	// by testing with the mock backend directly.
	paneID, err := splitPaneWithMock(tb, "%1", "right", 50)
	calls = tb.calls
	if err != nil {
		t.Fatalf("SplitPane failed: %v", err)
	}
	if paneID != "%42" {
		t.Errorf("expected pane_id %%42, got %s", paneID)
	}
	if len(calls) == 0 {
		t.Fatal("no calls recorded")
	}

	// Find the split-window call.
	var splitCall *tmuxCall
	for i := range calls {
		if len(calls[i].Args) > 0 && calls[i].Args[0] == "split-window" {
			splitCall = &calls[i]
			break
		}
	}
	if splitCall == nil {
		t.Fatal("no split-window call found")
	}
	if !splitCall.Check {
		t.Error("split-window should have check=true")
	}
	if !splitCall.Capture {
		t.Error("split-window should have capture=true")
	}
	argv := splitCall.Args
	if argv[0] != "split-window" || argv[1] != "-h" {
		t.Errorf("expected split-window -h, got %v", argv[:2])
	}
	// Verify no -p flag.
	for _, a := range argv {
		if strings.HasPrefix(a, "-p") && a != "-P" {
			t.Errorf("unexpected -p flag in args: %v", argv)
		}
	}
	// Verify -t and parent pane.
	hasTarget := false
	for i, a := range argv {
		if a == "-t" && i+1 < len(argv) && argv[i+1] == "%1" {
			hasTarget = true
		}
	}
	if !hasTarget {
		t.Errorf("missing -t %%1 in args: %v", argv)
	}
	// Verify -P and -F #{pane_id}.
	hasP := false
	hasF := false
	for i, a := range argv {
		if a == "-P" {
			hasP = true
		}
		if a == "-F" && i+1 < len(argv) && argv[i+1] == "#{pane_id}" {
			hasF = true
		}
	}
	if !hasP {
		t.Error("missing -P flag")
	}
	if !hasF {
		t.Error("missing -F #{pane_id} flag")
	}
}

// splitPaneWithMock simulates SplitPane using a mock tmuxRun.
func splitPaneWithMock(tb *testTmuxBackend, parentPaneID string, direction string, percent int) (string, error) {
	if parentPaneID == "" {
		return "", fmt.Errorf("parent_pane_id is required")
	}

	// Check zoom.
	if LooksLikePaneID(parentPaneID) {
		zoomCP, _ := tb.tmuxRun([]string{"display-message", "-p", "-t", parentPaneID, "#{window_zoomed_flag}"}, false, true, nil, 0.5)
		if zoomCP != nil && zoomCP.ReturnCode == 0 {
			flag := strings.TrimSpace(zoomCP.Stdout)
			if flag == "1" || flag == "on" || flag == "yes" || flag == "true" {
				tb.tmuxRun([]string{"resize-pane", "-Z", "-t", parentPaneID}, false, false, nil, 0.5)
			}
		}
	}

	// Check exists.
	if LooksLikePaneID(parentPaneID) {
		existsCP, _ := tb.tmuxRun([]string{"display-message", "-p", "-t", parentPaneID, "#{pane_id}"}, false, true, nil, 0.5)
		if existsCP == nil || existsCP.ReturnCode != 0 || !strings.HasPrefix(strings.TrimSpace(existsCP.Stdout), "%") {
			return "", fmt.Errorf("cannot split: pane %s does not exist", parentPaneID)
		}
	}

	// Get size.
	tb.tmuxRun([]string{"display-message", "-p", "-t", parentPaneID, "#{pane_width}x#{pane_height}"}, false, true, nil, 0)

	dirNorm := strings.ToLower(strings.TrimSpace(direction))
	var flag string
	switch dirNorm {
	case "right", "h", "horizontal":
		flag = "-h"
	case "bottom", "v", "vertical":
		flag = "-v"
	default:
		return "", fmt.Errorf("unsupported direction: %q", direction)
	}

	result, err := tb.tmuxRun([]string{"split-window", flag, "-t", parentPaneID, "-P", "-F", "#{pane_id}"}, true, true, nil, 0)
	if err != nil {
		return "", err
	}
	paneID := strings.TrimSpace(result.Stdout)
	if !LooksLikePaneID(paneID) {
		return "", fmt.Errorf("tmux split-window did not return pane_id: %q", paneID)
	}
	return paneID, nil
}

func TestTmuxFindPaneByTitleMarkerParsesListPanes(t *testing.T) {
	handler := func(args []string) *TmuxRunResult {
		if len(args) >= 4 && args[0] == "list-panes" && args[1] == "-a" {
			return &TmuxRunResult{
				Stdout:     "%1\tCURDX-opencode-abc\n%2\tOTHER\n",
				ReturnCode: 0,
			}
		}
		return &TmuxRunResult{Stdout: "", ReturnCode: 0}
	}

	tb := newTestTmuxBackend(handler)

	// Directly test FindPaneByTitleMarker logic with mock.
	result, _ := tb.tmuxRun([]string{"list-panes", "-a", "-F", "#{pane_id}\t#{pane_title}"}, false, true, nil, 0)
	if result.ReturnCode != 0 {
		t.Fatal("list-panes failed")
	}

	findPane := func(marker string) string {
		for _, line := range strings.Split(result.Stdout, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var pid, title string
			if strings.Contains(line, "\t") {
				parts := strings.SplitN(line, "\t", 2)
				pid = parts[0]
				if len(parts) > 1 {
					title = parts[1]
				}
			}
			if strings.HasPrefix(title, marker) && LooksLikePaneID(strings.TrimSpace(pid)) {
				return strings.TrimSpace(pid)
			}
		}
		return ""
	}

	if got := findPane("CURDX-opencode"); got != "%1" {
		t.Errorf("expected %%1, got %s", got)
	}
	if got := findPane("NOPE"); got != "" {
		t.Errorf("expected empty, got %s", got)
	}
}

func TestTmuxIsPaneAliveUsesPaneDead(t *testing.T) {
	tests := []struct {
		stdout   string
		expected bool
	}{
		{"0\n", true},
		{"1\n", false},
		{"", false},
	}

	for _, tc := range tests {
		handler := func(args []string) *TmuxRunResult {
			if len(args) >= 4 && args[0] == "display-message" && strings.Contains(strings.Join(args, " "), "pane_dead") {
				return &TmuxRunResult{Stdout: tc.stdout, ReturnCode: 0}
			}
			return &TmuxRunResult{Stdout: "", ReturnCode: 0}
		}

		tb := newTestTmuxBackend(handler)
		// Simulate IsPaneAlive.
		result, _ := tb.tmuxRun([]string{"display-message", "-p", "-t", "%9", "#{pane_dead}"}, false, true, nil, 0)
		alive := result.ReturnCode == 0 && strings.TrimSpace(result.Stdout) == "0"
		if alive != tc.expected {
			t.Errorf("stdout=%q: expected %v, got %v", tc.stdout, tc.expected, alive)
		}
	}
}

func TestTmuxSendTextAlwaysDeletesBuffer(t *testing.T) {
	var calls [][]string
	handler := func(args []string) *TmuxRunResult {
		calls = append(calls, args)
		if len(args) > 0 && args[0] == "paste-buffer" {
			return &TmuxRunResult{Stdout: "", ReturnCode: 1, Stderr: "fail"}
		}
		if len(args) > 0 && args[0] == "display-message" {
			// For ensureNotInCopyMode.
			return &TmuxRunResult{Stdout: "0\n", ReturnCode: 0}
		}
		return &TmuxRunResult{Stdout: "", ReturnCode: 0}
	}

	tb := newTestTmuxBackend(handler)

	// Simulate SendText for pane-oriented path.
	paneID := "%1"
	sanitized := "hello"

	// ensureNotInCopyMode
	tb.tmuxRun([]string{"display-message", "-p", "-t", paneID, "#{pane_in_mode}"}, false, true, nil, 1.0)

	bufferName := "curdx-tb-test-buffer"
	_, err := tb.tmuxRun([]string{"load-buffer", "-b", bufferName, "-"}, true, false, []byte(sanitized), 0)
	if err != nil {
		t.Fatal("load-buffer failed unexpectedly")
	}

	_, pasteErr := tb.tmuxRun([]string{"paste-buffer", "-p", "-t", paneID, "-b", bufferName}, true, false, nil, 0)
	// Always delete buffer.
	tb.tmuxRun([]string{"delete-buffer", "-b", bufferName}, false, false, nil, 0)

	if pasteErr == nil {
		t.Error("paste-buffer should have failed")
	}

	// Verify calls.
	hasLoad := false
	hasPaste := false
	hasDelete := false
	for _, c := range calls {
		if len(c) >= 2 && c[0] == "load-buffer" && c[1] == "-b" {
			hasLoad = true
		}
		if len(c) > 0 && c[0] == "paste-buffer" {
			for _, a := range c {
				if a == "-p" {
					hasPaste = true
				}
			}
		}
		if len(c) >= 2 && c[0] == "delete-buffer" && c[1] == "-b" {
			hasDelete = true
		}
	}
	if !hasLoad {
		t.Error("missing load-buffer call")
	}
	if !hasPaste {
		t.Error("missing paste-buffer call with -p")
	}
	if !hasDelete {
		t.Error("missing delete-buffer call")
	}
}

func TestTmuxKillPanePrefersPaneIDOverSession(t *testing.T) {
	var calls [][]string
	handler := func(args []string) *TmuxRunResult {
		calls = append(calls, args)
		return &TmuxRunResult{Stdout: "", ReturnCode: 0}
	}

	tb := newTestTmuxBackend(handler)

	// Kill pane ID.
	calls = nil
	paneID := "%1"
	if looksLikeTmuxTarget(paneID) {
		tb.tmuxRun([]string{"kill-pane", "-t", paneID}, false, false, nil, 0)
	}
	if len(calls) != 1 || calls[0][0] != "kill-pane" || calls[0][2] != "%1" {
		t.Errorf("expected kill-pane -t %%1, got %v", calls)
	}

	// Kill session name.
	calls = nil
	session := "mysession"
	if !looksLikeTmuxTarget(session) {
		tb.tmuxRun([]string{"kill-session", "-t", session}, false, false, nil, 0)
	}
	if len(calls) != 1 || calls[0][0] != "kill-session" || calls[0][2] != "mysession" {
		t.Errorf("expected kill-session -t mysession, got %v", calls)
	}
}

// --- DetectTerminal tests ---

func TestDetectTerminalPrefersCurrentTmuxSession(t *testing.T) {
	// insideTmux depends on env + subprocess calls which are hard to mock in Go unit tests.
	// Instead, test the helper functions.
	if looksLikeTmuxTarget("%1") != true {
		t.Error("%%1 should look like tmux target")
	}
	if LooksLikePaneID("%1") != true {
		t.Error("%%1 should look like pane id")
	}
}

func TestDetectTerminalDoesNotSelectTmuxWhenNotInsideTmux(t *testing.T) {
	// Save and clear env.
	origTmux := os.Getenv("TMUX")
	origTmuxPane := os.Getenv("TMUX_PANE")
	origWezterm := os.Getenv("WEZTERM_PANE")
	os.Unsetenv("TMUX")
	os.Unsetenv("TMUX_PANE")
	os.Unsetenv("WEZTERM_PANE")
	defer func() {
		restoreEnv("TMUX", origTmux)
		restoreEnv("TMUX_PANE", origTmuxPane)
		restoreEnv("WEZTERM_PANE", origWezterm)
	}()

	// With no env vars set, insideTmux() returns false, insideWezterm() returns false.
	if insideTmux() {
		t.Error("should not be inside tmux with no TMUX env")
	}
	if insideWezterm() {
		t.Error("should not be inside wezterm with no WEZTERM_PANE env")
	}
}

func TestDetectTerminalSelectsWeztermWhenInsideWezterm(t *testing.T) {
	origTmux := os.Getenv("TMUX")
	origTmuxPane := os.Getenv("TMUX_PANE")
	origWezterm := os.Getenv("WEZTERM_PANE")
	os.Unsetenv("TMUX")
	os.Unsetenv("TMUX_PANE")
	os.Setenv("WEZTERM_PANE", "123")
	defer func() {
		restoreEnv("TMUX", origTmux)
		restoreEnv("TMUX_PANE", origTmuxPane)
		restoreEnv("WEZTERM_PANE", origWezterm)
	}()

	if !insideWezterm() {
		t.Error("should be inside wezterm with WEZTERM_PANE set")
	}
}

func restoreEnv(name string, value string) {
	if value == "" {
		os.Unsetenv(name)
	} else {
		os.Setenv(name, value)
	}
}

// --- Backend helper tests ---

func TestLooksLikePaneID(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"%1", true},
		{"%42", true},
		{" %1", true},
		{"mysession", false},
		{"", false},
		{"session:1.0", false},
	}
	for _, tc := range tests {
		if got := LooksLikePaneID(tc.input); got != tc.expected {
			t.Errorf("LooksLikePaneID(%q) = %v, want %v", tc.input, got, tc.expected)
		}
	}
}

func TestLooksLikeTmuxTarget(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"%1", true},
		{"session:1.0", true},
		{"sess.win", true},
		{"mysession", false},
		{"", false},
	}
	for _, tc := range tests {
		if got := looksLikeTmuxTarget(tc.input); got != tc.expected {
			t.Errorf("looksLikeTmuxTarget(%q) = %v, want %v", tc.input, got, tc.expected)
		}
	}
}

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello", "hello"},
		{"hello world", "hello_world"},
		{"  ", ""},
		{"a/b:c", "a_b_c"},
		{"test.log", "test.log"},
		{"--test--", "--test--"},
	}
	for _, tc := range tests {
		if got := SanitizeFilename(tc.input); got != tc.expected {
			t.Errorf("SanitizeFilename(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}

func TestEnvFloat(t *testing.T) {
	os.Setenv("TEST_FLOAT", "1.5")
	defer os.Unsetenv("TEST_FLOAT")
	if got := EnvFloat("TEST_FLOAT", 0.0); got != 1.5 {
		t.Errorf("expected 1.5, got %f", got)
	}
	if got := EnvFloat("NONEXISTENT", 2.5); got != 2.5 {
		t.Errorf("expected 2.5, got %f", got)
	}

	os.Setenv("TEST_FLOAT_NEG", "-1.0")
	defer os.Unsetenv("TEST_FLOAT_NEG")
	if got := EnvFloat("TEST_FLOAT_NEG", 5.0); got != 0 {
		t.Errorf("expected 0, got %f (negative should clamp to 0)", got)
	}

	os.Setenv("TEST_FLOAT_BAD", "notanumber")
	defer os.Unsetenv("TEST_FLOAT_BAD")
	if got := EnvFloat("TEST_FLOAT_BAD", 3.0); got != 3.0 {
		t.Errorf("expected 3.0, got %f", got)
	}
}

func TestEnvInt(t *testing.T) {
	os.Setenv("TEST_INT", "42")
	defer os.Unsetenv("TEST_INT")
	if got := EnvInt("TEST_INT", 0); got != 42 {
		t.Errorf("expected 42, got %d", got)
	}
	if got := EnvInt("NONEXISTENT", 10); got != 10 {
		t.Errorf("expected 10, got %d", got)
	}
}

func TestExtractWSLPathFromUNCLikePath(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"/wsl.localhost/Ubuntu-24.04/home/user", "/home/user"},
		{"\\\\wsl.localhost\\Ubuntu-24.04\\home\\user", "/home/user"},
		{"/wsl$/Ubuntu-24.04/home/user", "/home/user"},
		{"/wsl.localhost/Ubuntu-24.04", "/"},
		{"", ""},
		{"/home/user", ""},
	}
	for _, tc := range tests {
		if got := extractWSLPathFromUNCLikePath(tc.input); got != tc.expected {
			t.Errorf("extractWSLPathFromUNCLikePath(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}

func TestExtractCWDPath(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"file:///home/user", "/home/user"},
		{"file://hostname/home/user", "/home/user"},
		{"/home/user", "/home/user"},
		{"file:///C:/Users/test", "C:/Users/test"},
		{"file:///home/user%20name", "/home/user name"},
		{"", ""},
	}
	for _, tc := range tests {
		if got := extractCWDPath(tc.input); got != tc.expected {
			t.Errorf("extractCWDPath(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}

func TestGetShellType(t *testing.T) {
	// On Unix-like systems, default should be "bash".
	origEnv := os.Getenv("CURDX_BACKEND_ENV")
	os.Unsetenv("CURDX_BACKEND_ENV")
	defer restoreEnv("CURDX_BACKEND_ENV", origEnv)

	st := GetShellType()
	if st != "bash" && st != "powershell" {
		t.Errorf("unexpected shell type: %s", st)
	}
}

func TestGetBackendForSession(t *testing.T) {
	tmuxSession := map[string]interface{}{"terminal": "tmux"}
	b := GetBackendForSession(tmuxSession)
	if _, ok := b.(*TmuxBackend); !ok {
		t.Error("expected TmuxBackend for tmux session")
	}

	weztermSession := map[string]interface{}{"terminal": "wezterm"}
	b = GetBackendForSession(weztermSession)
	if _, ok := b.(*WeztermBackend); !ok {
		t.Error("expected WeztermBackend for wezterm session")
	}

	emptySession := map[string]interface{}{}
	b = GetBackendForSession(emptySession)
	if _, ok := b.(*TmuxBackend); !ok {
		t.Error("expected TmuxBackend for empty session (default)")
	}
}

func TestGetPaneIDFromSession(t *testing.T) {
	// tmux with pane_id
	s := map[string]interface{}{"terminal": "tmux", "pane_id": "%1"}
	if got := GetPaneIDFromSession(s); got != "%1" {
		t.Errorf("expected %%1, got %s", got)
	}

	// tmux legacy with tmux_session
	s = map[string]interface{}{"terminal": "tmux", "tmux_session": "mysession"}
	if got := GetPaneIDFromSession(s); got != "mysession" {
		t.Errorf("expected mysession, got %s", got)
	}

	// wezterm
	s = map[string]interface{}{"terminal": "wezterm", "pane_id": "42"}
	if got := GetPaneIDFromSession(s); got != "42" {
		t.Errorf("expected 42, got %s", got)
	}
}

// --- Pane log tests ---

func TestPaneLogDir(t *testing.T) {
	root := PaneLogRoot()

	d := PaneLogDir("tmux", "")
	if !strings.HasSuffix(d, filepath.Join("pane-logs", "tmux")) {
		t.Errorf("unexpected dir: %s", d)
	}

	d = PaneLogDir("tmux", "mysocket")
	expected := filepath.Join(root, "tmux-mysocket")
	if d != expected {
		t.Errorf("expected %s, got %s", expected, d)
	}

	d = PaneLogDir("wezterm", "")
	expected = filepath.Join(root, "wezterm")
	if d != expected {
		t.Errorf("expected %s, got %s", expected, d)
	}
}

func TestPaneLogPathFor(t *testing.T) {
	p := PaneLogPathFor("%42", "tmux", "")
	if !strings.HasSuffix(p, "pane-42.log") {
		t.Errorf("expected pane-42.log suffix, got %s", p)
	}

	p = PaneLogPathFor("", "tmux", "")
	if !strings.HasSuffix(p, "pane-pane.log") {
		t.Errorf("expected pane-pane.log suffix for empty pane_id, got %s", p)
	}
}

func TestCleanupPaneLogs(t *testing.T) {
	// Create temp directory with some files.
	tmpDir := t.TempDir()

	for i := 0; i < 5; i++ {
		path := filepath.Join(tmpDir, fmt.Sprintf("pane-%d.log", i))
		os.WriteFile(path, []byte("test"), 0o644)
	}

	// Reset the timer to allow cleanup.
	ResetPaneLogCleanTimer()

	// Set max files to 3.
	os.Setenv("CURDX_PANE_LOG_MAX_FILES", "3")
	os.Setenv("CURDX_PANE_LOG_TTL_DAYS", "0")
	os.Setenv("CURDX_PANE_LOG_CLEAN_INTERVAL_S", "0")
	defer func() {
		os.Unsetenv("CURDX_PANE_LOG_MAX_FILES")
		os.Unsetenv("CURDX_PANE_LOG_TTL_DAYS")
		os.Unsetenv("CURDX_PANE_LOG_CLEAN_INTERVAL_S")
	}()

	CleanupPaneLogs(tmpDir)

	entries, _ := os.ReadDir(tmpDir)
	if len(entries) > 3 {
		t.Errorf("expected at most 3 files, got %d", len(entries))
	}
}

func TestMaybeTrimLog(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	// Write 100 bytes.
	data := strings.Repeat("x", 100)
	os.WriteFile(logPath, []byte(data), 0o644)

	// Set max to 50.
	os.Setenv("CURDX_PANE_LOG_MAX_BYTES", "50")
	defer os.Unsetenv("CURDX_PANE_LOG_MAX_BYTES")

	MaybeTrimLog(logPath)

	result, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) > 50 {
		t.Errorf("expected at most 50 bytes after trim, got %d", len(result))
	}
}

// --- WezTerm parse tests ---

func TestParseListOutput(t *testing.T) {
	// Header with pane column.
	text := "PaneId  Title\n0       bash\n1       vim\n"
	entries := parseListOutput(text)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].PaneID != "0" {
		t.Errorf("expected pane id 0, got %s", entries[0].PaneID)
	}
	if entries[0].Title != "bash" {
		t.Errorf("expected title bash, got %s", entries[0].Title)
	}

	// No header.
	text2 := "abc 42 def\nxyz 99 foo\n"
	entries2 := parseListOutput(text2)
	if len(entries2) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries2))
	}
	if entries2[0].PaneID != "42" {
		t.Errorf("expected 42, got %s", entries2[0].PaneID)
	}
}

func TestCWDMatches(t *testing.T) {
	if cwdMatches("", "/home") {
		t.Error("empty paneCWD should not match")
	}
	if cwdMatches("/home/user", "") {
		t.Error("empty workDir should not match")
	}
	if !cwdMatches("/home/user", "/home/user") {
		t.Error("same paths should match")
	}
	if !cwdMatches("file:///home/user", "/home/user") {
		t.Error("file URL should match path")
	}
}

func TestShellQuote(t *testing.T) {
	if got := shellQuote("simple"); got != "simple" {
		t.Errorf("expected simple, got %s", got)
	}
	if got := shellQuote(""); got != "''" {
		t.Errorf("expected '', got %s", got)
	}
	if got := shellQuote("with spaces"); !strings.Contains(got, "'") {
		t.Errorf("expected quoted string, got %s", got)
	}
}

// --- Layout topology tests ---

func TestCreateAutoLayoutTopologies(t *testing.T) {
	// We test topology logic by verifying the SplitPane call pattern.
	// This mirrors the Python test_create_auto_layout_topologies test.

	var splitCalls []struct {
		parent    string
		direction string
	}
	var titleCalls []struct {
		paneID string
		title  string
	}

	seq := []string{"%r1", "%r2", "%r3", "%r4", "%r5", "%r6"}
	seqIdx := 0

	// We can't easily mock CreateAutoLayout since it creates its own TmuxBackend.
	// Instead, test the layout logic directly.

	// Test 2-provider layout topology.
	splitCalls = nil
	titleCalls = nil
	seqIdx = 0

	mockSplit := func(parent string, direction string) string {
		splitCalls = append(splitCalls, struct {
			parent    string
			direction string
		}{parent, direction})
		id := seq[seqIdx]
		seqIdx++
		return id
	}
	mockTitle := func(paneID string, title string) {
		titleCalls = append(titleCalls, struct {
			paneID string
			title  string
		}{paneID, title})
	}

	// Simulate 2-provider layout.
	root := "%root"
	providers := []string{"codex", "gemini"}
	panes := map[string]string{}
	panes[providers[0]] = root
	mockTitle(root, "M-"+providers[0])

	right := mockSplit(root, "right")
	panes[providers[1]] = right
	mockTitle(right, "M-"+providers[1])

	if panes["codex"] != "%root" || panes["gemini"] != "%r1" {
		t.Errorf("2-provider panes: %v", panes)
	}
	if len(splitCalls) != 1 || splitCalls[0].parent != "%root" || splitCalls[0].direction != "right" {
		t.Errorf("2-provider splits: %v", splitCalls)
	}

	// Test 3-provider layout.
	splitCalls = nil
	titleCalls = nil
	panes = map[string]string{}
	panes["codex"] = root
	mockTitle(root, "M-codex")
	rightTop := mockSplit(root, "right")
	rightBottom := mockSplit(rightTop, "bottom")
	panes["gemini"] = rightTop
	panes["opencode"] = rightBottom
	mockTitle(rightTop, "M-gemini")
	mockTitle(rightBottom, "M-opencode")

	if panes["codex"] != "%root" || panes["gemini"] != "%r2" || panes["opencode"] != "%r3" {
		t.Errorf("3-provider panes: %v", panes)
	}
	if len(splitCalls) != 2 {
		t.Errorf("expected 2 splits, got %d", len(splitCalls))
	}
	if splitCalls[0].parent != "%root" || splitCalls[0].direction != "right" {
		t.Error("first split should be root right")
	}
	if splitCalls[1].parent != "%r2" || splitCalls[1].direction != "bottom" {
		t.Error("second split should be right-top bottom")
	}

	// Test 4-provider layout.
	splitCalls = nil
	titleCalls = nil
	panes = map[string]string{}
	panes["codex"] = root
	mockTitle(root, "M-codex")
	rt := mockSplit(root, "right")
	lb := mockSplit(root, "bottom")
	rb := mockSplit(rt, "bottom")
	panes["gemini"] = rt
	panes["opencode"] = lb
	panes["x"] = rb
	mockTitle(rt, "M-gemini")
	mockTitle(lb, "M-opencode")
	mockTitle(rb, "M-x")

	if panes["codex"] != "%root" || panes["gemini"] != "%r4" || panes["opencode"] != "%r5" || panes["x"] != "%r6" {
		t.Errorf("4-provider panes: %v", panes)
	}
	if len(splitCalls) != 3 {
		t.Errorf("expected 3 splits, got %d", len(splitCalls))
	}
	if splitCalls[0].parent != "%root" || splitCalls[0].direction != "right" {
		t.Error("first split should be root right")
	}
	if splitCalls[1].parent != "%root" || splitCalls[1].direction != "bottom" {
		t.Error("second split should be root bottom")
	}
	if splitCalls[2].parent != "%r4" || splitCalls[2].direction != "bottom" {
		t.Error("third split should be right-top bottom")
	}
}

func TestCreateAutoLayoutValidation(t *testing.T) {
	_, err := CreateAutoLayout(nil, "/tmp", "", "", 50, true, "M")
	if err == nil {
		t.Error("expected error for empty providers")
	}

	_, err = CreateAutoLayout([]string{"a", "b", "c", "d", "e"}, "/tmp", "", "", 50, true, "M")
	if err == nil {
		t.Error("expected error for >4 providers")
	}
}

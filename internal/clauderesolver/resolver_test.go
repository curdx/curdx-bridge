package clauderesolver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Tests focus on the pure helper functions — the heavier orchestration
// (ResolveClaudeSession, loadRegistryByProjectIDUnfiltered) touches real
// filesystem state managed by the Claude CLI and is better covered with
// integration testing at a higher level.

// ── ClaudeProjectsRoot ──

func TestClaudeProjectsRoot_Default(t *testing.T) {
	// Env-free path: falls back to $HOME/.claude/projects
	home := t.TempDir()
	t.Setenv("HOME", home)
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", home)
	}
	t.Setenv("CLAUDE_PROJECTS_ROOT", "")
	t.Setenv("CLAUDE_PROJECT_ROOT", "")

	want := filepath.Join(home, ".claude", "projects")
	if got := ClaudeProjectsRoot(); got != want {
		t.Errorf("want %q, got %q", want, got)
	}
}

func TestClaudeProjectsRoot_EnvOverride(t *testing.T) {
	custom := t.TempDir()
	t.Setenv("CLAUDE_PROJECTS_ROOT", custom)
	if got := ClaudeProjectsRoot(); got != custom {
		t.Errorf("want %q, got %q", custom, got)
	}
}

func TestClaudeProjectsRoot_LegacyEnvOverride(t *testing.T) {
	// CLAUDE_PROJECT_ROOT (singular) is honoured when plural is unset —
	// this matches the Python source's fallback chain.
	t.Setenv("CLAUDE_PROJECTS_ROOT", "")
	custom := t.TempDir()
	t.Setenv("CLAUDE_PROJECT_ROOT", custom)
	if got := ClaudeProjectsRoot(); got != custom {
		t.Errorf("want %q, got %q", custom, got)
	}
}

func TestClaudeProjectsRoot_PluralWinsOverSingular(t *testing.T) {
	plural := t.TempDir()
	singular := t.TempDir()
	t.Setenv("CLAUDE_PROJECTS_ROOT", plural)
	t.Setenv("CLAUDE_PROJECT_ROOT", singular)
	if got := ClaudeProjectsRoot(); got != plural {
		t.Errorf("plural var should win; got %q", got)
	}
}

// ── readJSON ──

func TestReadJSON_Valid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.json")
	obj := map[string]any{"a": "b", "n": float64(42)}
	data, _ := json.Marshal(obj)
	_ = os.WriteFile(path, data, 0o600)

	got := readJSON(path)
	if got["a"] != "b" {
		t.Errorf("a=%v", got["a"])
	}
	if got["n"].(float64) != 42 {
		t.Errorf("n=%v", got["n"])
	}
}

func TestReadJSON_StripsBOM(t *testing.T) {
	// UTF-8 BOM (0xEF 0xBB 0xBF) prepended to valid JSON should be
	// tolerated — Claude occasionally writes JSON with BOM on Windows.
	path := filepath.Join(t.TempDir(), "bom.json")
	withBOM := append([]byte{0xef, 0xbb, 0xbf}, []byte(`{"k":"v"}`)...)
	_ = os.WriteFile(path, withBOM, 0o600)

	got := readJSON(path)
	if got == nil {
		t.Fatal("BOM-prefixed JSON should parse")
	}
	if got["k"] != "v" {
		t.Errorf("k=%v", got["k"])
	}
}

func TestReadJSON_MissingFile(t *testing.T) {
	if got := readJSON(filepath.Join(t.TempDir(), "nope.json")); got != nil {
		t.Errorf("expected nil for missing file, got %v", got)
	}
}

func TestReadJSON_Malformed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	_ = os.WriteFile(path, []byte("{not json"), 0o600)
	if got := readJSON(path); got != nil {
		t.Errorf("expected nil for malformed, got %v", got)
	}
}

// ── paneFromData ──

func TestPaneFromData_PaneIDWins(t *testing.T) {
	data := map[string]any{
		"pane_id":        "%42",
		"claude_pane_id": "%99", // should be ignored when pane_id present
		"terminal":       "tmux",
		"tmux_session":   "alt",
	}
	if got := paneFromData(data); got != "%42" {
		t.Errorf("pane_id should win, got %q", got)
	}
}

func TestPaneFromData_ClaudePaneIDFallback(t *testing.T) {
	data := map[string]any{"claude_pane_id": "%7"}
	if got := paneFromData(data); got != "%7" {
		t.Errorf("claude_pane_id should be used, got %q", got)
	}
}

func TestPaneFromData_TmuxSessionFallbackOnlyForTmux(t *testing.T) {
	// tmux_session is a legacy fallback, but ONLY when terminal=="tmux".
	tmuxData := map[string]any{
		"terminal":     "tmux",
		"tmux_session": "work",
	}
	if got := paneFromData(tmuxData); got != "work" {
		t.Errorf("tmux+tmux_session should yield 'work', got %q", got)
	}

	// Case-insensitive match on "tmux".
	upperData := map[string]any{
		"terminal":     "TMUX",
		"tmux_session": "work",
	}
	if got := paneFromData(upperData); got != "work" {
		t.Errorf("case-insensitive 'tmux' should work, got %q", got)
	}

	// wezterm should NOT fall back to tmux_session.
	wezData := map[string]any{
		"terminal":     "wezterm",
		"tmux_session": "ignored",
	}
	if got := paneFromData(wezData); got != "" {
		t.Errorf("wezterm should not use tmux_session fallback, got %q", got)
	}
}

func TestPaneFromData_NilReturnsEmpty(t *testing.T) {
	if got := paneFromData(nil); got != "" {
		t.Errorf("nil data should yield empty, got %q", got)
	}
}

func TestPaneFromData_WhitespaceTrimmed(t *testing.T) {
	data := map[string]any{"pane_id": "  %42  \n"}
	if got := paneFromData(data); got != "%42" {
		t.Errorf("expected trimmed %%42, got %q", got)
	}
}

// ── sessionFileFromRecord ──

func TestSessionFileFromRecord_NestedProviders(t *testing.T) {
	record := map[string]any{
		"providers": map[string]any{
			"claude": map[string]any{
				"session_file": "/nested/claude.jsonl",
			},
		},
	}
	if got := sessionFileFromRecord(record); got != "/nested/claude.jsonl" {
		t.Errorf("got %q", got)
	}
}

func TestSessionFileFromRecord_LegacyClaudeSessionFile(t *testing.T) {
	record := map[string]any{"claude_session_file": "/legacy/c.jsonl"}
	if got := sessionFileFromRecord(record); got != "/legacy/c.jsonl" {
		t.Errorf("got %q", got)
	}
}

func TestSessionFileFromRecord_GenericSessionFile(t *testing.T) {
	record := map[string]any{"session_file": "/flat/s.jsonl"}
	if got := sessionFileFromRecord(record); got != "/flat/s.jsonl" {
		t.Errorf("got %q", got)
	}
}

func TestSessionFileFromRecord_PrecedenceOrder(t *testing.T) {
	// providers.claude.session_file wins over claude_session_file,
	// which wins over session_file.
	record := map[string]any{
		"providers": map[string]any{
			"claude": map[string]any{"session_file": "/A"},
		},
		"claude_session_file": "/B",
		"session_file":        "/C",
	}
	if got := sessionFileFromRecord(record); got != "/A" {
		t.Errorf("nested providers should win, got %q", got)
	}

	delete(record, "providers")
	if got := sessionFileFromRecord(record); got != "/B" {
		t.Errorf("claude_session_file should win over session_file, got %q", got)
	}
}

func TestSessionFileFromRecord_Nil(t *testing.T) {
	if got := sessionFileFromRecord(nil); got != "" {
		t.Errorf("nil record should yield empty, got %q", got)
	}
}

// ── projectKeyForPath ──

func TestProjectKeyForPath(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"/home/user/proj", "-home-user-proj"},
		{"/a/b_c", "-a-b-c"},     // underscore replaced
		{"C:\\Users\\proj", "C--Users-proj"},
		{"path with spaces", "path-with-spaces"},
		{"", ""},
		{"abc123", "abc123"}, // alphanumerics preserved
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			if got := projectKeyForPath(tc.in); got != tc.want {
				t.Errorf("projectKeyForPath(%q)=%q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// ── candidateProjectDirs ──

func TestCandidateProjectDirs_DedupsAndIncludesPWD(t *testing.T) {
	root := t.TempDir()
	workDir := "/home/user/myproj"

	t.Setenv("PWD", "/home/user/myproj") // same as workDir → dedup
	dirs := candidateProjectDirs(root, workDir)
	if len(dirs) != 1 {
		t.Errorf("expected 1 deduped candidate, got %v", dirs)
	}
	want := filepath.Join(root, projectKeyForPath(workDir))
	if dirs[0] != want {
		t.Errorf("expected %q, got %q", want, dirs[0])
	}
}

func TestCandidateProjectDirs_PWDDifferentFromWorkDir(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PWD", "/home/user/other")
	dirs := candidateProjectDirs(root, "/home/user/myproj")
	// PWD and workDir yield distinct project keys → both appear.
	if len(dirs) < 2 {
		t.Errorf("expected PWD + workDir candidates, got %v", dirs)
	}
	// PWD comes first (per code order).
	wantFirst := filepath.Join(root, projectKeyForPath("/home/user/other"))
	if dirs[0] != wantFirst {
		t.Errorf("PWD should lead, got %q", dirs[0])
	}
}

func TestCandidateProjectDirs_HandlesEmptyPWD(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PWD", "")
	dirs := candidateProjectDirs(root, "/home/user/myproj")
	if len(dirs) == 0 {
		t.Fatal("expected at least one candidate")
	}
	want := filepath.Join(root, projectKeyForPath("/home/user/myproj"))
	found := false
	for _, d := range dirs {
		if d == want {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected workDir candidate %q in %v", want, dirs)
	}
}

// ── sessionPathFromID ──

func TestSessionPathFromID_EmptySessionID(t *testing.T) {
	if got := sessionPathFromID("", "/home/user"); got != "" {
		t.Errorf("empty session ID should yield empty path, got %q", got)
	}
	if got := sessionPathFromID("   ", "/home/user"); got != "" {
		t.Errorf("whitespace-only session ID should yield empty path, got %q", got)
	}
}

func TestSessionPathFromID_FindsExistingFile(t *testing.T) {
	// Set up a fake Claude projects root with one known session file.
	root := t.TempDir()
	t.Setenv("CLAUDE_PROJECTS_ROOT", root)
	t.Setenv("CLAUDE_PROJECT_ROOT", "")

	workDir := "/fake/work/dir"
	t.Setenv("PWD", workDir)
	projDir := filepath.Join(root, projectKeyForPath(workDir))
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sid := "abc-123"
	sessionFile := filepath.Join(projDir, sid+".jsonl")
	if err := os.WriteFile(sessionFile, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got := sessionPathFromID(sid, workDir)
	if got != sessionFile {
		t.Errorf("expected %q, got %q", sessionFile, got)
	}
}

func TestSessionPathFromID_NotFoundReturnsEmpty(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CLAUDE_PROJECTS_ROOT", root)
	t.Setenv("CLAUDE_PROJECT_ROOT", "")
	t.Setenv("PWD", "/nonexistent/work")

	if got := sessionPathFromID("missing-id", "/nonexistent/work"); got != "" {
		t.Errorf("expected empty for not-found, got %q", got)
	}
}

// Sanity check: confirm that SessionEnvKeys covers the expected providers.
func TestSessionEnvKeys_Contents(t *testing.T) {
	joined := strings.Join(SessionEnvKeys, ",")
	for _, want := range []string{
		"CURDX_SESSION_ID", "CODEX_SESSION_ID",
		"GEMINI_SESSION_ID", "OPENCODE_SESSION_ID",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("SessionEnvKeys missing %q", want)
		}
	}
}

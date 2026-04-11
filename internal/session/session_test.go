package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// --- test helpers ---

type mockBackend struct {
	alive      map[string]bool
	markerMap  map[string]string
	belongsCwd bool
}

func (b *mockBackend) IsAlive(paneID string) bool {
	return b.alive[paneID]
}

func (b *mockBackend) FindPaneByTitleMarker(marker string, cwdHint string) string {
	return b.markerMap[marker]
}

func (b *mockBackend) PaneBelongsToCWD(paneID, workDir string) bool {
	return b.belongsCwd
}

func (b *mockBackend) EnsurePaneLog(paneID string) string { return "" }

func (b *mockBackend) RespawnPane(paneID, cmd, cwd, stderrLogPath string, remainOnExit bool) error {
	b.alive[paneID] = true
	return nil
}

func (b *mockBackend) SaveCrashLog(paneID, logPath string, lines int) error { return nil }

func writeSessionFile(t *testing.T, dir, filename string, data map[string]interface{}) string {
	t.Helper()
	os.MkdirAll(dir, 0o755)
	path := filepath.Join(dir, filename)
	raw, _ := json.MarshalIndent(data, "", "  ")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// --- tests ---

func TestEnsurePaneAlive(t *testing.T) {
	backend := &mockBackend{
		alive:      map[string]bool{"%1": true},
		markerMap:  map[string]string{},
		belongsCwd: true,
	}
	oldFn := GetBackendFunc
	GetBackendFunc = func(data map[string]interface{}) TerminalBackend { return backend }
	defer func() { GetBackendFunc = oldFn }()

	dir := t.TempDir()
	sf := writeSessionFile(t, dir, ".codex-session", map[string]interface{}{
		"pane_id":  "%1",
		"terminal": "tmux",
		"work_dir": dir,
	})

	s := &CodexProjectSession{SessionFile: sf, Data: readJSON(sf)}
	result := s.EnsurePane()
	if !result.OK {
		t.Errorf("expected OK, got error: %s", result.Err)
	}
	if result.PaneID != "%1" {
		t.Errorf("pane_id = %q, want %%1", result.PaneID)
	}
}

func TestEnsurePaneMarkerFallback(t *testing.T) {
	backend := &mockBackend{
		alive:      map[string]bool{"%2": true},
		markerMap:  map[string]string{"CURDX_CODEX_123": "%2"},
		belongsCwd: true,
	}
	oldFn := GetBackendFunc
	GetBackendFunc = func(data map[string]interface{}) TerminalBackend { return backend }
	defer func() { GetBackendFunc = oldFn }()

	dir := t.TempDir()
	sf := writeSessionFile(t, dir, ".codex-session", map[string]interface{}{
		"pane_id":            "%dead",
		"pane_title_marker":  "CURDX_CODEX_123",
		"terminal":           "tmux",
		"work_dir":           dir,
	})

	s := &CodexProjectSession{SessionFile: sf, Data: readJSON(sf)}
	result := s.EnsurePane()
	if !result.OK {
		t.Errorf("expected OK, got error: %s", result.Err)
	}
	if result.PaneID != "%2" {
		t.Errorf("pane_id = %q, want %%2", result.PaneID)
	}
}

func TestEnsurePaneRespawn(t *testing.T) {
	backend := &mockBackend{
		alive:      map[string]bool{},
		markerMap:  map[string]string{},
		belongsCwd: true,
	}
	oldFn := GetBackendFunc
	GetBackendFunc = func(data map[string]interface{}) TerminalBackend { return backend }
	defer func() { GetBackendFunc = oldFn }()

	dir := t.TempDir()
	sf := writeSessionFile(t, dir, ".codex-session", map[string]interface{}{
		"pane_id":   "%3",
		"terminal":  "tmux",
		"work_dir":  dir,
		"start_cmd": "codex start",
	})

	s := &CodexProjectSession{SessionFile: sf, Data: readJSON(sf)}
	result := s.EnsurePane()
	if !result.OK {
		t.Errorf("expected OK after respawn, got error: %s", result.Err)
	}
	if result.PaneID != "%3" {
		t.Errorf("pane_id = %q, want %%3", result.PaneID)
	}
}

func TestEnsurePaneNoBackend(t *testing.T) {
	oldFn := GetBackendFunc
	GetBackendFunc = nil
	defer func() { GetBackendFunc = oldFn }()

	dir := t.TempDir()
	sf := writeSessionFile(t, dir, ".codex-session", map[string]interface{}{
		"pane_id":  "%1",
		"terminal": "tmux",
		"work_dir": dir,
	})

	s := &CodexProjectSession{SessionFile: sf, Data: readJSON(sf)}
	result := s.EnsurePane()
	if result.OK {
		t.Error("expected failure when no backend available")
	}
}

func TestClaudeNoRespawn(t *testing.T) {
	// Claude does NOT support respawn.
	backend := &mockBackend{
		alive:      map[string]bool{},
		markerMap:  map[string]string{},
		belongsCwd: true,
	}
	oldFn := GetBackendFunc
	GetBackendFunc = func(data map[string]interface{}) TerminalBackend { return backend }
	defer func() { GetBackendFunc = oldFn }()

	dir := t.TempDir()
	sf := writeSessionFile(t, dir, ".claude-session", map[string]interface{}{
		"pane_id":   "%4",
		"terminal":  "tmux",
		"work_dir":  dir,
		"start_cmd": "claude",
	})

	s := &ClaudeProjectSession{SessionFile: sf, Data: readJSON(sf)}
	result := s.EnsurePane()
	if result.OK {
		t.Error("claude should not respawn, expected failure")
	}
}

func TestComputeSessionKey(t *testing.T) {
	dir := t.TempDir()
	sf := writeSessionFile(t, dir, ".codex-session", map[string]interface{}{
		"curdx_project_id": "abc123",
		"work_dir":       dir,
	})

	s := &CodexProjectSession{SessionFile: sf, Data: readJSON(sf)}

	key := ComputeCodexSessionKey(s, "")
	if key != "codex:abc123" {
		t.Errorf("key = %q, want codex:abc123", key)
	}

	key = ComputeCodexSessionKey(s, "auth")
	if key != "codex:auth:abc123" {
		t.Errorf("key = %q, want codex:auth:abc123", key)
	}
}

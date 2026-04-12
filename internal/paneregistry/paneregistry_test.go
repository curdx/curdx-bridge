package paneregistry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/curdx/curdx-bridge/internal/projectid"
)

// --- test helpers ---

type fakeBackend struct {
	alive     map[string]bool
	markerMap map[string]string
}

func (b *fakeBackend) IsAlive(paneID string) bool {
	return b.alive[paneID]
}

func (b *fakeBackend) FindPaneByTitleMarker(marker string, cwdHint ...string) string {
	return b.markerMap[marker]
}

func writeRegistryFile(t *testing.T, home, sessionID string, payload map[string]interface{}) string {
	t.Helper()
	dir := filepath.Join(home, ".curdx", "run")
	os.MkdirAll(dir, 0o755)
	path := filepath.Join(dir, RegistryPrefix+sessionID+RegistrySuffix)
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func setupHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

// --- tests ---

func TestUpsertRegistryMergesProviders(t *testing.T) {
	home := setupHome(t)

	GetBackendFunc = func(record map[string]interface{}) TerminalBackend {
		return &fakeBackend{alive: map[string]bool{"%1": true}}
	}
	defer func() { GetBackendFunc = nil }()

	workDir := filepath.Join(home, "proj")
	os.MkdirAll(workDir, 0o755)
	pid := projectid.ComputeCURDXProjectID(workDir)

	ok1 := UpsertRegistry(map[string]interface{}{
		"curdx_session_id": "s1",
		"curdx_project_id": pid,
		"work_dir":       workDir,
		"terminal":       "tmux",
		"providers": map[string]interface{}{
			"codex": map[string]interface{}{
				"pane_id":      "%1",
				"session_file": filepath.Join(workDir, ".curdx", ".codex-session"),
			},
		},
	})
	if !ok1 {
		t.Fatal("first upsert failed")
	}

	ok2 := UpsertRegistry(map[string]interface{}{
		"curdx_session_id": "s1",
		"curdx_project_id": pid,
		"work_dir":       workDir,
		"terminal":       "tmux",
		"providers": map[string]interface{}{
			"claude": map[string]interface{}{
				"pane_id":      "%1",
				"session_file": filepath.Join(workDir, ".curdx", ".claude-session"),
			},
		},
	})
	if !ok2 {
		t.Fatal("second upsert failed")
	}

	path := RegistryPathForSession("s1")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var data map[string]interface{}
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}

	if data["curdx_project_id"] != pid {
		t.Errorf("curdx_project_id = %v, want %v", data["curdx_project_id"], pid)
	}
	provs, ok := data["providers"].(map[string]interface{})
	if !ok {
		t.Fatal("providers not a map")
	}
	if _, ok := provs["codex"]; !ok {
		t.Error("missing codex in providers")
	}
	if _, ok := provs["claude"]; !ok {
		t.Error("missing claude in providers")
	}
}

func TestLoadRegistryByProjectIDFiltersDeadPanes(t *testing.T) {
	home := setupHome(t)

	workDir := filepath.Join(home, "proj")
	os.MkdirAll(workDir, 0o755)
	pid := projectid.ComputeCURDXProjectID(workDir)

	now := time.Now().Unix()

	// Newer but dead.
	writeRegistryFile(t, home, "new", map[string]interface{}{
		"curdx_session_id": "new",
		"curdx_project_id": pid,
		"work_dir":       workDir,
		"terminal":       "tmux",
		"updated_at":     float64(now),
		"providers": map[string]interface{}{
			"codex": map[string]interface{}{"pane_id": "%dead"},
		},
	})

	// Older but alive.
	writeRegistryFile(t, home, "old", map[string]interface{}{
		"curdx_session_id": "old",
		"curdx_project_id": pid,
		"work_dir":       workDir,
		"terminal":       "tmux",
		"updated_at":     float64(now - 10),
		"providers": map[string]interface{}{
			"codex": map[string]interface{}{"pane_id": "%alive"},
		},
	})

	GetBackendFunc = func(record map[string]interface{}) TerminalBackend {
		return &fakeBackend{alive: map[string]bool{"%alive": true}}
	}
	defer func() { GetBackendFunc = nil }()

	rec := LoadRegistryByProjectID(pid, "codex")
	if rec == nil {
		t.Fatal("expected non-nil record")
	}
	sid, _ := rec["curdx_session_id"].(string)
	if sid != "old" {
		t.Errorf("curdx_session_id = %v, want old", rec["curdx_session_id"])
	}
}

func TestLoadRegistryByProjectIDInfersMissingProjectID(t *testing.T) {
	home := setupHome(t)

	workDir := filepath.Join(home, "proj")
	os.MkdirAll(workDir, 0o755)
	pid := projectid.ComputeCURDXProjectID(workDir)

	now := time.Now().Unix()

	// Legacy record missing curdx_project_id.
	writeRegistryFile(t, home, "legacy", map[string]interface{}{
		"curdx_session_id": "legacy",
		"work_dir":       workDir,
		"terminal":       "tmux",
		"updated_at":     float64(now),
		"providers": map[string]interface{}{
			"codex": map[string]interface{}{"pane_id": "%1"},
		},
	})

	GetBackendFunc = func(record map[string]interface{}) TerminalBackend {
		return &fakeBackend{alive: map[string]bool{"%1": true}}
	}
	defer func() { GetBackendFunc = nil }()

	rec := LoadRegistryByProjectID(pid, "codex")
	if rec == nil {
		t.Fatal("expected non-nil record")
	}
	sid, _ := rec["curdx_session_id"].(string)
	if sid != "legacy" {
		t.Errorf("curdx_session_id = %v, want legacy", rec["curdx_session_id"])
	}
}

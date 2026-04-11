// Package sessionutil tests — ported from test/test_session_utils.py
package sessionutil

import (
	"os"
	"path/filepath"
	"testing"
)

// resolveDir resolves symlinks in a path (e.g. /var -> /private/var on macOS).
func resolveDir(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return path
	}
	return resolved
}

func TestFindProjectSessionFileWalksUpward(t *testing.T) {
	tmpDir := resolveDir(t.TempDir())
	root := filepath.Join(tmpDir, "repo")
	leaf := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(leaf, 0o755); err != nil {
		t.Fatal(err)
	}

	session := filepath.Join(root, ".codex-session")
	if err := os.WriteFile(session, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	found := FindProjectSessionFile(leaf, ".codex-session")
	if found != session {
		t.Errorf("from leaf: expected %s, got %s", session, found)
	}

	found2 := FindProjectSessionFile(root, ".codex-session")
	if found2 != session {
		t.Errorf("from root: expected %s, got %s", session, found2)
	}
}

func TestFindProjectSessionFilePrefersCURDXConfig(t *testing.T) {
	tmpDir := resolveDir(t.TempDir())
	root := filepath.Join(tmpDir, "repo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := filepath.Join(root, ".curdx")
	if err := os.MkdirAll(cfg, 0o755); err != nil {
		t.Fatal(err)
	}

	primary := filepath.Join(cfg, ".codex-session")
	if err := os.WriteFile(primary, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	legacy := filepath.Join(root, ".codex-session")
	if err := os.WriteFile(legacy, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	found := FindProjectSessionFile(root, ".codex-session")
	if found != primary {
		t.Errorf("expected primary %s, got %s", primary, found)
	}
}

func TestSafeWriteSessionAtomicWrite(t *testing.T) {
	tmpDir := t.TempDir()
	target := filepath.Join(tmpDir, "state.json")

	ok, errMsg := SafeWriteSession(target, "{\"hello\":\"world\"}\n")
	if !ok {
		t.Fatalf("first write failed: %s", errMsg)
	}
	if errMsg != "" {
		t.Fatalf("expected no error, got: %s", errMsg)
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "{\"hello\":\"world\"}\n" {
		t.Errorf("unexpected content: %q", string(data))
	}

	tmpFile := target + ".tmp"
	if _, err := os.Stat(tmpFile); !os.IsNotExist(err) {
		t.Error("tmp file should not exist after write")
	}

	// Second write (overwrite)
	ok2, errMsg2 := SafeWriteSession(target, "{\"hello\":\"again\"}\n")
	if !ok2 {
		t.Fatalf("second write failed: %s", errMsg2)
	}
	if errMsg2 != "" {
		t.Fatalf("expected no error on second write, got: %s", errMsg2)
	}

	data2, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data2) != "{\"hello\":\"again\"}\n" {
		t.Errorf("unexpected content after second write: %q", string(data2))
	}

	if _, err := os.Stat(tmpFile); !os.IsNotExist(err) {
		t.Error("tmp file should not exist after second write")
	}
}

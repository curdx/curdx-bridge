package projectid

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Source: claude_code_bridge/test/test_project_id.py

func TestNormalizeWorkDirBasic(t *testing.T) {
	result1 := NormalizeWorkDir("/a/b/../c")
	if runtime.GOOS == "windows" {
		if result1 != "a:/c" {
			t.Errorf("expected a:/c on Windows, got %s", result1)
		}
	} else {
		if result1 != "/a/c" {
			t.Errorf("expected /a/c on Unix, got %s", result1)
		}
	}

	result2 := NormalizeWorkDir("/a//b///c")
	if runtime.GOOS == "windows" {
		if result2 != "a:/b/c" {
			t.Errorf("expected a:/b/c on Windows, got %s", result2)
		}
	} else {
		if result2 != "/a/b/c" {
			t.Errorf("expected /a/b/c on Unix, got %s", result2)
		}
	}
}

func TestNormalizeWorkDirWSLDriveMapping(t *testing.T) {
	result := NormalizeWorkDir("/mnt/C/Users/alice")
	if result != "c:/Users/alice" {
		t.Errorf("expected c:/Users/alice, got %s", result)
	}

	result = NormalizeWorkDir("/mnt/c/Users/alice")
	if result != "c:/Users/alice" {
		t.Errorf("expected c:/Users/alice, got %s", result)
	}
}

func TestNormalizeWorkDirEmpty(t *testing.T) {
	if NormalizeWorkDir("") != "" {
		t.Error("empty input should return empty")
	}
	if NormalizeWorkDir("  ") != "" {
		t.Error("whitespace input should return empty")
	}
}

func TestNormalizeWorkDirDoubleSlashPrefix(t *testing.T) {
	// Python's posixpath.normpath preserves // prefix
	result := NormalizeWorkDir("//server/share/path")
	if result != "//server/share/path" {
		t.Errorf("expected //server/share/path, got %s", result)
	}
}

func TestComputeCURDXProjectIDStableForSameDir(t *testing.T) {
	dir := t.TempDir()
	pid1 := ComputeCURDXProjectID(dir)
	pid2 := ComputeCURDXProjectID(dir)
	if pid1 == "" {
		t.Error("project ID should not be empty")
	}
	if pid1 != pid2 {
		t.Errorf("same dir should produce same ID: %s != %s", pid1, pid2)
	}
}

func TestComputeCURDXProjectIDUsesAnchorRoot(t *testing.T) {
	dir := t.TempDir()
	// Create .curdx anchor
	os.Mkdir(filepath.Join(dir, ".curdx"), 0o755)
	subdir := filepath.Join(dir, "a", "b")
	os.MkdirAll(subdir, 0o755)

	pidRoot := ComputeCURDXProjectID(dir)
	pidSub := ComputeCURDXProjectID(subdir)
	if pidRoot == "" || pidSub == "" {
		t.Error("project IDs should not be empty")
	}
	// Subdir doesn't have .curdx, so it uses itself as base → different from root
	if pidRoot == pidSub {
		t.Error("root with .curdx anchor and subdir without should differ")
	}
}

func TestComputeCURDXProjectIDFallbackDiffForSubdirs(t *testing.T) {
	dir := t.TempDir()
	subdir := filepath.Join(dir, "a", "b")
	os.MkdirAll(subdir, 0o755)
	pidRoot := ComputeCURDXProjectID(dir)
	pidSub := ComputeCURDXProjectID(subdir)
	if pidRoot == pidSub {
		t.Error("different dirs without anchor should produce different IDs")
	}
}

func TestComputeCURDXProjectIDLength(t *testing.T) {
	dir := t.TempDir()
	pid := ComputeCURDXProjectID(dir)
	// SHA256 hex = 64 chars
	if len(pid) != 64 {
		t.Errorf("expected 64 char hex, got %d chars: %s", len(pid), pid)
	}
}

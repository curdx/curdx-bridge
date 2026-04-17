package filewatcher

import (
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"
)

func TestDefaultPredicate(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"foo.jsonl", true},
		{".hidden.jsonl", false},
		{"sessions-index.json", true},
		{"other.json", false},
		{"bar.txt", false},
	}
	for _, tc := range tests {
		got := DefaultPredicate(tc.path)
		if got != tc.want {
			t.Errorf("DefaultPredicate(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestSessionFileWatcherEmitsOnCreate(t *testing.T) {
	dir := t.TempDir()

	var mu sync.Mutex
	var paths []string
	cb := func(path string) {
		mu.Lock()
		paths = append(paths, filepath.Base(path))
		mu.Unlock()
	}

	w := New(dir, cb, false, nil)
	if err := w.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer w.Stop()

	// Create a matching file.
	f := filepath.Join(dir, "test.jsonl")
	if err := os.WriteFile(f, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Wait for event to propagate.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(paths)
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	found := slices.Contains(paths, "test.jsonl")
	if !found {
		t.Errorf("expected test.jsonl in callback paths, got %v", paths)
	}
}

func TestSessionFileWatcherIgnoresNonMatching(t *testing.T) {
	dir := t.TempDir()

	var mu sync.Mutex
	var paths []string
	cb := func(path string) {
		mu.Lock()
		paths = append(paths, filepath.Base(path))
		mu.Unlock()
	}

	w := New(dir, cb, false, nil)
	if err := w.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer w.Stop()

	// Create a non-matching file.
	f := filepath.Join(dir, "readme.txt")
	if err := os.WriteFile(f, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Give time for events to propagate, then check.
	time.Sleep(300 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(paths) != 0 {
		t.Errorf("expected no callbacks for .txt file, got %v", paths)
	}
}

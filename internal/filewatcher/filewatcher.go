// Package filewatcher watches for session file changes using fsnotify.
// Source: claude_code_bridge/lib/session_file_watcher.py
package filewatcher

import (
	"path/filepath"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
)

// Callback is invoked when a watched file changes.
type Callback func(path string)

// Predicate decides whether a file event should be forwarded.
type Predicate func(path string) bool

// isLogFile returns true for .jsonl files not starting with ".".
func isLogFile(path string) bool {
	base := filepath.Base(path)
	return filepath.Ext(path) == ".jsonl" && !strings.HasPrefix(base, ".")
}

// isIndexFile returns true for sessions-index.json.
func isIndexFile(path string) bool {
	return filepath.Base(path) == "sessions-index.json"
}

// DefaultPredicate matches .jsonl log files and sessions-index.json.
func DefaultPredicate(path string) bool {
	return isLogFile(path) || isIndexFile(path)
}

// SessionFileWatcher monitors a directory for session file changes.
type SessionFileWatcher struct {
	projectDir string
	callback   Callback
	predicate  Predicate
	recursive  bool
	watcher    *fsnotify.Watcher
	stopOnce   sync.Once
	done       chan struct{}
}

// New creates a new SessionFileWatcher. If predicate is nil, DefaultPredicate is used.
func New(projectDir string, callback Callback, recursive bool, predicate Predicate) *SessionFileWatcher {
	if predicate == nil {
		predicate = DefaultPredicate
	}
	return &SessionFileWatcher{
		projectDir: projectDir,
		callback:   callback,
		predicate:  predicate,
		recursive:  recursive,
		done:       make(chan struct{}),
	}
}

// Start begins watching the project directory.
func (w *SessionFileWatcher) Start() error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	w.watcher = watcher

	if err := watcher.Add(w.projectDir); err != nil {
		watcher.Close()
		return err
	}

	go w.loop()
	return nil
}

// Stop terminates the watcher.
func (w *SessionFileWatcher) Stop() {
	w.stopOnce.Do(func() {
		if w.watcher != nil {
			w.watcher.Close()
		}
		<-w.done
	})
}

func (w *SessionFileWatcher) emit(path string) {
	if path == "" {
		return
	}
	if !w.predicate(path) {
		return
	}
	func() {
		defer func() { recover() }()
		w.callback(path)
	}()
}

func (w *SessionFileWatcher) loop() {
	defer close(w.done)

	for {
		select {
		case event, ok := <-w.watcher.Events:
			if !ok {
				return
			}
			switch {
			case event.Op&fsnotify.Create != 0:
				w.emit(event.Name)
			case event.Op&fsnotify.Write != 0:
				w.emit(event.Name)
			case event.Op&fsnotify.Rename != 0:
				// Rename: the new name is not available in fsnotify event
				// directly; on most systems only the old name fires.
				// We emit it for index/log files anyway.
				w.emit(event.Name)
			}
		case _, ok := <-w.watcher.Errors:
			if !ok {
				return
			}
			// Swallow errors like Python does.
		}
	}
}

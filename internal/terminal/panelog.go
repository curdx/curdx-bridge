package terminal

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	ccbruntime "github.com/anthropics/curdx-bridge/internal/runtime"
)

var (
	lastPaneLogClean   float64
	lastPaneLogCleanMu sync.Mutex
)

// PaneLogRoot returns the root directory for pane logs.
func PaneLogRoot() string {
	runDir := ccbruntime.RunDir()
	return filepath.Join(runDir, "pane-logs")
}

// PaneLogDir returns the directory for pane logs for a given backend and socket.
func PaneLogDir(backend string, socketName string) string {
	root := PaneLogRoot()
	if backend == "tmux" {
		if socketName != "" {
			safe := SanitizeFilename(socketName)
			if safe == "" {
				safe = "default"
			}
			return filepath.Join(root, "tmux-"+safe)
		}
		return filepath.Join(root, "tmux")
	}
	safeBackend := SanitizeFilename(backend)
	if safeBackend == "" {
		safeBackend = "pane"
	}
	return filepath.Join(root, safeBackend)
}

// PaneLogPathFor returns the log file path for a specific pane.
func PaneLogPathFor(paneID string, backend string, socketName string) string {
	pid := strings.TrimSpace(strings.ReplaceAll(paneID, "%", ""))
	safe := SanitizeFilename(pid)
	if safe == "" {
		safe = "pane"
	}
	return filepath.Join(PaneLogDir(backend, socketName), fmt.Sprintf("pane-%s.log", safe))
}

// MaybeTrimLog trims a log file to the last max_bytes if it exceeds the limit.
func MaybeTrimLog(path string) {
	maxBytes := EnvInt("CCB_PANE_LOG_MAX_BYTES", 10*1024*1024)
	if maxBytes <= 0 {
		return
	}

	info, err := os.Stat(path)
	if err != nil {
		return
	}
	size := info.Size()
	if size <= int64(maxBytes) {
		return
	}

	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	_, err = f.Seek(-int64(maxBytes), io.SeekEnd)
	if err != nil {
		return
	}
	tail, err := io.ReadAll(f)
	if err != nil {
		return
	}
	f.Close()

	dir := filepath.Dir(path)
	os.MkdirAll(dir, 0o755)
	tmpFile, err := os.CreateTemp(dir, fmt.Sprintf(".%s.*.tmp", filepath.Base(path)))
	if err != nil {
		return
	}
	tmpName := tmpFile.Name()
	defer os.Remove(tmpName)

	_, err = tmpFile.Write(tail)
	tmpFile.Close()
	if err != nil {
		return
	}
	os.Rename(tmpName, path)
}

// CleanupPaneLogs removes old pane log files based on TTL and max file count.
func CleanupPaneLogs(dirPath string) {
	lastPaneLogCleanMu.Lock()
	intervalS := EnvFloat("CCB_PANE_LOG_CLEAN_INTERVAL_S", 600.0)
	now := float64(time.Now().Unix())
	if intervalS > 0 && (now-lastPaneLogClean) < intervalS {
		lastPaneLogCleanMu.Unlock()
		return
	}
	lastPaneLogClean = now
	lastPaneLogCleanMu.Unlock()

	ttlDays := EnvInt("CCB_PANE_LOG_TTL_DAYS", 7)
	maxFiles := EnvInt("CCB_PANE_LOG_MAX_FILES", 200)
	if ttlDays <= 0 && maxFiles <= 0 {
		return
	}

	info, err := os.Stat(dirPath)
	if err != nil || !info.IsDir() {
		return
	}

	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return
	}

	type fileInfo struct {
		path  string
		mtime time.Time
		name  string
	}
	var files []fileInfo
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(dirPath, entry.Name())
		fi, err := entry.Info()
		if err != nil {
			continue
		}
		files = append(files, fileInfo{path: path, mtime: fi.ModTime(), name: entry.Name()})
	}

	// Remove files older than TTL.
	if ttlDays > 0 {
		cutoff := time.Now().Add(-time.Duration(ttlDays) * 24 * time.Hour)
		var remaining []fileInfo
		for _, f := range files {
			if f.mtime.Before(cutoff) {
				os.Remove(f.path)
			} else {
				remaining = append(remaining, f)
			}
		}
		files = remaining
	}

	// Enforce max file count.
	if maxFiles > 0 && len(files) > maxFiles {
		// Sort by mtime ascending (oldest first).
		sort.Slice(files, func(i, j int) bool {
			return files[i].mtime.Before(files[j].mtime)
		})
		extra := len(files) - maxFiles
		for _, f := range files[:extra] {
			os.Remove(f.path)
		}
	}
}

// EnsurePaneLog ensures tmux pipe-pane logging is enabled for a pane.
// Returns the log path.
func (t *TmuxBackend) EnsurePaneLog(paneID string) string {
	pid := strings.TrimSpace(paneID)
	if pid == "" {
		return ""
	}
	logPath := t.PaneLogPath(paneID)
	if logPath == "" {
		return ""
	}

	CleanupPaneLogs(filepath.Dir(logPath))
	os.MkdirAll(filepath.Dir(logPath), 0o755)
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY, 0o644)
	if err == nil {
		f.Close()
	}

	// Use tee (no shell redirection) so tmux can exec reliably.
	cmd := fmt.Sprintf("tee -a %s", shellQuote(logPath))
	t.tmuxRun([]string{"pipe-pane", "-o", "-t", pid, cmd}, false, false, nil, 0)

	MaybeTrimLog(logPath)

	t.paneLogInfoMu.Lock()
	if t.paneLogInfo == nil {
		t.paneLogInfo = make(map[string]float64)
	}
	t.paneLogInfo[pid] = float64(time.Now().Unix())
	t.paneLogInfoMu.Unlock()

	return logPath
}

// PaneLogPath returns the log path for a tmux pane.
func (t *TmuxBackend) PaneLogPath(paneID string) string {
	pid := strings.TrimSpace(paneID)
	if pid == "" {
		return ""
	}
	return PaneLogPathFor(pid, "tmux", t.socketName)
}

// RefreshPaneLogs reattaches pipe-pane for known panes.
func (t *TmuxBackend) RefreshPaneLogs() {
	t.paneLogInfoMu.Lock()
	pids := make([]string, 0, len(t.paneLogInfo))
	for pid := range t.paneLogInfo {
		pids = append(pids, pid)
	}
	t.paneLogInfoMu.Unlock()

	for _, pid := range pids {
		if !t.IsAlive(pid) {
			continue
		}
		result, err := t.tmuxRun([]string{"display-message", "-p", "-t", pid, "#{pane_pipe}"}, false, true, nil, 0)
		if err == nil && result != nil && strings.TrimSpace(result.Stdout) == "1" {
			continue
		}
		t.EnsurePaneLog(pid)
	}
}

// ResetPaneLogCleanTimer resets the cleanup timer (for testing).
func ResetPaneLogCleanTimer() {
	lastPaneLogCleanMu.Lock()
	lastPaneLogClean = 0
	lastPaneLogCleanMu.Unlock()
}

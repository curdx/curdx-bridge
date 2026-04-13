// Package runtime provides daemon runtime utilities: paths, logging, tokens.
// Source: claude_code_bridge/lib/askd_runtime.py
package runtime

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/curdx/curdx-bridge/internal/envutil"
)

// RunDir returns the daemon run directory, respecting CURDX_RUN_DIR and XDG_CACHE_HOME.
func RunDir() string {
	override := strings.TrimSpace(os.Getenv("CURDX_RUN_DIR"))
	if override != "" {
		// Expand ~ prefix
		if strings.HasPrefix(override, "~/") || override == "~" {
			home, _ := os.UserHomeDir()
			override = filepath.Join(home, override[1:])
		}
		return override
	}

	xdgCache := strings.TrimSpace(os.Getenv("XDG_CACHE_HOME"))
	if xdgCache != "" {
		return filepath.Join(xdgCache, "curdx")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache", "curdx")
}

// StateFilePath returns the path for a state file (appends .json if needed).
func StateFilePath(name string) string {
	if strings.HasSuffix(name, ".json") {
		return filepath.Join(RunDir(), name)
	}
	return filepath.Join(RunDir(), name+".json")
}

// LogPath returns the path for a log file (appends .log if needed).
func LogPath(name string) string {
	if strings.HasSuffix(name, ".log") {
		return filepath.Join(RunDir(), name)
	}
	return filepath.Join(RunDir(), name+".log")
}

var (
	lastLogShrinkCheck   = make(map[string]time.Time)
	lastLogShrinkCheckMu sync.Mutex
)

// maybeShrinkLog keeps daemon logs from growing unbounded.
// Truncates to last N bytes when file exceeds CURDX_LOG_MAX_BYTES.
func maybeShrinkLog(path string) {
	maxBytes := envutil.EnvInt("CURDX_LOG_MAX_BYTES", 2*1024*1024) // 2 MiB default
	if maxBytes <= 0 {
		return
	}

	intervalS := envutil.EnvInt("CURDX_LOG_SHRINK_CHECK_INTERVAL_S", 10)
	if intervalS < 0 {
		intervalS = 0
	}

	now := time.Now()
	lastLogShrinkCheckMu.Lock()
	last := lastLogShrinkCheck[path]
	if intervalS > 0 && now.Sub(last).Seconds() < float64(intervalS) {
		lastLogShrinkCheckMu.Unlock()
		return
	}
	lastLogShrinkCheck[path] = now
	lastLogShrinkCheckMu.Unlock()

	info, err := os.Stat(path)
	if err != nil {
		return
	}
	size := info.Size()
	if size <= int64(maxBytes) {
		return
	}

	// Read the tail
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

	// Write via temp file + rename
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

// WriteLog appends a log message to the given path, creating directories as needed.
func WriteLog(path, msg string) {
	defer func() { recover() }() // swallow panics like Python's bare except
	maybeShrinkLog(path)
	dir := filepath.Dir(path)
	os.MkdirAll(dir, 0o755)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	f.WriteString(strings.TrimRight(msg, " \t\r\n") + "\n")
}

// RandomToken returns a random 32-character hex token (16 random bytes).
func RandomToken() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// NormalizeConnectHost normalizes a daemon bind address for client connections.
func NormalizeConnectHost(host string) string {
	host = strings.TrimSpace(host)
	if host == "" || host == "0.0.0.0" {
		return "127.0.0.1"
	}
	if host == "::" || host == "[::]" {
		return "::1"
	}
	return host
}

// GetDaemonWorkDir reads the daemon's work_dir from its state file.
// Returns empty string if not available.
func GetDaemonWorkDir(stateFileName string) string {
	if stateFileName == "" {
		stateFileName = "cxb-askd.json"
	}
	statePath := StateFilePath(stateFileName)
	data, err := os.ReadFile(statePath)
	if err != nil {
		return ""
	}
	var state map[string]interface{}
	if err := json.Unmarshal(data, &state); err != nil {
		return ""
	}
	workDir, ok := state["work_dir"]
	if !ok {
		return ""
	}
	s, ok := workDir.(string)
	if !ok || strings.TrimSpace(s) == "" {
		return ""
	}
	return strings.TrimSpace(s)
}

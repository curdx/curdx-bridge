// Package session provides per-provider project session management.
// Source: claude_code_bridge/lib/*askd_session.py
//
// Each provider has a session struct wrapping a JSON file.  The common
// ensure-pane logic (multi-level fallback: pane_id -> title marker -> respawn)
// lives in shared helpers; provider-specific fields are in their own files.
package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/curdx/curdx-bridge/internal/projectid"
	"github.com/curdx/curdx-bridge/internal/providers"
	"github.com/curdx/curdx-bridge/internal/sessionutil"
)

// TerminalBackend abstracts terminal multiplexer operations for session management.
type TerminalBackend interface {
	IsAlive(paneID string) bool
	FindPaneByTitleMarker(marker string, cwdHint string) string
	PaneBelongsToCWD(paneID, workDir string) bool
	EnsurePaneLog(paneID string) string
	RespawnPane(paneID string, cmd, cwd, stderrLogPath string, remainOnExit bool) error
	SaveCrashLog(paneID, logPath string, lines int) error
}

// GetBackendFunc is the function used to obtain a TerminalBackend from session data.
// Set externally to avoid circular dependency with the terminal package.
var GetBackendFunc func(data map[string]any) TerminalBackend

// nowStr returns the current time formatted for session file timestamps.
func nowStr() string {
	return time.Now().Format("2006-01-02 15:04:05")
}

// readJSON reads a JSON file, returning an empty map on any error.
func readJSON(path string) map[string]any {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	// Strip UTF-8 BOM if present.
	if len(raw) >= 3 && raw[0] == 0xEF && raw[1] == 0xBB && raw[2] == 0xBF {
		raw = raw[3:]
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil
	}
	if len(obj) == 0 {
		return nil
	}
	return obj
}

// writeBack persists session data to the session file.
func writeBack(sessionFile string, data map[string]any) {
	payload, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return
	}
	sessionutil.SafeWriteSession(sessionFile, string(payload)+"\n")
}

// getString safely extracts a trimmed string from a map key.
func getString(data map[string]any, key string) string {
	v, ok := data[key]
	if !ok || v == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprintf("%v", v))
}

// getTerminal extracts the terminal type, defaulting to the platform-native
// multiplexer ("wezterm" on Windows, "tmux" elsewhere).
func getTerminal(data map[string]any) string {
	t := getString(data, "terminal")
	if t == "" {
		if runtime.GOOS == "windows" {
			return "wezterm"
		}
		return "tmux"
	}
	return t
}

// getPaneID extracts the pane_id, falling back to tmux_session for tmux terminals.
func getPaneID(data map[string]any) string {
	v := getString(data, "pane_id")
	if v == "" && getTerminal(data) == "tmux" {
		v = getString(data, "tmux_session")
	}
	return v
}

// getWorkDir extracts work_dir with a fallback to the session file's parent directory.
func getWorkDir(data map[string]any, sessionFile string) string {
	v := getString(data, "work_dir")
	if v != "" {
		return v
	}
	return filepath.Dir(sessionFile)
}

// getRuntimeDir extracts runtime_dir with a fallback to the session file's parent directory.
func getRuntimeDir(data map[string]any, sessionFile string) string {
	v := getString(data, "runtime_dir")
	if v != "" {
		return v
	}
	return filepath.Dir(sessionFile)
}

// attachPaneLog is a best-effort call to backend.EnsurePaneLog.
func attachPaneLog(backend TerminalBackend, paneID string) {
	if backend == nil {
		return
	}
	_ = backend.EnsurePaneLog(paneID)
}

// EnsurePaneResult holds the result of an ensure_pane call.
type EnsurePaneResult struct {
	OK     bool
	PaneID string
	Err    string
}

// ensurePane implements the shared multi-level fallback logic:
// 1. Check existing pane_id + cwd ownership
// 2. Resolve by title marker
// 3. Respawn dead tmux panes
func ensurePane(sessionFile string, data map[string]any, hasRespawn bool) EnsurePaneResult {
	if GetBackendFunc == nil {
		return EnsurePaneResult{OK: false, Err: "Terminal backend not available"}
	}
	backend := GetBackendFunc(data)
	if backend == nil {
		return EnsurePaneResult{OK: false, Err: "Terminal backend not available"}
	}

	paneID := getPaneID(data)
	marker := getString(data, "pane_title_marker")
	workDir := getWorkDir(data, sessionFile)
	terminal := getTerminal(data)

	// Step 1: existing pane_id is alive.
	if paneID != "" && backend.IsAlive(paneID) {
		// WezTerm multi-window check: verify pane belongs to this project.
		if backend.PaneBelongsToCWD(paneID, workDir) {
			// Try marker re-resolution for more accurate pane_id.
			if marker != "" {
				resolved := backend.FindPaneByTitleMarker(marker, workDir)
				if resolved != "" && resolved != paneID && backend.IsAlive(resolved) {
					data["pane_id"] = resolved
					data["updated_at"] = nowStr()
					writeBack(sessionFile, data)
					attachPaneLog(backend, resolved)
					return EnsurePaneResult{OK: true, PaneID: resolved}
				}
			}
			attachPaneLog(backend, paneID)
			return EnsurePaneResult{OK: true, PaneID: paneID}
		}
		// Pane alive but belongs to wrong project -- fall through to marker resolution.
	}

	// Step 2: resolve by title marker.
	resolved := ""
	if marker != "" {
		resolved = backend.FindPaneByTitleMarker(marker, workDir)
		if resolved != "" && backend.IsAlive(resolved) {
			data["pane_id"] = resolved
			data["updated_at"] = nowStr()
			writeBack(sessionFile, data)
			attachPaneLog(backend, resolved)
			return EnsurePaneResult{OK: true, PaneID: resolved}
		}
	}

	// Step 3: tmux respawn if applicable.
	if hasRespawn && terminal == "tmux" {
		startCmd := getString(data, "start_cmd")
		// Codex has a special codex_start_cmd override.
		if csCmd := getString(data, "codex_start_cmd"); csCmd != "" {
			startCmd = csCmd
		}
		if startCmd != "" {
			runtimeDir := getRuntimeDir(data, sessionFile)

			// Determine targets to try respawning.
			targets := []string{}
			if resolved != "" {
				targets = append(targets, resolved)
			}
			if paneID != "" {
				targets = append(targets, paneID)
			}

			var lastErr string
			for _, target := range targets {
				if !strings.HasPrefix(target, "%") {
					continue
				}
				// Save crash log.
				os.MkdirAll(runtimeDir, 0o755)
				crashLog := filepath.Join(runtimeDir, fmt.Sprintf("pane-crash-%d.log", time.Now().Unix()))
				_ = backend.SaveCrashLog(target, crashLog, 1000)

				// Respawn.
				if err := backend.RespawnPane(target, startCmd, workDir, "", true); err != nil {
					lastErr = err.Error()
					continue
				}
				if backend.IsAlive(target) {
					data["pane_id"] = target
					data["updated_at"] = nowStr()
					writeBack(sessionFile, data)
					attachPaneLog(backend, target)
					return EnsurePaneResult{OK: true, PaneID: target}
				}
				lastErr = "respawn did not revive pane"
			}
			if lastErr != "" {
				return EnsurePaneResult{OK: false, Err: "Pane not alive and respawn failed: " + lastErr}
			}
		}
	}

	return EnsurePaneResult{OK: false, Err: "Pane not alive: " + paneID}
}

// findProjectSessionFile wraps sessionutil.FindActiveProjectSessionFile with instance support.
// It skips sessions marked active:false, allowing fallback to parent directories.
func findProjectSessionFile(workDir, baseFilename, instance string) string {
	filename := providers.SessionFilenameForInstance(baseFilename, instance)
	return sessionutil.FindActiveProjectSessionFile(workDir, filename)
}

// computeSessionKey computes the routing key for a provider session.
func computeSessionKey(prefix string, data map[string]any, sessionFile, instance string) string {
	pid := getString(data, "curdx_project_id")
	if pid == "" {
		workDir := getWorkDir(data, sessionFile)
		pid = projectid.ComputeCURDXProjectID(workDir)
	}
	if instance != "" {
		prefix = prefix + ":" + instance
	}
	if pid != "" {
		return prefix + ":" + pid
	}
	return prefix + ":unknown"
}

package session

import (
	"path/filepath"
	"strings"
	"sync"

	"github.com/anthropics/curdx-bridge/internal/projectid"
	"github.com/anthropics/curdx-bridge/internal/sessionutil"
)

// ClaudeProjectSession represents a Claude provider session.
type ClaudeProjectSession struct {
	SessionFile string
	mu          sync.RWMutex
	Data        map[string]interface{}
}

// GetDataKey returns the value for a single key from the Data map (thread-safe).
func (s *ClaudeProjectSession) GetDataKey(key string) interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Data[key]
}

// SetDataKey sets a single key in the Data map (thread-safe).
func (s *ClaudeProjectSession) SetDataKey(key string, value interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Data == nil {
		s.Data = make(map[string]interface{})
	}
	s.Data[key] = value
}

// GetDataSnapshot returns a shallow copy of the Data map (thread-safe).
// Callers that need a consistent read of multiple keys should use this.
func (s *ClaudeProjectSession) GetDataSnapshot() map[string]interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cp := make(map[string]interface{}, len(s.Data))
	for k, v := range s.Data {
		cp[k] = v
	}
	return cp
}

func (s *ClaudeProjectSession) Terminal() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return getTerminal(s.Data)
}
func (s *ClaudeProjectSession) PaneID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return getPaneID(s.Data)
}
func (s *ClaudeProjectSession) PaneTitleMarker() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return getString(s.Data, "pane_title_marker")
}
func (s *ClaudeProjectSession) WorkDir() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return getWorkDir(s.Data, s.SessionFile)
}

func (s *ClaudeProjectSession) ClaudeSessionID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v := getString(s.Data, "claude_session_id")
	if v == "" {
		v = getString(s.Data, "session_id")
	}
	return v
}

func (s *ClaudeProjectSession) ClaudeSessionPath() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return getString(s.Data, "claude_session_path")
}

// EnsurePane ensures the claude pane is alive.
// Note: Claude does NOT support tmux respawn (no start_cmd pattern).
func (s *ClaudeProjectSession) EnsurePane() EnsurePaneResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	return ensurePane(s.SessionFile, s.Data, false)
}

// UpdateClaudeBinding updates the claude session path and ID bindings.
func (s *ClaudeProjectSession) UpdateClaudeBinding(sessionPath, sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	oldPath := getString(s.Data, "claude_session_path")
	oldID := getString(s.Data, "claude_session_id")
	updated := false
	sessionPathStr := sessionPath

	if sessionPathStr != "" && s.Data["claude_session_path"] != sessionPathStr {
		s.Data["claude_session_path"] = sessionPathStr
		updated = true
	}

	if sessionID != "" && s.Data["claude_session_id"] != sessionID {
		s.Data["claude_session_id"] = sessionID
		updated = true
	}

	if updated {
		newID := sessionID
		if newID == "" && sessionPathStr != "" {
			base := filepath.Base(sessionPathStr)
			ext := filepath.Ext(base)
			if ext != "" {
				newID = base[:len(base)-len(ext)]
			} else {
				newID = base
			}
		}
		if oldID != "" && oldID != newID {
			s.Data["old_claude_session_id"] = oldID
		}
		if oldPath != "" && (oldPath != sessionPathStr || (oldID != "" && oldID != newID)) {
			s.Data["old_claude_session_path"] = oldPath
		}
		if oldPath != "" || oldID != "" {
			s.Data["old_updated_at"] = nowStr()
		}
		s.Data["updated_at"] = nowStr()
		if active, ok := s.Data["active"]; ok && active == false {
			s.Data["active"] = true
		}
		s.writeBackLocked()
	}
}

// WriteBack persists session data, ensuring work_dir fields are populated.
func (s *ClaudeProjectSession) WriteBack() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writeBackLocked()
}

// writeBackLocked is the internal WriteBack that assumes the lock is already held.
func (s *ClaudeProjectSession) writeBackLocked() {
	ensureWorkDirFields(s.Data, s.SessionFile, "")
	writeBack(s.SessionFile, s.Data)
}

// inferWorkDirFromSessionFile infers a work_dir from the session file path.
func inferWorkDirFromSessionFile(sessionFile string) string {
	parent := filepath.Dir(sessionFile)
	base := filepath.Base(parent)
	if base == ".ccb" || base == ".ccb_config" {
		return filepath.Dir(parent)
	}
	return parent
}

// ensureWorkDirFields populates work_dir, work_dir_norm, and ccb_project_id if missing.
func ensureWorkDirFields(data map[string]interface{}, sessionFile, fallbackWorkDir string) {
	if data == nil {
		return
	}

	workDir := getString(data, "work_dir")
	if workDir == "" {
		if fallbackWorkDir != "" {
			workDir = fallbackWorkDir
		} else {
			workDir = inferWorkDirFromSessionFile(sessionFile)
		}
		data["work_dir"] = workDir
	}

	workDirNorm := getString(data, "work_dir_norm")
	if workDirNorm == "" {
		data["work_dir_norm"] = projectid.NormalizeWorkDir(workDir)
	}

	pid := getString(data, "ccb_project_id")
	if pid == "" {
		computed := projectid.ComputeCCBProjectID(workDir)
		if computed != "" {
			data["ccb_project_id"] = computed
		}
	}
}

// ResolveClaudeSessionFunc is a pluggable resolver for the default (non-instance) session.
// It returns (sessionFile, data) or ("", nil) if not found.
// Set externally to avoid circular dependency with the clauderesolver package.
var ResolveClaudeSessionFunc func(workDir string) (string, map[string]interface{})

// FindClaudeSessionFile finds a claude session file for the given work directory.
func FindClaudeSessionFile(workDir, instance string) string {
	return findProjectSessionFile(workDir, ".claude-session", instance)
}

// LoadClaudeSession loads a claude project session.
func LoadClaudeSession(workDir, instance string) *ClaudeProjectSession {
	// When an instance is specified, use instance-specific session file.
	if instance != "" {
		sessionFile := FindClaudeSessionFile(workDir, instance)
		if sessionFile == "" {
			return nil
		}
		data := readJSON(sessionFile)
		if data == nil {
			return nil
		}
		if getString(data, "work_dir") == "" {
			data["work_dir"] = workDir
		}
		pid := getString(data, "ccb_project_id")
		if pid == "" {
			wd := getString(data, "work_dir")
			if wd == "" {
				wd = workDir
			}
			data["ccb_project_id"] = projectid.ComputeCCBProjectID(wd)
		}
		ensureWorkDirFields(data, sessionFile, workDir)
		return &ClaudeProjectSession{SessionFile: sessionFile, Data: data}
	}

	// Default behavior: use ResolveClaudeSessionFunc if available.
	if ResolveClaudeSessionFunc != nil {
		sessionFile, data := ResolveClaudeSessionFunc(workDir)
		if data == nil || len(data) == 0 {
			return nil
		}
		if getString(data, "work_dir") == "" {
			data["work_dir"] = workDir
		}
		pid := getString(data, "ccb_project_id")
		if pid == "" {
			wd := getString(data, "work_dir")
			if wd == "" {
				wd = workDir
			}
			data["ccb_project_id"] = projectid.ComputeCCBProjectID(wd)
		}
		if sessionFile == "" {
			configDir := sessionutil.ResolveProjectConfigDir(workDir)
			sessionFile = filepath.Join(configDir, ".claude-session")
		}
		ensureWorkDirFields(data, sessionFile, workDir)
		return &ClaudeProjectSession{SessionFile: sessionFile, Data: data}
	}

	// Fallback: direct file lookup.
	sessionFile := FindClaudeSessionFile(workDir, "")
	if sessionFile == "" {
		return nil
	}
	data := readJSON(sessionFile)
	if data == nil {
		return nil
	}
	if getString(data, "work_dir") == "" {
		data["work_dir"] = workDir
	}
	pid := getString(data, "ccb_project_id")
	if pid == "" {
		wd := strings.TrimSpace(getString(data, "work_dir"))
		if wd == "" {
			wd = workDir
		}
		data["ccb_project_id"] = projectid.ComputeCCBProjectID(wd)
	}
	ensureWorkDirFields(data, sessionFile, workDir)
	return &ClaudeProjectSession{SessionFile: sessionFile, Data: data}
}

// ComputeClaudeSessionKey returns the routing key for a claude session.
func ComputeClaudeSessionKey(s *ClaudeProjectSession, instance string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return computeSessionKey("claude", s.Data, s.SessionFile, instance)
}

package session

import (
	"path/filepath"
)

// CodexProjectSession represents a Codex provider session.
type CodexProjectSession struct {
	SessionFile string
	Data        map[string]interface{}
}

func (s *CodexProjectSession) Terminal() string           { return getTerminal(s.Data) }
func (s *CodexProjectSession) PaneID() string             { return getPaneID(s.Data) }
func (s *CodexProjectSession) PaneTitleMarker() string    { return getString(s.Data, "pane_title_marker") }
func (s *CodexProjectSession) CodexSessionPath() string   { return getString(s.Data, "codex_session_path") }
func (s *CodexProjectSession) CodexSessionID() string     { return getString(s.Data, "codex_session_id") }
func (s *CodexProjectSession) WorkDir() string            { return getWorkDir(s.Data, s.SessionFile) }
func (s *CodexProjectSession) RuntimeDir() string         { return getRuntimeDir(s.Data, s.SessionFile) }

func (s *CodexProjectSession) StartCmd() string {
	// Prefer explicit codex_start_cmd when present.
	cmd := getString(s.Data, "codex_start_cmd")
	if cmd != "" {
		return cmd
	}
	return getString(s.Data, "start_cmd")
}

// EnsurePane ensures the codex pane is alive, with full multi-level fallback including tmux respawn.
func (s *CodexProjectSession) EnsurePane() EnsurePaneResult {
	return ensurePane(s.SessionFile, s.Data, true)
}

// UpdateCodexLogBinding updates the codex session path and ID bindings.
func (s *CodexProjectSession) UpdateCodexLogBinding(logPath, sessionID string) {
	oldPath := getString(s.Data, "codex_session_path")
	oldID := getString(s.Data, "codex_session_id")

	updated := false
	logPathStr := logPath
	if logPathStr != "" && s.Data["codex_session_path"] != logPathStr {
		s.Data["codex_session_path"] = logPathStr
		updated = true
	}
	if sessionID != "" && s.Data["codex_session_id"] != sessionID {
		s.Data["codex_session_id"] = sessionID
		s.Data["codex_start_cmd"] = "codex resume " + sessionID
		updated = true
	}

	if updated {
		newID := sessionID
		if newID == "" && logPathStr != "" {
			base := filepath.Base(logPathStr)
			ext := filepath.Ext(base)
			if ext != "" {
				newID = base[:len(base)-len(ext)]
			} else {
				newID = base
			}
		}
		if oldID != "" && oldID != newID {
			s.Data["old_codex_session_id"] = oldID
		}
		if oldPath != "" && (oldPath != logPathStr || (oldID != "" && oldID != newID)) {
			s.Data["old_codex_session_path"] = oldPath
		}
		if oldPath != "" || oldID != "" {
			s.Data["old_updated_at"] = nowStr()
		}

		s.Data["updated_at"] = nowStr()
		if active, ok := s.Data["active"]; ok && active == false {
			s.Data["active"] = true
		}
		writeBack(s.SessionFile, s.Data)
	}
}

// WriteBack persists session data.
func (s *CodexProjectSession) WriteBack() {
	writeBack(s.SessionFile, s.Data)
}

// FindCodexSessionFile finds a codex session file for the given work directory.
func FindCodexSessionFile(workDir, instance string) string {
	return findProjectSessionFile(workDir, ".codex-session", instance)
}

// LoadCodexSession loads a codex project session.
func LoadCodexSession(workDir, instance string) *CodexProjectSession {
	sessionFile := FindCodexSessionFile(workDir, instance)
	if sessionFile == "" {
		return nil
	}
	data := readJSON(sessionFile)
	if data == nil {
		return nil
	}
	return &CodexProjectSession{SessionFile: sessionFile, Data: data}
}

// ComputeCodexSessionKey returns the routing key for a codex session.
func ComputeCodexSessionKey(s *CodexProjectSession, instance string) string {
	return computeSessionKey("codex", s.Data, s.SessionFile, instance)
}

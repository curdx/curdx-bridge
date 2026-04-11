package session

import (
	"path/filepath"

	"github.com/curdx/curdx-bridge/internal/projectid"
)

// GeminiProjectSession represents a Gemini provider session.
type GeminiProjectSession struct {
	SessionFile string
	Data        map[string]interface{}
}

func (s *GeminiProjectSession) Terminal() string           { return getTerminal(s.Data) }
func (s *GeminiProjectSession) PaneID() string             { return getPaneID(s.Data) }
func (s *GeminiProjectSession) PaneTitleMarker() string    { return getString(s.Data, "pane_title_marker") }
func (s *GeminiProjectSession) GeminiSessionID() string    { return getString(s.Data, "gemini_session_id") }
func (s *GeminiProjectSession) GeminiSessionPath() string  { return getString(s.Data, "gemini_session_path") }
func (s *GeminiProjectSession) WorkDir() string            { return getWorkDir(s.Data, s.SessionFile) }
func (s *GeminiProjectSession) RuntimeDir() string         { return getRuntimeDir(s.Data, s.SessionFile) }
func (s *GeminiProjectSession) StartCmd() string           { return getString(s.Data, "start_cmd") }

// EnsurePane ensures the gemini pane is alive, with full multi-level fallback including tmux respawn.
func (s *GeminiProjectSession) EnsurePane() EnsurePaneResult {
	return ensurePane(s.SessionFile, s.Data, true)
}

// UpdateGeminiBinding updates the gemini session path and ID bindings.
func (s *GeminiProjectSession) UpdateGeminiBinding(sessionPath, sessionID string) {
	oldPath := getString(s.Data, "gemini_session_path")
	oldID := getString(s.Data, "gemini_session_id")
	updated := false

	if sessionPath != "" {
		if s.Data["gemini_session_path"] != sessionPath {
			s.Data["gemini_session_path"] = sessionPath
			updated = true
		}
		// Best-effort: store Gemini project hash for debugging.
		projectHash := filepath.Base(filepath.Dir(filepath.Dir(sessionPath)))
		if projectHash != "" && s.Data["gemini_project_hash"] != projectHash {
			s.Data["gemini_project_hash"] = projectHash
			updated = true
		}
	}

	if sessionID != "" && s.Data["gemini_session_id"] != sessionID {
		s.Data["gemini_session_id"] = sessionID
		updated = true
	}

	// Ensure curdx_project_id exists.
	pid := getString(s.Data, "curdx_project_id")
	if pid == "" {
		workDir := s.WorkDir()
		computed := projectid.ComputeCURDXProjectID(workDir)
		if computed != "" {
			s.Data["curdx_project_id"] = computed
			updated = true
		}
	}

	if updated {
		newID := sessionID
		if newID == "" && sessionPath != "" {
			base := filepath.Base(sessionPath)
			ext := filepath.Ext(base)
			if ext != "" {
				newID = base[:len(base)-len(ext)]
			} else {
				newID = base
			}
		}
		if oldID != "" && oldID != newID {
			s.Data["old_gemini_session_id"] = oldID
		}
		if oldPath != "" && (oldPath != sessionPath || (oldID != "" && oldID != newID)) {
			s.Data["old_gemini_session_path"] = oldPath
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
func (s *GeminiProjectSession) WriteBack() {
	writeBack(s.SessionFile, s.Data)
}

// FindGeminiSessionFile finds a gemini session file for the given work directory.
func FindGeminiSessionFile(workDir, instance string) string {
	return findProjectSessionFile(workDir, ".gemini-session", instance)
}

// LoadGeminiSession loads a gemini project session.
func LoadGeminiSession(workDir, instance string) *GeminiProjectSession {
	sessionFile := FindGeminiSessionFile(workDir, instance)
	if sessionFile == "" {
		return nil
	}
	data := readJSON(sessionFile)
	if data == nil {
		return nil
	}
	return &GeminiProjectSession{SessionFile: sessionFile, Data: data}
}

// ComputeGeminiSessionKey returns the routing key for a gemini session.
func ComputeGeminiSessionKey(s *GeminiProjectSession, instance string) string {
	return computeSessionKey("gemini", s.Data, s.SessionFile, instance)
}

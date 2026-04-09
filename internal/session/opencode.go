package session

import (
	"strings"

	"github.com/anthropics/curdx-bridge/internal/projectid"
)

// OpenCodeProjectSession represents an OpenCode provider session.
type OpenCodeProjectSession struct {
	SessionFile string
	Data        map[string]interface{}
}

func (s *OpenCodeProjectSession) Terminal() string           { return getTerminal(s.Data) }
func (s *OpenCodeProjectSession) PaneID() string             { return getPaneID(s.Data) }
func (s *OpenCodeProjectSession) PaneTitleMarker() string    { return getString(s.Data, "pane_title_marker") }
func (s *OpenCodeProjectSession) WorkDir() string            { return getWorkDir(s.Data, s.SessionFile) }
func (s *OpenCodeProjectSession) RuntimeDir() string         { return getRuntimeDir(s.Data, s.SessionFile) }
func (s *OpenCodeProjectSession) StartCmd() string           { return getString(s.Data, "start_cmd") }

// SessionID returns the CCB session ID (legacy compat).
func (s *OpenCodeProjectSession) SessionID() string {
	v := getString(s.Data, "ccb_session_id")
	if v == "" {
		v = getString(s.Data, "session_id")
	}
	return v
}

// CCBSessionID is an alias for SessionID.
func (s *OpenCodeProjectSession) CCBSessionID() string {
	return s.SessionID()
}

// OpenCodeSessionID returns OpenCode's internal session id (typically "ses_...").
func (s *OpenCodeProjectSession) OpenCodeSessionID() string {
	sid := getString(s.Data, "opencode_session_id")
	if sid == "" {
		sid = getString(s.Data, "opencode_storage_session_id")
	}
	if sid != "" {
		return sid
	}
	legacy := getString(s.Data, "session_id")
	if strings.HasPrefix(legacy, "ses_") {
		return legacy
	}
	return ""
}

// OpenCodeSessionIDFilter returns the session ID if it starts with "ses_", else empty.
func (s *OpenCodeProjectSession) OpenCodeSessionIDFilter() string {
	sid := s.OpenCodeSessionID()
	if strings.HasPrefix(sid, "ses_") {
		return sid
	}
	return ""
}

// OpenCodeProjectID returns the opencode_project_id field.
func (s *OpenCodeProjectSession) OpenCodeProjectID() string {
	return getString(s.Data, "opencode_project_id")
}

// EnsurePane ensures the opencode pane is alive.
func (s *OpenCodeProjectSession) EnsurePane() EnsurePaneResult {
	return ensurePane(s.SessionFile, s.Data, true)
}

// UpdateOpenCodeBinding updates the opencode session and project bindings.
func (s *OpenCodeProjectSession) UpdateOpenCodeBinding(sessionID, projectID string) {
	oldID := getString(s.Data, "opencode_session_id")
	oldProject := getString(s.Data, "opencode_project_id")
	updated := false

	if sessionID != "" && getString(s.Data, "opencode_session_id") != sessionID {
		s.Data["opencode_session_id"] = sessionID
		updated = true
	}
	if projectID != "" && getString(s.Data, "opencode_project_id") != projectID {
		s.Data["opencode_project_id"] = projectID
		updated = true
	}

	// Ensure ccb_project_id exists.
	pid := getString(s.Data, "ccb_project_id")
	if pid == "" {
		workDir := s.WorkDir()
		computed := projectid.ComputeCCBProjectID(workDir)
		if computed != "" {
			s.Data["ccb_project_id"] = computed
			updated = true
		}
	}

	if updated {
		newID := sessionID
		newProject := projectID
		if oldID != "" && oldID != newID {
			s.Data["old_opencode_session_id"] = oldID
		}
		if oldProject != "" && oldProject != newProject {
			s.Data["old_opencode_project_id"] = oldProject
		}
		if oldID != "" || oldProject != "" {
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
func (s *OpenCodeProjectSession) WriteBack() {
	writeBack(s.SessionFile, s.Data)
}

// FindOpenCodeSessionFile finds an opencode session file for the given work directory.
func FindOpenCodeSessionFile(workDir, instance string) string {
	return findProjectSessionFile(workDir, ".opencode-session", instance)
}

// LoadOpenCodeSession loads an opencode project session.
func LoadOpenCodeSession(workDir, instance string) *OpenCodeProjectSession {
	sessionFile := FindOpenCodeSessionFile(workDir, instance)
	if sessionFile == "" {
		return nil
	}
	data := readJSON(sessionFile)
	if data == nil {
		return nil
	}
	return &OpenCodeProjectSession{SessionFile: sessionFile, Data: data}
}

// ComputeOpenCodeSessionKey returns the routing key for an opencode session.
func ComputeOpenCodeSessionKey(s *OpenCodeProjectSession, instance string) string {
	return computeSessionKey("opencode", s.Data, s.SessionFile, instance)
}

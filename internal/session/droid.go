package session

import (
	"path/filepath"

	"github.com/anthropics/curdx-bridge/internal/projectid"
)

// DroidProjectSession represents a Droid provider session.
type DroidProjectSession struct {
	SessionFile string
	Data        map[string]interface{}
}

func (s *DroidProjectSession) Terminal() string           { return getTerminal(s.Data) }
func (s *DroidProjectSession) PaneID() string             { return getPaneID(s.Data) }
func (s *DroidProjectSession) PaneTitleMarker() string    { return getString(s.Data, "pane_title_marker") }
func (s *DroidProjectSession) DroidSessionID() string     { return getString(s.Data, "droid_session_id") }
func (s *DroidProjectSession) DroidSessionPath() string   { return getString(s.Data, "droid_session_path") }
func (s *DroidProjectSession) WorkDir() string            { return getWorkDir(s.Data, s.SessionFile) }
func (s *DroidProjectSession) RuntimeDir() string         { return getRuntimeDir(s.Data, s.SessionFile) }
func (s *DroidProjectSession) StartCmd() string           { return getString(s.Data, "start_cmd") }

// EnsurePane ensures the droid pane is alive, with full multi-level fallback including tmux respawn.
func (s *DroidProjectSession) EnsurePane() EnsurePaneResult {
	return ensurePane(s.SessionFile, s.Data, true)
}

// UpdateDroidBinding updates the droid session path and ID bindings.
func (s *DroidProjectSession) UpdateDroidBinding(sessionPath, sessionID string) {
	oldPath := getString(s.Data, "droid_session_path")
	oldID := getString(s.Data, "droid_session_id")
	updated := false

	if sessionPath != "" {
		if getString(s.Data, "droid_session_path") != sessionPath {
			s.Data["droid_session_path"] = sessionPath
			updated = true
		}
	}

	if sessionID != "" && getString(s.Data, "droid_session_id") != sessionID {
		s.Data["droid_session_id"] = sessionID
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
		if newID == "" && sessionPath != "" {
			base := filepath.Base(sessionPath)
			ext := filepath.Ext(base)
			if ext != "" {
				newID = base[:len(base)-len(ext)]
			} else {
				newID = base
			}
		}
		sessionPathStr := sessionPath
		if oldID != "" && oldID != newID {
			s.Data["old_droid_session_id"] = oldID
		}
		if oldPath != "" && (oldPath != sessionPathStr || (oldID != "" && oldID != newID)) {
			s.Data["old_droid_session_path"] = oldPath
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
func (s *DroidProjectSession) WriteBack() {
	writeBack(s.SessionFile, s.Data)
}

// FindDroidSessionFile finds a droid session file for the given work directory.
func FindDroidSessionFile(workDir, instance string) string {
	return findProjectSessionFile(workDir, ".droid-session", instance)
}

// LoadDroidSession loads a droid project session.
func LoadDroidSession(workDir, instance string) *DroidProjectSession {
	sessionFile := FindDroidSessionFile(workDir, instance)
	if sessionFile == "" {
		return nil
	}
	data := readJSON(sessionFile)
	if data == nil {
		return nil
	}
	// Droid checks active == false.
	if active, ok := data["active"]; ok && active == false {
		return nil
	}
	return &DroidProjectSession{SessionFile: sessionFile, Data: data}
}

// ComputeDroidSessionKey returns the routing key for a droid session.
func ComputeDroidSessionKey(s *DroidProjectSession, instance string) string {
	return computeSessionKey("droid", s.Data, s.SessionFile, instance)
}

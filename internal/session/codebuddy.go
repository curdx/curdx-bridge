package session

// CodebuddyProjectSession represents a Tencent CodeBuddy CLI provider session.
// Simplified: no JSONL session binding, pane-log communication only.
type CodebuddyProjectSession struct {
	SessionFile string
	Data        map[string]interface{}
}

func (s *CodebuddyProjectSession) Terminal() string           { return getTerminal(s.Data) }
func (s *CodebuddyProjectSession) PaneID() string             { return getPaneID(s.Data) }
func (s *CodebuddyProjectSession) PaneTitleMarker() string    { return getString(s.Data, "pane_title_marker") }
func (s *CodebuddyProjectSession) WorkDir() string            { return getWorkDir(s.Data, s.SessionFile) }
func (s *CodebuddyProjectSession) RuntimeDir() string         { return getRuntimeDir(s.Data, s.SessionFile) }
func (s *CodebuddyProjectSession) StartCmd() string           { return getString(s.Data, "start_cmd") }

// EnsurePane ensures the codebuddy pane is alive, with full multi-level fallback including tmux respawn.
func (s *CodebuddyProjectSession) EnsurePane() EnsurePaneResult {
	return ensurePane(s.SessionFile, s.Data, true)
}

// WriteBack persists session data.
func (s *CodebuddyProjectSession) WriteBack() {
	writeBack(s.SessionFile, s.Data)
}

// FindCodebuddySessionFile finds a codebuddy session file for the given work directory.
func FindCodebuddySessionFile(workDir, instance string) string {
	return findProjectSessionFile(workDir, ".codebuddy-session", instance)
}

// LoadCodebuddySession loads a codebuddy project session.
func LoadCodebuddySession(workDir, instance string) *CodebuddyProjectSession {
	sessionFile := FindCodebuddySessionFile(workDir, instance)
	if sessionFile == "" {
		return nil
	}
	data := readJSON(sessionFile)
	if data == nil {
		return nil
	}
	if active, ok := data["active"]; ok && active == false {
		return nil
	}
	return &CodebuddyProjectSession{SessionFile: sessionFile, Data: data}
}

// ComputeCodebuddySessionKey returns the routing key for a codebuddy session.
func ComputeCodebuddySessionKey(s *CodebuddyProjectSession, instance string) string {
	return computeSessionKey("codebuddy", s.Data, s.SessionFile, instance)
}

package session

// CopilotProjectSession represents a GitHub Copilot CLI provider session.
// Simplified: no JSONL session binding, pane-log communication only.
type CopilotProjectSession struct {
	SessionFile string
	Data        map[string]interface{}
}

func (s *CopilotProjectSession) Terminal() string           { return getTerminal(s.Data) }
func (s *CopilotProjectSession) PaneID() string             { return getPaneID(s.Data) }
func (s *CopilotProjectSession) PaneTitleMarker() string    { return getString(s.Data, "pane_title_marker") }
func (s *CopilotProjectSession) WorkDir() string            { return getWorkDir(s.Data, s.SessionFile) }
func (s *CopilotProjectSession) RuntimeDir() string         { return getRuntimeDir(s.Data, s.SessionFile) }
func (s *CopilotProjectSession) StartCmd() string           { return getString(s.Data, "start_cmd") }

// EnsurePane ensures the copilot pane is alive, with full multi-level fallback including tmux respawn.
func (s *CopilotProjectSession) EnsurePane() EnsurePaneResult {
	return ensurePane(s.SessionFile, s.Data, true)
}

// WriteBack persists session data.
func (s *CopilotProjectSession) WriteBack() {
	writeBack(s.SessionFile, s.Data)
}

// FindCopilotSessionFile finds a copilot session file for the given work directory.
func FindCopilotSessionFile(workDir, instance string) string {
	return findProjectSessionFile(workDir, ".copilot-session", instance)
}

// LoadCopilotSession loads a copilot project session.
func LoadCopilotSession(workDir, instance string) *CopilotProjectSession {
	sessionFile := FindCopilotSessionFile(workDir, instance)
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
	return &CopilotProjectSession{SessionFile: sessionFile, Data: data}
}

// ComputeCopilotSessionKey returns the routing key for a copilot session.
func ComputeCopilotSessionKey(s *CopilotProjectSession, instance string) string {
	return computeSessionKey("copilot", s.Data, s.SessionFile, instance)
}

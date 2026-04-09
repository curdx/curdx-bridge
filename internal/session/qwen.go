package session

// QwenProjectSession represents a Qwen (qwen-code) CLI provider session.
// Simplified: no JSONL session binding, pane-log communication only.
type QwenProjectSession struct {
	SessionFile string
	Data        map[string]interface{}
}

func (s *QwenProjectSession) Terminal() string           { return getTerminal(s.Data) }
func (s *QwenProjectSession) PaneID() string             { return getPaneID(s.Data) }
func (s *QwenProjectSession) PaneTitleMarker() string    { return getString(s.Data, "pane_title_marker") }
func (s *QwenProjectSession) WorkDir() string            { return getWorkDir(s.Data, s.SessionFile) }
func (s *QwenProjectSession) RuntimeDir() string         { return getRuntimeDir(s.Data, s.SessionFile) }
func (s *QwenProjectSession) StartCmd() string           { return getString(s.Data, "start_cmd") }

// EnsurePane ensures the qwen pane is alive, with full multi-level fallback including tmux respawn.
func (s *QwenProjectSession) EnsurePane() EnsurePaneResult {
	return ensurePane(s.SessionFile, s.Data, true)
}

// WriteBack persists session data.
func (s *QwenProjectSession) WriteBack() {
	writeBack(s.SessionFile, s.Data)
}

// FindQwenSessionFile finds a qwen session file for the given work directory.
func FindQwenSessionFile(workDir, instance string) string {
	return findProjectSessionFile(workDir, ".qwen-session", instance)
}

// LoadQwenSession loads a qwen project session.
func LoadQwenSession(workDir, instance string) *QwenProjectSession {
	sessionFile := FindQwenSessionFile(workDir, instance)
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
	return &QwenProjectSession{SessionFile: sessionFile, Data: data}
}

// ComputeQwenSessionKey returns the routing key for a qwen session.
func ComputeQwenSessionKey(s *QwenProjectSession, instance string) string {
	return computeSessionKey("qwen", s.Data, s.SessionFile, instance)
}

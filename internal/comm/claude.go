// Claude communication module.
//
// Reads replies from ~/.claude/projects/<project-key>/<session-id>.jsonl and
// sends prompts by injecting text into the Claude pane via the configured backend.
//
// Source: claude_code_bridge/lib/claude_comm.py
package comm

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Claude projects root
// ---------------------------------------------------------------------------

// DefaultClaudeProjectsRoot returns the default Claude projects root.
func DefaultClaudeProjectsRoot() string {
	for _, env := range []string{"CLAUDE_PROJECTS_ROOT", "CLAUDE_PROJECT_ROOT"} {
		if v := strings.TrimSpace(os.Getenv(env)); v != "" {
			return expandHome(v)
		}
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "projects")
}

// ---------------------------------------------------------------------------
// Project key computation
// ---------------------------------------------------------------------------

var projectKeyRE = regexp.MustCompile(`[^A-Za-z0-9]`)

// ProjectKeyForPath computes the Claude project key for a filesystem path.
func ProjectKeyForPath(path string) string {
	return projectKeyRE.ReplaceAllString(path, "-")
}

func normalizeProjectPath(value string) string {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return ""
	}
	raw = expandHome(raw)
	abs, err := filepath.Abs(raw)
	if err == nil {
		raw = abs
	}
	raw = strings.ReplaceAll(raw, "\\", "/")
	raw = strings.TrimRight(raw, "/")
	return raw
}

func candidateProjectPaths(workDir string) []string {
	var candidates []string
	seen := make(map[string]bool)
	add := func(v string) {
		n := normalizeProjectPath(v)
		if n == "" || seen[n] {
			return
		}
		seen[n] = true
		candidates = append(candidates, n)
	}

	if envPwd := os.Getenv("PWD"); envPwd != "" {
		add(envPwd)
	}
	add(workDir)
	if resolved, err := filepath.EvalSymlinks(workDir); err == nil {
		add(resolved)
	}
	return candidates
}

func candidateProjectDirs(root, workDir string) []string {
	var candidates []string
	seen := make(map[string]bool)
	add := func(path string) {
		key := ProjectKeyForPath(path)
		if seen[key] {
			return
		}
		seen[key] = true
		candidates = append(candidates, filepath.Join(root, key))
	}

	if envPwd := os.Getenv("PWD"); envPwd != "" {
		add(envPwd)
	}
	add(workDir)
	if resolved, err := filepath.EvalSymlinks(workDir); err == nil {
		add(resolved)
	}
	return candidates
}

// ---------------------------------------------------------------------------
// Claude JSONL message extraction
// ---------------------------------------------------------------------------

// claudeExtractMessage extracts a message with the given role from a Claude JSONL entry.
func claudeExtractMessage(entry map[string]any, role string) string {
	if entry == nil {
		return ""
	}
	entryType := strings.ToLower(strings.TrimSpace(strOrEmpty(entry["type"])))

	// 1. response_item entries
	if entryType == "response_item" {
		payload, _ := entry["payload"].(map[string]any)
		if payload == nil || strOrEmpty(payload["type"]) != "message" {
			return ""
		}
		if strings.ToLower(strOrEmpty(payload["role"])) != role {
			return ""
		}
		return extractContentText(payload["content"])
	}

	// 2. event_msg entries
	if entryType == "event_msg" {
		payload, _ := entry["payload"].(map[string]any)
		if payload == nil {
			return ""
		}
		ptype := strings.ToLower(strOrEmpty(payload["type"]))
		if ptype == "agent_message" || ptype == "assistant_message" || ptype == "assistant" {
			if strings.ToLower(strOrEmpty(payload["role"])) != role {
				return ""
			}
			for _, key := range []string{"message", "content", "text"} {
				if msg := strings.TrimSpace(strOrEmpty(payload[key])); msg != "" {
					return msg
				}
			}
		}
		return ""
	}

	// 3. Default Claude log shape: {"message": {"role": ..., "content": ...}}
	if msgObj, ok := entry["message"].(map[string]any); ok {
		msgRole := strings.ToLower(strings.TrimSpace(coalesce(strOrEmpty(msgObj["role"]), entryType)))
		if msgRole != role {
			return ""
		}
		return extractContentText(msgObj["content"])
	}
	if entryType != role {
		return ""
	}
	return extractContentText(entry["content"])
}

// ---------------------------------------------------------------------------
// ClaudeLogState tracks the read cursor for Claude JSONL sessions.
// ---------------------------------------------------------------------------

// ClaudeLogState is the cursor into a Claude session JSONL file.
type ClaudeLogState struct {
	SessionPath string
	Offset      int64
	Carry       []byte // partial line from previous read
}

// ---------------------------------------------------------------------------
// ClaudeLogReader reads Claude session logs.
// ---------------------------------------------------------------------------

// ClaudeLogReader reads Claude session logs from ~/.claude/projects/<key>.
type ClaudeLogReader struct {
	Root             string
	WorkDir          string
	preferredSession string
	useSessionsIndex bool
	includeSubagents bool
	pollInterval     time.Duration
	mu               sync.Mutex
}

// NewClaudeLogReader creates a new ClaudeLogReader.
func NewClaudeLogReader(root, workDir string, opts ...ClaudeLogReaderOption) *ClaudeLogReader {
	if root == "" {
		root = DefaultClaudeProjectsRoot()
	}
	if workDir == "" {
		workDir, _ = os.Getwd()
	}
	poll := envFloat("CLAUDE_POLL_INTERVAL", 0.05)
	poll = clampPollInterval(poll, 0.02, 0.5)

	r := &ClaudeLogReader{
		Root:             root,
		WorkDir:          workDir,
		useSessionsIndex: true,
		pollInterval:     time.Duration(poll * float64(time.Second)),
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// ClaudeLogReaderOption configures a ClaudeLogReader.
type ClaudeLogReaderOption func(*ClaudeLogReader)

// WithSessionsIndex controls whether sessions-index.json is consulted.
func WithSessionsIndex(v bool) ClaudeLogReaderOption {
	return func(r *ClaudeLogReader) { r.useSessionsIndex = v }
}

// WithIncludeSubagents controls subagent log inclusion.
func WithIncludeSubagents(v bool) ClaudeLogReaderOption {
	return func(r *ClaudeLogReader) { r.includeSubagents = v }
}

// SetPreferredSession sets the preferred session JSONL path.
func (r *ClaudeLogReader) SetPreferredSession(path string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if path == "" {
		return
	}
	if _, err := os.Stat(path); err == nil {
		r.preferredSession = path
	}
}

// CurrentSessionPath returns the path of the latest session JSONL file.
func (r *ClaudeLogReader) CurrentSessionPath() string {
	return r.latestSession()
}

func (r *ClaudeLogReader) projectDir() string {
	candidates := candidateProjectDirs(r.Root, r.WorkDir)
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	if len(candidates) > 0 {
		return candidates[len(candidates)-1]
	}
	return filepath.Join(r.Root, ProjectKeyForPath(r.WorkDir))
}

func (r *ClaudeLogReader) parseSessionsIndex() string {
	if !r.useSessionsIndex {
		return ""
	}
	projDir := r.projectDir()
	indexPath := filepath.Join(projDir, "sessions-index.json")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		return ""
	}
	var payload map[string]any
	if json.Unmarshal(data, &payload) != nil {
		return ""
	}
	entries, _ := payload["entries"].([]any)
	candidates := make(map[string]bool)
	for _, c := range candidateProjectPaths(r.WorkDir) {
		candidates[c] = true
	}

	var bestPath string
	bestMtime := int64(-1)
	for _, raw := range entries {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		isSidechain, _ := entry["isSidechain"].(bool)
		if isSidechain {
			continue
		}
		projectPath := strOrEmpty(entry["projectPath"])
		if projectPath != "" {
			n := normalizeProjectPath(projectPath)
			if len(candidates) > 0 && n != "" && !candidates[n] {
				continue
			}
		} else if len(candidates) > 0 {
			continue
		}
		fullPath := strOrEmpty(entry["fullPath"])
		if fullPath == "" {
			continue
		}
		sessionPath := expandHome(fullPath)
		if !filepath.IsAbs(sessionPath) {
			sessionPath = filepath.Join(projDir, sessionPath)
		}
		if _, err := os.Stat(sessionPath); err != nil {
			continue
		}
		var mtime int64
		switch v := entry["fileMtime"].(type) {
		case float64:
			mtime = int64(v)
		case string:
			// try parse
		}
		if mtime == 0 {
			if fi, err := os.Stat(sessionPath); err == nil {
				mtime = fi.ModTime().UnixMilli()
			}
		}
		if mtime > bestMtime {
			bestMtime = mtime
			bestPath = sessionPath
		}
	}
	return bestPath
}

func (r *ClaudeLogReader) sessionIsSidechain(path string) *bool {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 4096)
	lineCount := 0
	for lineCount < 20 {
		n, err := f.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		for {
			idx := indexByte(buf, '\n')
			if idx < 0 {
				break
			}
			line := strings.TrimSpace(string(buf[:idx]))
			buf = buf[idx+1:]
			lineCount++
			if line == "" || lineCount > 20 {
				continue
			}
			var entry map[string]any
			if json.Unmarshal([]byte(line), &entry) != nil {
				continue
			}
			if v, ok := entry["isSidechain"]; ok {
				b, _ := v.(bool)
				return &b
			}
		}
		if err != nil {
			break
		}
	}
	return nil
}

func (r *ClaudeLogReader) scanLatestSession() string {
	projDir := r.projectDir()
	if _, err := os.Stat(projDir); err != nil {
		return ""
	}
	entries, err := os.ReadDir(projDir)
	if err != nil {
		return ""
	}

	type sessionInfo struct {
		path  string
		mtime time.Time
	}
	var sessions []sessionInfo
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") || filepath.Ext(e.Name()) != ".jsonl" {
			continue
		}
		path := filepath.Join(projDir, e.Name())
		fi, err := os.Stat(path)
		if err != nil {
			continue
		}
		sessions = append(sessions, sessionInfo{path: path, mtime: fi.ModTime()})
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].mtime.After(sessions[j].mtime) })

	var firstUnknown string
	for _, s := range sessions {
		sc := r.sessionIsSidechain(s.path)
		if sc != nil && !*sc {
			return s.path
		}
		if sc == nil && firstUnknown == "" {
			firstUnknown = s.path
		}
	}
	if firstUnknown != "" {
		return firstUnknown
	}
	if len(sessions) > 0 {
		return sessions[0].path
	}
	return ""
}

func (r *ClaudeLogReader) latestSession() string {
	r.mu.Lock()
	preferred := r.preferredSession
	r.mu.Unlock()

	indexSession := r.parseSessionsIndex()
	var scanned string
	if indexSession == "" {
		scanned = r.scanLatestSession()
	}

	if preferred != "" {
		if pfi, err := os.Stat(preferred); err == nil {
			if indexSession != "" {
				if ifi, err := os.Stat(indexSession); err == nil && ifi.ModTime().After(pfi.ModTime()) {
					r.mu.Lock()
					r.preferredSession = indexSession
					r.mu.Unlock()
					return indexSession
				}
				return preferred
			}
			if scanned != "" {
				if sfi, err := os.Stat(scanned); err == nil && sfi.ModTime().After(pfi.ModTime()) {
					r.mu.Lock()
					r.preferredSession = scanned
					r.mu.Unlock()
					return scanned
				}
			}
			return preferred
		}
	}
	if indexSession != "" {
		r.mu.Lock()
		r.preferredSession = indexSession
		r.mu.Unlock()
		return indexSession
	}
	if scanned != "" {
		r.mu.Lock()
		r.preferredSession = scanned
		r.mu.Unlock()
		return scanned
	}
	if envBool("CLAUDE_ALLOW_ANY_PROJECT_SCAN") {
		pattern := filepath.Join(r.Root, "*", "*.jsonl")
		matches, _ := filepath.Glob(pattern)
		var best string
		var bestMtime time.Time
		for _, m := range matches {
			if strings.HasPrefix(filepath.Base(m), ".") {
				continue
			}
			fi, err := os.Stat(m)
			if err != nil {
				continue
			}
			if fi.ModTime().After(bestMtime) {
				bestMtime = fi.ModTime()
				best = m
			}
		}
		if best != "" {
			r.mu.Lock()
			r.preferredSession = best
			r.mu.Unlock()
			return best
		}
	}
	return ""
}

// CaptureState captures the current session file and offset.
func (r *ClaudeLogReader) CaptureState() ClaudeLogState {
	session := r.latestSession()
	var offset int64
	if session != "" {
		if fi, err := os.Stat(session); err == nil {
			offset = fi.Size()
		}
	}
	return ClaudeLogState{SessionPath: session, Offset: offset}
}

// WaitForMessage blocks until a new assistant reply appears or timeout expires.
func (r *ClaudeLogReader) WaitForMessage(state ClaudeLogState, timeout time.Duration) (string, ClaudeLogState) {
	return r.readSince(state, timeout, true)
}

// TryGetMessage performs a non-blocking read for a new assistant reply.
func (r *ClaudeLogReader) TryGetMessage(state ClaudeLogState) (string, ClaudeLogState) {
	return r.readSince(state, 0, false)
}

// WaitForEvents blocks until new conversation events appear or timeout expires.
func (r *ClaudeLogReader) WaitForEvents(state ClaudeLogState, timeout time.Duration) ([]Event, ClaudeLogState) {
	return r.readSinceEvents(state, timeout, true)
}

// TryGetEvents performs a non-blocking read for new conversation events.
func (r *ClaudeLogReader) TryGetEvents(state ClaudeLogState) ([]Event, ClaudeLogState) {
	return r.readSinceEvents(state, 0, false)
}

// LatestMessage scans the full session and returns the last assistant message.
func (r *ClaudeLogReader) LatestMessage() string {
	session := r.latestSession()
	if session == "" {
		return ""
	}
	f, err := os.Open(session)
	if err != nil {
		return ""
	}
	defer f.Close()

	var last string
	scanner := newLineScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry map[string]any
		if json.Unmarshal([]byte(line), &entry) != nil {
			continue
		}
		if msg := claudeExtractMessage(entry, "assistant"); msg != "" {
			last = msg
		}
	}
	return last
}

// LatestConversations returns up to n recent (user, assistant) pairs.
func (r *ClaudeLogReader) LatestConversations(n int) []ConvPair {
	session := r.latestSession()
	if session == "" {
		return nil
	}
	f, err := os.Open(session)
	if err != nil {
		return nil
	}
	defer f.Close()

	var pairs []ConvPair
	var lastUser string
	scanner := newLineScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry map[string]any
		if json.Unmarshal([]byte(line), &entry) != nil {
			continue
		}
		if userMsg := claudeExtractMessage(entry, "user"); userMsg != "" {
			lastUser = userMsg
			continue
		}
		if assistantMsg := claudeExtractMessage(entry, "assistant"); assistantMsg != "" {
			pairs = append(pairs, ConvPair{User: lastUser, Assistant: assistantMsg})
			lastUser = ""
		}
	}
	if n < 1 {
		n = 1
	}
	if len(pairs) > n {
		return pairs[len(pairs)-n:]
	}
	return pairs
}

func (r *ClaudeLogReader) readSince(state ClaudeLogState, timeout time.Duration, block bool) (string, ClaudeLogState) {
	deadline := time.Now().Add(timeout)
	if !block {
		deadline = time.Now()
	}
	currentState := state

	for {
		session := r.latestSession()
		if session == "" {
			if !block || !time.Now().Before(deadline) {
				return "", currentState
			}
			pollSleep(r.pollInterval)
			continue
		}
		if currentState.SessionPath != session {
			currentState.SessionPath = session
			currentState.Offset = 0
			currentState.Carry = nil
		}

		msg, newState := readNewClaudeMessages(session, currentState)
		currentState = newState
		if msg != "" {
			return msg, currentState
		}

		if !block || !time.Now().Before(deadline) {
			return "", currentState
		}
		pollSleep(r.pollInterval)
	}
}

func (r *ClaudeLogReader) readSinceEvents(state ClaudeLogState, timeout time.Duration, block bool) ([]Event, ClaudeLogState) {
	deadline := time.Now().Add(timeout)
	if !block {
		deadline = time.Now()
	}
	currentState := state

	for {
		session := r.latestSession()
		if session == "" {
			if !block || !time.Now().Before(deadline) {
				return nil, currentState
			}
			pollSleep(r.pollInterval)
			continue
		}
		if currentState.SessionPath != session {
			currentState.SessionPath = session
			currentState.Offset = 0
			currentState.Carry = nil
		}

		events, newState := readNewClaudeEvents(session, currentState)
		currentState = newState
		if len(events) > 0 {
			return events, currentState
		}

		if !block || !time.Now().Before(deadline) {
			return nil, currentState
		}
		pollSleep(r.pollInterval)
	}
}

// readNewClaudeMessages is like readNewJSONLMessages but uses Claude's extraction.
func readNewClaudeMessages(path string, state ClaudeLogState) (string, ClaudeLogState) {
	offset := state.Offset
	carry := state.Carry

	fi, err := os.Stat(path)
	if err != nil {
		return "", state
	}
	size := fi.Size()
	if size < offset {
		offset = 0
		carry = nil
	}

	f, err := os.Open(path)
	if err != nil {
		return "", state
	}
	defer f.Close()

	if _, err := f.Seek(offset, 0); err != nil {
		return "", state
	}
	data, _ := readAllBytes(f)
	newOffset := offset + int64(len(data))

	buf := append(carry, data...)
	lines := splitByteLines(buf)
	var newCarry []byte
	if len(buf) > 0 && buf[len(buf)-1] != '\n' {
		newCarry = lines[len(lines)-1]
		lines = lines[:len(lines)-1]
	}

	var latest string
	for _, raw := range lines {
		line := strings.TrimSpace(string(raw))
		if line == "" {
			continue
		}
		var entry map[string]any
		if json.Unmarshal([]byte(line), &entry) != nil {
			continue
		}
		if msg := claudeExtractMessage(entry, "assistant"); msg != "" {
			latest = msg
		}
	}
	return latest, ClaudeLogState{SessionPath: path, Offset: newOffset, Carry: newCarry}
}

// readNewClaudeEvents reads new JSONL lines and returns user/assistant events.
func readNewClaudeEvents(path string, state ClaudeLogState) ([]Event, ClaudeLogState) {
	offset := state.Offset
	carry := state.Carry

	fi, err := os.Stat(path)
	if err != nil {
		return nil, state
	}
	size := fi.Size()
	if size < offset {
		offset = 0
		carry = nil
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, state
	}
	defer f.Close()

	if _, err := f.Seek(offset, 0); err != nil {
		return nil, state
	}
	data, _ := readAllBytes(f)
	newOffset := offset + int64(len(data))

	buf := append(carry, data...)
	lines := splitByteLines(buf)
	var newCarry []byte
	if len(buf) > 0 && buf[len(buf)-1] != '\n' {
		newCarry = lines[len(lines)-1]
		lines = lines[:len(lines)-1]
	}

	var events []Event
	for _, raw := range lines {
		line := strings.TrimSpace(string(raw))
		if line == "" {
			continue
		}
		var entry map[string]any
		if json.Unmarshal([]byte(line), &entry) != nil {
			continue
		}
		if userMsg := claudeExtractMessage(entry, "user"); userMsg != "" {
			events = append(events, Event{Role: "user", Text: userMsg})
			continue
		}
		if assistantMsg := claudeExtractMessage(entry, "assistant"); assistantMsg != "" {
			events = append(events, Event{Role: "assistant", Text: assistantMsg})
		}
	}
	return events, ClaudeLogState{SessionPath: path, Offset: newOffset, Carry: newCarry}
}

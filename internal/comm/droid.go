// Droid communication module.
//
// Reads replies from ~/.factory/sessions/<slug>/<session-id>.jsonl and
// sends prompts by injecting text into the Droid pane via the configured backend.
//
// Source: claude_code_bridge/lib/droid_comm.py
package comm

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Droid sessions root
// ---------------------------------------------------------------------------

// DefaultDroidSessionsRoot returns the default sessions root for Droid.
func DefaultDroidSessionsRoot() string {
	if override := strings.TrimSpace(coalesce(os.Getenv("DROID_SESSIONS_ROOT"), os.Getenv("FACTORY_SESSIONS_ROOT"))); override != "" {
		return expandHome(override)
	}
	factoryHome := strings.TrimSpace(coalesce(os.Getenv("FACTORY_HOME"), os.Getenv("FACTORY_ROOT")))
	if factoryHome != "" {
		return filepath.Join(expandHome(factoryHome), "sessions")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".factory", "sessions")
}

// ---------------------------------------------------------------------------
// JSONL message extraction helpers (shared with Claude)
// ---------------------------------------------------------------------------

// extractContentText extracts text from a JSON content field (string or list of blocks).
func extractContentText(content interface{}) string {
	if content == nil {
		return ""
	}
	if s, ok := content.(string); ok {
		return strings.TrimSpace(s)
	}
	items, ok := content.([]interface{})
	if !ok {
		return ""
	}
	var texts []string
	for _, raw := range items {
		item, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		itemType := strings.ToLower(strings.TrimSpace(strOrEmpty(item["type"])))
		if itemType == "thinking" || itemType == "thinking_delta" {
			continue
		}
		text := strOrEmpty(item["text"])
		if text == "" && itemType == "text" {
			text = strOrEmpty(item["content"])
		}
		if t := strings.TrimSpace(text); t != "" {
			texts = append(texts, t)
		}
	}
	if len(texts) == 0 {
		return ""
	}
	return strings.TrimSpace(strings.Join(texts, "\n"))
}

// extractJSONLMessage extracts a message with the given role from a JSONL entry.
// Used by Droid and Claude log readers.
func extractJSONLMessage(entry map[string]interface{}, role string) string {
	if entry == nil {
		return ""
	}
	entryType := strings.ToLower(strings.TrimSpace(strOrEmpty(entry["type"])))

	// Shape 1: {"type": "message", "message": {"role": ..., "content": ...}}
	if entryType == "message" {
		if msgObj, ok := entry["message"].(map[string]interface{}); ok {
			msgRole := strings.ToLower(strings.TrimSpace(strOrEmpty(msgObj["role"])))
			if msgRole == role {
				return extractContentText(msgObj["content"])
			}
		}
	}

	// Shape 2: top-level role field
	msgRole := strings.ToLower(strings.TrimSpace(coalesce(strOrEmpty(entry["role"]), entryType)))
	if msgRole == role {
		return extractContentText(coalesceIface(entry["content"], entry["message"]))
	}
	return ""
}

// ---------------------------------------------------------------------------
// DroidLogState tracks the read cursor for Droid JSONL sessions.
// ---------------------------------------------------------------------------

// DroidLogState is the cursor into a Droid session JSONL file.
type DroidLogState struct {
	SessionPath string
	Offset      int64
	Carry       []byte // partial line from previous read
}

// ---------------------------------------------------------------------------
// DroidLogReader reads Droid session logs.
// ---------------------------------------------------------------------------

// DroidLogReader reads Droid session JSONL logs from ~/.factory/sessions.
type DroidLogReader struct {
	Root             string
	WorkDir          string
	preferredSession string
	sessionIDHint    string
	pollInterval     time.Duration
	scanLimit        int
	mu               sync.Mutex
}

// NewDroidLogReader creates a new DroidLogReader.
func NewDroidLogReader(root, workDir string) *DroidLogReader {
	if root == "" {
		root = DefaultDroidSessionsRoot()
	}
	if workDir == "" {
		workDir, _ = os.Getwd()
	}
	poll := envFloat("DROID_POLL_INTERVAL", 0.05)
	poll = clampPollInterval(poll, 0.02, 0.5)
	limit := envInt("DROID_SESSION_SCAN_LIMIT", 200)
	if limit < 1 {
		limit = 1
	}
	return &DroidLogReader{
		Root:         root,
		WorkDir:      workDir,
		pollInterval: time.Duration(poll * float64(time.Second)),
		scanLimit:    limit,
	}
}

// SetPreferredSession sets the preferred session JSONL path.
func (r *DroidLogReader) SetPreferredSession(path string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if path == "" {
		return
	}
	if _, err := os.Stat(path); err == nil {
		r.preferredSession = path
	}
}

// SetSessionIDHint sets the session ID hint for targeted lookup.
func (r *DroidLogReader) SetSessionIDHint(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessionIDHint = strings.TrimSpace(id)
}

// CurrentSessionPath returns the path of the latest session JSONL file.
func (r *DroidLogReader) CurrentSessionPath() string {
	return r.latestSession()
}

// ReadDroidSessionStart reads the first session_start entry from a JSONL file.
// Returns (cwd, sessionID).
func ReadDroidSessionStart(sessionPath string, maxLines int) (cwd, sessionID string) {
	if maxLines <= 0 {
		maxLines = 30
	}
	f, err := os.Open(sessionPath)
	if err != nil {
		return "", ""
	}
	defer f.Close()

	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 4096)
	lineCount := 0
	for lineCount < maxLines {
		n, err := f.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		// Process complete lines
		for {
			idx := indexByte(buf, '\n')
			if idx < 0 {
				break
			}
			line := strings.TrimSpace(string(buf[:idx]))
			buf = buf[idx+1:]
			lineCount++
			if line == "" || lineCount > maxLines {
				continue
			}
			var entry map[string]interface{}
			if json.Unmarshal([]byte(line), &entry) != nil {
				continue
			}
			if strOrEmpty(entry["type"]) != "session_start" {
				continue
			}
			cwdVal := strings.TrimSpace(strOrEmpty(entry["cwd"]))
			sidVal := strings.TrimSpace(strOrEmpty(entry["id"]))
			if cwdVal != "" {
				cwd = cwdVal
			}
			if sidVal != "" {
				sessionID = sidVal
			}
			return cwd, sessionID
		}
		if err != nil {
			break
		}
	}
	return "", ""
}

func (r *DroidLogReader) findSessionByID() string {
	r.mu.Lock()
	sid := r.sessionIDHint
	r.mu.Unlock()
	if sid == "" {
		return ""
	}
	if _, err := os.Stat(r.Root); err != nil {
		return ""
	}
	pattern := filepath.Join(r.Root, "**", sid+".jsonl")
	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) == 0 {
		// filepath.Glob doesn't support ** well; do manual walk
		var latest string
		var latestMtime float64
		_ = filepath.Walk(r.Root, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			if filepath.Base(path) == sid+".jsonl" {
				mt := float64(info.ModTime().UnixMilli()) / 1000.0
				if mt >= latestMtime {
					latestMtime = mt
					latest = path
				}
			}
			return nil
		})
		return latest
	}
	return matches[0]
}

func (r *DroidLogReader) scanLatestSession() string {
	if _, err := os.Stat(r.Root); err != nil {
		return ""
	}

	type candidate struct {
		mtime float64
		path  string
	}
	var candidates []candidate
	_ = filepath.Walk(r.Root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || filepath.Ext(path) != ".jsonl" || strings.HasPrefix(info.Name(), ".") {
			return nil
		}
		candidates = append(candidates, candidate{
			mtime: float64(info.ModTime().UnixMilli()) / 1000.0,
			path:  path,
		})
		return nil
	})

	// Sort by mtime descending, limit to scanLimit
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].mtime > candidates[j].mtime })
	if len(candidates) > r.scanLimit {
		candidates = candidates[:r.scanLimit]
	}

	workDir := r.WorkDir
	for _, c := range candidates {
		cwd, _ := ReadDroidSessionStart(c.path, 30)
		if cwd == "" {
			continue
		}
		if pathIsSameOrParent(workDir, cwd) || pathIsSameOrParent(cwd, workDir) {
			return c.path
		}
	}
	return ""
}

func (r *DroidLogReader) latestSession() string {
	r.mu.Lock()
	preferred := r.preferredSession
	r.mu.Unlock()

	scanned := r.scanLatestSession()

	if preferred != "" {
		if fi, err := os.Stat(preferred); err == nil {
			if scanned != "" {
				if si, err2 := os.Stat(scanned); err2 == nil {
					if si.ModTime().After(fi.ModTime()) {
						r.mu.Lock()
						r.preferredSession = scanned
						r.mu.Unlock()
						return scanned
					}
				}
			}
			return preferred
		}
	}

	byID := r.findSessionByID()
	if byID != "" {
		r.mu.Lock()
		r.preferredSession = byID
		r.mu.Unlock()
		return byID
	}

	if scanned != "" {
		r.mu.Lock()
		r.preferredSession = scanned
		r.mu.Unlock()
		return scanned
	}

	if envBool("DROID_ALLOW_ANY_PROJECT_SCAN") {
		// Scan for any latest session regardless of project
		var latest string
		var latestMtime float64
		_ = filepath.Walk(r.Root, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || filepath.Ext(path) != ".jsonl" || strings.HasPrefix(info.Name(), ".") {
				return nil
			}
			mt := float64(info.ModTime().UnixMilli()) / 1000.0
			if mt >= latestMtime {
				latestMtime = mt
				latest = path
			}
			return nil
		})
		if latest != "" {
			r.mu.Lock()
			r.preferredSession = latest
			r.mu.Unlock()
			return latest
		}
	}
	return ""
}

// CaptureState captures the current session file and offset.
func (r *DroidLogReader) CaptureState() DroidLogState {
	session := r.latestSession()
	var offset int64
	if session != "" {
		if fi, err := os.Stat(session); err == nil {
			offset = fi.Size()
		}
	}
	return DroidLogState{SessionPath: session, Offset: offset}
}

// WaitForMessage blocks until a new assistant reply appears or timeout expires.
func (r *DroidLogReader) WaitForMessage(state DroidLogState, timeout time.Duration) (string, DroidLogState) {
	return r.readSince(state, timeout, true)
}

// TryGetMessage performs a non-blocking read for a new assistant reply.
func (r *DroidLogReader) TryGetMessage(state DroidLogState) (string, DroidLogState) {
	return r.readSince(state, 0, false)
}

// WaitForEvents blocks until new conversation events appear or timeout expires.
func (r *DroidLogReader) WaitForEvents(state DroidLogState, timeout time.Duration) ([]Event, DroidLogState) {
	return r.readSinceEvents(state, timeout, true)
}

// TryGetEvents performs a non-blocking read for new conversation events.
func (r *DroidLogReader) TryGetEvents(state DroidLogState) ([]Event, DroidLogState) {
	return r.readSinceEvents(state, 0, false)
}

// LatestMessage scans the full session and returns the last assistant message.
func (r *DroidLogReader) LatestMessage() string {
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
		var entry map[string]interface{}
		if json.Unmarshal([]byte(line), &entry) != nil {
			continue
		}
		if msg := extractJSONLMessage(entry, "assistant"); msg != "" {
			last = msg
		}
	}
	return last
}

// LatestConversations returns up to n recent (user, assistant) pairs.
func (r *DroidLogReader) LatestConversations(n int) []ConvPair {
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
		var entry map[string]interface{}
		if json.Unmarshal([]byte(line), &entry) != nil {
			continue
		}
		if userMsg := extractJSONLMessage(entry, "user"); userMsg != "" {
			lastUser = userMsg
			continue
		}
		if assistantMsg := extractJSONLMessage(entry, "assistant"); assistantMsg != "" {
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

func (r *DroidLogReader) readSince(state DroidLogState, timeout time.Duration, block bool) (string, DroidLogState) {
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

		msg, newState := readNewJSONLMessages(session, currentState, "assistant")
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

func (r *DroidLogReader) readSinceEvents(state DroidLogState, timeout time.Duration, block bool) ([]Event, DroidLogState) {
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

		events, newState := readNewJSONLEvents(session, currentState)
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

// ---------------------------------------------------------------------------
// JSONL incremental reading (shared by Droid and Claude)
// ---------------------------------------------------------------------------

// readNewJSONLMessages reads new JSONL lines, returns the latest message with the given role.
func readNewJSONLMessages(path string, state DroidLogState, role string) (string, DroidLogState) {
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
	data, err := readAllBytes(f)
	if err != nil {
		return "", state
	}
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
		var entry map[string]interface{}
		if json.Unmarshal([]byte(line), &entry) != nil {
			continue
		}
		if msg := extractJSONLMessage(entry, role); msg != "" {
			latest = msg
		}
	}
	return latest, DroidLogState{SessionPath: path, Offset: newOffset, Carry: newCarry}
}

// readNewJSONLEvents reads new JSONL lines and returns user/assistant events.
func readNewJSONLEvents(path string, state DroidLogState) ([]Event, DroidLogState) {
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
	data, err := readAllBytes(f)
	if err != nil {
		return nil, state
	}
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
		var entry map[string]interface{}
		if json.Unmarshal([]byte(line), &entry) != nil {
			continue
		}
		if userMsg := extractJSONLMessage(entry, "user"); userMsg != "" {
			events = append(events, Event{Role: "user", Text: userMsg})
			continue
		}
		if assistantMsg := extractJSONLMessage(entry, "assistant"); assistantMsg != "" {
			events = append(events, Event{Role: "assistant", Text: assistantMsg})
		}
	}
	return events, DroidLogState{SessionPath: path, Offset: newOffset, Carry: newCarry}
}

// ---------------------------------------------------------------------------
// Path matching helpers
// ---------------------------------------------------------------------------

func normalizePathForMatch(value string) string {
	s := strings.TrimSpace(value)
	s = strings.ReplaceAll(s, "\\", "/")
	s = strings.TrimRight(s, "/")
	return s
}

func pathIsSameOrParent(parent, child string) bool {
	pn := normalizePathForMatch(parent)
	cn := normalizePathForMatch(child)
	if pn == "" || cn == "" {
		return false
	}
	if pn == cn {
		return true
	}
	if !strings.HasPrefix(cn, pn) {
		return false
	}
	return cn[len(pn):len(pn)+1] == "/"
}

// ---------------------------------------------------------------------------
// Utility helpers
// ---------------------------------------------------------------------------

func strOrEmpty(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func coalesce(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func coalesceIface(values ...interface{}) interface{} {
	for _, v := range values {
		if v != nil {
			return v
		}
	}
	return nil
}

func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") || path == "~" {
		home, err := os.UserHomeDir()
		if err == nil {
			if path == "~" {
				return home
			}
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

func indexByte(data []byte, b byte) int {
	for i, c := range data {
		if c == b {
			return i
		}
	}
	return -1
}

func splitByteLines(buf []byte) [][]byte {
	return splitBytesBy(buf, '\n')
}

func splitBytesBy(buf []byte, sep byte) [][]byte {
	var lines [][]byte
	for {
		idx := indexByte(buf, sep)
		if idx < 0 {
			lines = append(lines, buf)
			break
		}
		lines = append(lines, buf[:idx])
		buf = buf[idx+1:]
	}
	return lines
}

func readAllBytes(f *os.File) ([]byte, error) {
	var buf []byte
	tmp := make([]byte, 32*1024)
	for {
		n, err := f.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if err != nil {
			break
		}
	}
	return buf, nil
}

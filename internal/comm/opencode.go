// OpenCode communication module.
//
// Reads replies from OpenCode storage (~/.local/share/opencode/storage)
// via JSON files or SQLite.
//
// Source: claude_code_bridge/lib/opencode_comm.py
package comm

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver
)

// ---------------------------------------------------------------------------
// OpenCode storage root
// ---------------------------------------------------------------------------

// DefaultOpenCodeStorageRoot returns the default OpenCode storage root.
func DefaultOpenCodeStorageRoot() string {
	if env := strings.TrimSpace(os.Getenv("OPENCODE_STORAGE_ROOT")); env != "" {
		return expandHome(env)
	}
	// Try XDG_DATA_HOME first
	if xdg := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); xdg != "" {
		p := filepath.Join(xdg, "opencode", "storage")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	home, _ := os.UserHomeDir()
	p := filepath.Join(home, ".local", "share", "opencode", "storage")
	if _, err := os.Stat(p); err == nil {
		return p
	}
	// Fallback to default even if it doesn't exist
	return p
}

// ---------------------------------------------------------------------------
// OpenCodeLogState tracks the read cursor for OpenCode sessions.
// ---------------------------------------------------------------------------

// OpenCodeLogState is the cursor into an OpenCode session.
type OpenCodeLogState struct {
	SessionID         string
	SessionUpdated    int64
	AssistantCount    int
	LastAssistantID   string
	LastAssistantDone bool
}

// ---------------------------------------------------------------------------
// OpenCodeLogReader reads OpenCode session/message/part data.
// ---------------------------------------------------------------------------

// OpenCodeLogReader reads OpenCode session data from storage JSON files or SQLite.
//
// Observed storage layout:
//
//	storage/session/<projectID>/ses_*.json
//	storage/message/<sessionID>/msg_*.json
//	storage/part/<messageID>/prt_*.json
//	../opencode.db (message/part tables)
type OpenCodeLogReader struct {
	Root              string
	WorkDir           string
	ProjectID         string
	sessionIDFilter   string
	allowParentMatch  bool
	allowAnySession   bool
	pollInterval      time.Duration
	forceReadInterval time.Duration
	dbPathHint        string
	db                *sql.DB
	dbPath            string
	mu                sync.Mutex
}

// NewOpenCodeLogReader creates a new OpenCodeLogReader.
func NewOpenCodeLogReader(root, workDir, projectID string, opts ...OpenCodeOption) *OpenCodeLogReader {
	if root == "" {
		root = DefaultOpenCodeStorageRoot()
	}
	if workDir == "" {
		workDir, _ = os.Getwd()
	}
	if projectID == "" {
		projectID = "global"
	}

	poll := envFloat("OPENCODE_POLL_INTERVAL", 0.05)
	poll = clampPollInterval(poll, 0.02, 0.5)
	force := envFloat("OPENCODE_FORCE_READ_INTERVAL", 1.0)
	if force < 0.2 {
		force = 0.2
	}
	if force > 5.0 {
		force = 5.0
	}

	r := &OpenCodeLogReader{
		Root:              root,
		WorkDir:           workDir,
		ProjectID:         strings.TrimSpace(coalesce(strings.TrimSpace(os.Getenv("OPENCODE_PROJECT_ID")), projectID)),
		allowParentMatch:  envBool("OPENCODE_ALLOW_PARENT_WORKDIR_MATCH"),
		allowAnySession:   envBool("OPENCODE_ALLOW_ANY_SESSION"),
		pollInterval:      time.Duration(poll * float64(time.Second)),
		forceReadInterval: time.Duration(force * float64(time.Second)),
	}
	for _, opt := range opts {
		opt(r)
	}

	// Auto-detect project ID if not explicitly provided
	envPID := strings.TrimSpace(os.Getenv("OPENCODE_PROJECT_ID"))
	explicitPID := envPID != "" || (strings.TrimSpace(projectID) != "" && strings.TrimSpace(projectID) != "global")
	if !explicitPID {
		if detected := r.detectProjectIDForWorkdir(); detected != "" {
			r.ProjectID = detected
		}
	}

	return r
}

// OpenCodeOption configures an OpenCodeLogReader.
type OpenCodeOption func(*OpenCodeLogReader)

// WithSessionIDFilter sets the session ID filter.
func WithSessionIDFilter(id string) OpenCodeOption {
	return func(r *OpenCodeLogReader) { r.sessionIDFilter = strings.TrimSpace(id) }
}

func (r *OpenCodeLogReader) sessionDir() string {
	return filepath.Join(r.Root, "session", r.ProjectID)
}

func (r *OpenCodeLogReader) messageDir(sessionID string) string {
	nested := filepath.Join(r.Root, "message", sessionID)
	if _, err := os.Stat(nested); err == nil {
		return nested
	}
	return filepath.Join(r.Root, "message")
}

func (r *OpenCodeLogReader) partDir(messageID string) string {
	nested := filepath.Join(r.Root, "part", messageID)
	if _, err := os.Stat(nested); err == nil {
		return nested
	}
	return filepath.Join(r.Root, "part")
}

func (r *OpenCodeLogReader) workDirCandidates() []string {
	var candidates []string
	seen := make(map[string]bool)
	add := func(v string) {
		n := normalizePathForMatch(v)
		if n == "" || seen[n] {
			return
		}
		seen[n] = true
		candidates = append(candidates, n)
	}
	if envPwd := os.Getenv("PWD"); envPwd != "" {
		add(envPwd)
	}
	add(r.WorkDir)
	if resolved, err := filepath.EvalSymlinks(r.WorkDir); err == nil {
		add(resolved)
	}
	return candidates
}

func (r *OpenCodeLogReader) loadJSON(path string) map[string]interface{} {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var result map[string]interface{}
	if json.Unmarshal(data, &result) != nil {
		return nil
	}
	return result
}

func (r *OpenCodeLogReader) detectProjectIDForWorkdir() string {
	projectsDir := filepath.Join(r.Root, "project")
	if _, err := os.Stat(projectsDir); err != nil {
		return ""
	}
	workCandidates := r.workDirCandidates()
	if len(workCandidates) == 0 {
		return ""
	}

	type scored struct {
		id    string
		score [3]interface{} // (len(worktree), updated, mtime)
		lenWT int
		upd   int64
		mt    float64
	}
	var best *scored

	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		path := filepath.Join(projectsDir, e.Name())
		payload := r.loadJSON(path)
		if payload == nil {
			continue
		}
		pid := strOrEmpty(payload["id"])
		if pid == "" {
			pid = strings.TrimSuffix(e.Name(), ".json")
		}
		worktree := strOrEmpty(payload["worktree"])
		if pid == "" || worktree == "" {
			continue
		}
		worktreeNorm := normalizePathForMatch(worktree)
		if worktreeNorm == "" {
			continue
		}
		matched := false
		for _, c := range workCandidates {
			if r.allowParentMatch {
				if pathIsSameOrParent(worktreeNorm, c) || pathIsSameOrParent(c, worktreeNorm) {
					matched = true
					break
				}
			} else {
				if worktreeNorm == c {
					matched = true
					break
				}
			}
		}
		if !matched {
			continue
		}

		var updatedI int64 = -1
		if timeObj, ok := payload["time"].(map[string]interface{}); ok {
			if v, ok := timeObj["updated"].(float64); ok {
				updatedI = int64(v)
			}
		}
		var mtime float64
		if fi, err := os.Stat(path); err == nil {
			mtime = float64(fi.ModTime().UnixMilli()) / 1000.0
		}
		if best == nil || len(worktreeNorm) > best.lenWT ||
			(len(worktreeNorm) == best.lenWT && updatedI > best.upd) ||
			(len(worktreeNorm) == best.lenWT && updatedI == best.upd && mtime > best.mt) {
			best = &scored{id: pid, lenWT: len(worktreeNorm), upd: updatedI, mt: mtime}
		}
	}
	if best != nil {
		return best.id
	}
	return ""
}

// resolveOpenCodeDBPath finds the opencode.db file.
func (r *OpenCodeLogReader) resolveOpenCodeDBPath() string {
	r.mu.Lock()
	hint := r.dbPathHint
	r.mu.Unlock()
	if hint != "" {
		if _, err := os.Stat(hint); err == nil {
			return hint
		}
	}
	candidates := r.openCodeDBCandidates()
	for _, c := range candidates {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			r.mu.Lock()
			r.dbPathHint = c
			r.mu.Unlock()
			return c
		}
	}
	r.mu.Lock()
	r.dbPathHint = ""
	r.mu.Unlock()
	return ""
}

func (r *OpenCodeLogReader) openCodeDBCandidates() []string {
	var candidates []string
	seen := make(map[string]bool)
	add := func(p string) {
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		candidates = append(candidates, p)
	}
	if env := strings.TrimSpace(os.Getenv("OPENCODE_DB_PATH")); env != "" {
		add(expandHome(env))
	}
	// OpenCode stores DB one level above storage/
	add(filepath.Join(filepath.Dir(r.Root), "opencode.db"))
	add(filepath.Join(r.Root, "opencode.db"))
	return candidates
}

// Close releases the pooled SQLite connection, if any.
func (r *OpenCodeLogReader) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.db != nil {
		err := r.db.Close()
		r.db = nil
		r.dbPath = ""
		return err
	}
	return nil
}

// getDB returns a pooled *sql.DB, opening one lazily on first use.
// If the resolved DB path changes (e.g. file moved), the old connection is
// closed and a new one is opened.
func (r *OpenCodeLogReader) getDB() (*sql.DB, error) {
	resolvedPath := r.resolveOpenCodeDBPath()
	if resolvedPath == "" {
		return nil, nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Reuse existing connection if the path hasn't changed.
	if r.db != nil && r.dbPath == resolvedPath {
		return r.db, nil
	}

	// Path changed — close the old connection.
	if r.db != nil {
		r.db.Close()
		r.db = nil
		r.dbPath = ""
	}

	db, err := sql.Open("sqlite", resolvedPath+"?mode=ro&_busy_timeout=200")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	r.db = db
	r.dbPath = resolvedPath
	return db, nil
}

// fetchOpenCodeDBRows runs a read-only query against opencode.db.
func (r *OpenCodeLogReader) fetchOpenCodeDBRows(query string, args ...interface{}) ([]map[string]interface{}, error) {
	db, err := r.getDB()
	if err != nil {
		return nil, err
	}
	if db == nil {
		return nil, nil
	}

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	var result []map[string]interface{}
	for rows.Next() {
		values := make([]interface{}, len(cols))
		ptrs := make([]interface{}, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			continue
		}
		row := make(map[string]interface{})
		for i, col := range cols {
			row[col] = values[i]
		}
		result = append(result, row)
	}
	return result, nil
}

// getLatestSession returns the latest session for the configured project ID.
// Tries SQLite first, then falls back to JSON files.
func (r *OpenCodeLogReader) getLatestSession() (sessionID string, directory string, updated int64) {
	sid, dir, upd := r.getLatestSessionFromDB()
	if sid != "" {
		return sid, dir, upd
	}
	return r.getLatestSessionFromFiles()
}

func (r *OpenCodeLogReader) getLatestSessionFromDB() (sessionID string, directory string, updated int64) {
	candidates := r.workDirCandidates()
	if len(candidates) == 0 {
		return "", "", -1
	}
	rows, err := r.fetchOpenCodeDBRows("SELECT id, directory, time_updated FROM session ORDER BY time_updated DESC LIMIT 200")
	if err != nil || len(rows) == 0 {
		return "", "", -1
	}

	type match struct {
		id      string
		dir     string
		updated int64
	}
	var best *match
	var bestUpdated int64 = -1
	var latestUnfiltered *match
	var latestUnfilteredUpdated int64 = -1

	for _, row := range rows {
		dir := dbString(row["directory"])
		if dir == "" {
			continue
		}
		sid := dbString(row["id"])
		upd := dbInt64(row["time_updated"])

		dirNorm := normalizePathForMatch(dir)
		matched := false
		for _, cwd := range candidates {
			if r.allowParentMatch {
				if pathIsSameOrParent(dirNorm, cwd) || pathIsSameOrParent(cwd, dirNorm) {
					matched = true
					break
				}
			} else if dirNorm == cwd {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		if upd > latestUnfilteredUpdated {
			latestUnfiltered = &match{id: sid, dir: dir, updated: upd}
			latestUnfilteredUpdated = upd
		}
		if r.sessionIDFilter != "" && sid != r.sessionIDFilter {
			continue
		}
		if upd > bestUpdated {
			best = &match{id: sid, dir: dir, updated: upd}
			bestUpdated = upd
		}
	}

	// If we have a filter but found a newer unfiltered session, use it
	if r.sessionIDFilter != "" && latestUnfiltered != nil && latestUnfilteredUpdated > bestUpdated {
		return latestUnfiltered.id, latestUnfiltered.dir, latestUnfiltered.updated
	}
	if best != nil {
		return best.id, best.dir, best.updated
	}
	return "", "", -1
}

func (r *OpenCodeLogReader) getLatestSessionFromFiles() (sessionID string, directory string, updated int64) {
	sessionsDir := r.sessionDir()
	if _, err := os.Stat(sessionsDir); err != nil {
		return "", "", -1
	}
	candidates := r.workDirCandidates()

	type match struct {
		id      string
		dir     string
		updated int64
		mtime   float64
	}
	var best *match
	var bestAny *match

	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		return "", "", -1
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "ses_") || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		path := filepath.Join(sessionsDir, e.Name())
		payload := r.loadJSON(path)
		if payload == nil {
			continue
		}
		sid := strOrEmpty(payload["id"])
		if sid == "" {
			continue
		}
		var updatedI int64 = -1
		if timeObj, ok := payload["time"].(map[string]interface{}); ok {
			if v, ok := timeObj["updated"].(float64); ok {
				updatedI = int64(v)
			}
		}
		var mtime float64
		if fi, err := os.Stat(path); err == nil {
			mtime = float64(fi.ModTime().UnixMilli()) / 1000.0
		}

		if bestAny == nil || updatedI > bestAny.updated || (updatedI == bestAny.updated && mtime >= bestAny.mtime) {
			bestAny = &match{id: sid, dir: strOrEmpty(payload["directory"]), updated: updatedI, mtime: mtime}
		}

		dir := strOrEmpty(payload["directory"])
		if dir == "" {
			continue
		}
		dirNorm := normalizePathForMatch(dir)
		matched := false
		for _, c := range candidates {
			if r.allowParentMatch {
				if pathIsSameOrParent(dirNorm, c) || pathIsSameOrParent(c, dirNorm) {
					matched = true
					break
				}
			} else if dirNorm == c {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}

		// Apply session_id filter
		if r.sessionIDFilter != "" && sid != r.sessionIDFilter {
			continue
		}

		if best == nil || updatedI > best.updated || (updatedI == best.updated && mtime >= best.mtime) {
			best = &match{id: sid, dir: dir, updated: updatedI, mtime: mtime}
		}
	}

	if best != nil {
		return best.id, best.dir, best.updated
	}
	if r.allowAnySession && bestAny != nil {
		return bestAny.id, bestAny.dir, bestAny.updated
	}
	return "", "", -1
}

// readMessages returns sorted messages for a session.
// Tries SQLite first, then falls back to JSON files.
func (r *OpenCodeLogReader) readMessages(sessionID string) []map[string]interface{} {
	messages := r.readMessagesFromDB(sessionID)
	if len(messages) > 0 {
		sort.Slice(messages, func(i, j int) bool {
			return messageSortKey(messages[i]) < messageSortKey(messages[j])
		})
		return messages
	}
	return r.readMessagesFromFiles(sessionID)
}

func (r *OpenCodeLogReader) readMessagesFromDB(sessionID string) []map[string]interface{} {
	rows, err := r.fetchOpenCodeDBRows(
		"SELECT id, session_id, time_created, time_updated, data FROM message WHERE session_id = ? ORDER BY time_created ASC, time_updated ASC, id ASC",
		sessionID,
	)
	if err != nil || len(rows) == 0 {
		return nil
	}
	var messages []map[string]interface{}
	for _, row := range rows {
		payload := loadJSONBlob(row["data"])
		if payload == nil {
			payload = make(map[string]interface{})
		}
		if _, ok := payload["id"]; !ok {
			payload["id"] = dbString(row["id"])
		}
		if _, ok := payload["sessionID"]; !ok {
			payload["sessionID"] = dbString(row["session_id"])
		}
		timeData, _ := payload["time"].(map[string]interface{})
		if timeData == nil {
			timeData = make(map[string]interface{})
		}
		if timeData["created"] == nil {
			timeData["created"] = dbFloat64(row["time_created"])
		}
		if timeData["updated"] == nil {
			timeData["updated"] = dbFloat64(row["time_updated"])
		}
		payload["time"] = timeData
		messages = append(messages, payload)
	}
	return messages
}

func (r *OpenCodeLogReader) readMessagesFromFiles(sessionID string) []map[string]interface{} {
	msgDir := r.messageDir(sessionID)
	if _, err := os.Stat(msgDir); err != nil {
		return nil
	}
	entries, err := os.ReadDir(msgDir)
	if err != nil {
		return nil
	}
	var messages []map[string]interface{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "msg_") || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		path := filepath.Join(msgDir, e.Name())
		payload := r.loadJSON(path)
		if payload == nil {
			continue
		}
		if strOrEmpty(payload["sessionID"]) != sessionID {
			continue
		}
		payload["_path"] = path
		messages = append(messages, payload)
	}
	sort.Slice(messages, func(i, j int) bool {
		return messageSortKey(messages[i]) < messageSortKey(messages[j])
	})
	return messages
}

// readParts returns sorted parts for a message.
// Tries SQLite first, then falls back to JSON files.
func (r *OpenCodeLogReader) readParts(messageID string) []map[string]interface{} {
	parts := r.readPartsFromDB(messageID)
	if len(parts) > 0 {
		sort.Slice(parts, func(i, j int) bool {
			return partSortKey(parts[i]) < partSortKey(parts[j])
		})
		return parts
	}
	return r.readPartsFromFiles(messageID)
}

func (r *OpenCodeLogReader) readPartsFromDB(messageID string) []map[string]interface{} {
	rows, err := r.fetchOpenCodeDBRows(
		"SELECT id, message_id, session_id, time_created, time_updated, data FROM part WHERE message_id = ? ORDER BY time_created ASC, time_updated ASC, id ASC",
		messageID,
	)
	if err != nil || len(rows) == 0 {
		return nil
	}
	var parts []map[string]interface{}
	for _, row := range rows {
		payload := loadJSONBlob(row["data"])
		if payload == nil {
			payload = make(map[string]interface{})
		}
		if _, ok := payload["id"]; !ok {
			payload["id"] = dbString(row["id"])
		}
		if _, ok := payload["messageID"]; !ok {
			payload["messageID"] = dbString(row["message_id"])
		}
		if _, ok := payload["sessionID"]; !ok {
			payload["sessionID"] = dbString(row["session_id"])
		}
		timeData, _ := payload["time"].(map[string]interface{})
		if timeData == nil {
			timeData = make(map[string]interface{})
		}
		if timeData["start"] == nil {
			timeData["start"] = dbFloat64(row["time_created"])
		}
		if timeData["updated"] == nil {
			timeData["updated"] = dbFloat64(row["time_updated"])
		}
		payload["time"] = timeData
		parts = append(parts, payload)
	}
	return parts
}

func (r *OpenCodeLogReader) readPartsFromFiles(messageID string) []map[string]interface{} {
	partDir := r.partDir(messageID)
	if _, err := os.Stat(partDir); err != nil {
		return nil
	}
	entries, err := os.ReadDir(partDir)
	if err != nil {
		return nil
	}
	var parts []map[string]interface{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "prt_") || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		path := filepath.Join(partDir, e.Name())
		payload := r.loadJSON(path)
		if payload == nil {
			continue
		}
		if strOrEmpty(payload["messageID"]) != messageID {
			continue
		}
		payload["_path"] = path
		parts = append(parts, payload)
	}
	sort.Slice(parts, func(i, j int) bool {
		return partSortKey(parts[i]) < partSortKey(parts[j])
	})
	return parts
}

// ExtractText extracts visible text from a list of parts.
func ExtractText(parts []map[string]interface{}, allowReasoningFallback bool) string {
	collect := func(types map[string]bool) string {
		var out []string
		for _, part := range parts {
			if !types[strOrEmpty(part["type"])] {
				continue
			}
			text := strOrEmpty(part["text"])
			if text != "" {
				out = append(out, text)
			}
		}
		return strings.TrimSpace(strings.Join(out, ""))
	}

	text := collect(map[string]bool{"text": true})
	if text != "" {
		return text
	}
	if allowReasoningFallback {
		return collect(map[string]bool{"reasoning": true})
	}
	return ""
}

// CaptureState captures the current session state.
func (r *OpenCodeLogReader) CaptureState() OpenCodeLogState {
	sessionID, _, updated := r.getLatestSession()
	if sessionID == "" {
		return OpenCodeLogState{SessionUpdated: -1}
	}
	state := OpenCodeLogState{
		SessionID:      sessionID,
		SessionUpdated: updated,
	}
	messages := r.readMessages(sessionID)
	for _, msg := range messages {
		if strOrEmpty(msg["role"]) == "assistant" {
			state.AssistantCount++
			mid := strOrEmpty(msg["id"])
			if mid != "" {
				state.LastAssistantID = mid
			}
		}
	}
	return state
}

// WaitForMessage blocks until a new assistant reply appears or timeout expires.
func (r *OpenCodeLogReader) WaitForMessage(state OpenCodeLogState, timeout time.Duration) (string, OpenCodeLogState) {
	return r.readSince(state, timeout, true)
}

// TryGetMessage performs a non-blocking read for a new assistant reply.
func (r *OpenCodeLogReader) TryGetMessage(state OpenCodeLogState) (string, OpenCodeLogState) {
	return r.readSince(state, 0, false)
}

// LatestMessage returns the latest assistant reply text.
func (r *OpenCodeLogReader) LatestMessage() string {
	sessionID, _, _ := r.getLatestSession()
	if sessionID == "" {
		return ""
	}
	messages := r.readMessages(sessionID)
	// Find last assistant message
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if strOrEmpty(msg["role"]) != "assistant" {
			continue
		}
		mid := strOrEmpty(msg["id"])
		if mid == "" {
			continue
		}
		parts := r.readParts(mid)
		text := ExtractText(parts, true)
		if text != "" {
			return text
		}
	}
	return ""
}

// LatestConversations returns up to n recent (user, assistant) pairs.
func (r *OpenCodeLogReader) LatestConversations(n int) []ConvPair {
	sessionID, _, _ := r.getLatestSession()
	if sessionID == "" {
		return nil
	}
	messages := r.readMessages(sessionID)

	var pairs []ConvPair
	var lastUser string
	for _, msg := range messages {
		role := strOrEmpty(msg["role"])
		mid := strOrEmpty(msg["id"])
		if role == "user" {
			if mid != "" {
				parts := r.readParts(mid)
				text := ExtractText(parts, false)
				if text != "" {
					lastUser = text
				}
			}
			continue
		}
		if role == "assistant" && mid != "" {
			parts := r.readParts(mid)
			text := ExtractText(parts, true)
			if text != "" {
				pairs = append(pairs, ConvPair{User: lastUser, Assistant: text})
				lastUser = ""
			}
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

func (r *OpenCodeLogReader) readSince(state OpenCodeLogState, timeout time.Duration, block bool) (string, OpenCodeLogState) {
	deadline := time.Now().Add(timeout)
	if !block {
		deadline = time.Now()
	}
	currentState := state

	for {
		sessionID, _, updated := r.getLatestSession()
		if sessionID == "" {
			if !block || !time.Now().Before(deadline) {
				return "", currentState
			}
			pollSleep(r.pollInterval)
			continue
		}

		// Session changed?
		if sessionID != currentState.SessionID {
			currentState.SessionID = sessionID
			currentState.SessionUpdated = updated
			currentState.AssistantCount = 0
			currentState.LastAssistantID = ""
			currentState.LastAssistantDone = false
		}

		messages := r.readMessages(sessionID)
		assistantCount := 0
		var lastAssistantID string
		for _, msg := range messages {
			if strOrEmpty(msg["role"]) == "assistant" {
				assistantCount++
				mid := strOrEmpty(msg["id"])
				if mid != "" {
					lastAssistantID = mid
				}
			}
		}

		if assistantCount > currentState.AssistantCount && lastAssistantID != "" {
			// Check if the assistant has finished streaming by checking the
			// completed timestamp, matching the Python implementation.
			completed := false
			for i := len(messages) - 1; i >= 0; i-- {
				msg := messages[i]
				if strOrEmpty(msg["role"]) == "assistant" && strOrEmpty(msg["id"]) == lastAssistantID {
					if timeData, ok := msg["time"].(map[string]interface{}); ok {
						if timeData["completed"] != nil {
							completed = true
						}
					}
					break
				}
			}

			parts := r.readParts(lastAssistantID)
			text := ExtractText(parts, true)

			// If no completed timestamp, check for completion markers as fallback.
			if !completed && text != "" {
				completionMarker := os.Getenv("CCB_EXECUTION_COMPLETE_MARKER")
				if completionMarker == "" {
					completionMarker = "[EXECUTION_COMPLETE]"
				}
				if strings.Contains(text, completionMarker) || strings.Contains(text, "CCB_DONE:") {
					completed = true
				}
			}

			if !completed {
				// Still streaming — don't return partial response yet.
				if !block || !time.Now().Before(deadline) {
					return "", currentState
				}
				pollSleep(r.pollInterval)
				continue
			}

			if text != "" {
				newState := OpenCodeLogState{
					SessionID:         sessionID,
					SessionUpdated:    updated,
					AssistantCount:    assistantCount,
					LastAssistantID:   lastAssistantID,
					LastAssistantDone: true,
				}
				return text, newState
			}
		}
		currentState.AssistantCount = assistantCount
		currentState.LastAssistantID = lastAssistantID

		if !block || !time.Now().Before(deadline) {
			return "", currentState
		}
		pollSleep(r.pollInterval)
	}
}

// ---------------------------------------------------------------------------
// Sort key helpers
// ---------------------------------------------------------------------------

func messageSortKey(m map[string]interface{}) string {
	var created int64 = -1
	if timeObj, ok := m["time"].(map[string]interface{}); ok {
		if v, ok := timeObj["created"].(float64); ok {
			created = int64(v)
		}
	}
	mid := strOrEmpty(m["id"])
	return strings.Join([]string{
		padInt64(created),
		mid,
	}, "|")
}

func partSortKey(p map[string]interface{}) string {
	var start int64 = -1
	if timeObj, ok := p["time"].(map[string]interface{}); ok {
		if v, ok := timeObj["start"].(float64); ok {
			start = int64(v)
		}
	}
	pid := strOrEmpty(p["id"])
	return strings.Join([]string{
		padInt64(start),
		pid,
	}, "|")
}

func padInt64(v int64) string {
	if v < 0 {
		return "00000000000000000000"
	}
	result := make([]byte, 20)
	for i := range result {
		result[i] = '0'
	}
	digits := []byte(itoa64(v))
	copy(result[20-len(digits):], digits)
	return string(result)
}

func itoa64(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// ---------------------------------------------------------------------------
// SQLite value extraction helpers
// ---------------------------------------------------------------------------

// dbString extracts a string from a sql.Scan value (which may be int64, float64, []byte, string, or nil).
func dbString(v interface{}) string {
	if v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	case []byte:
		return string(val)
	case int64:
		return itoa64(val)
	case float64:
		return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%f", val), "0"), ".")
	default:
		return fmt.Sprintf("%v", val)
	}
}

// dbInt64 extracts an int64 from a sql.Scan value.
func dbInt64(v interface{}) int64 {
	if v == nil {
		return -1
	}
	switch val := v.(type) {
	case int64:
		return val
	case float64:
		return int64(val)
	case string:
		n, _ := parseInt64(val)
		return n
	case []byte:
		n, _ := parseInt64(string(val))
		return n
	default:
		return -1
	}
}

// dbFloat64 extracts a float64 from a sql.Scan value.
func dbFloat64(v interface{}) float64 {
	if v == nil {
		return -1
	}
	switch val := v.(type) {
	case float64:
		return val
	case int64:
		return float64(val)
	case string:
		n, _ := parseInt64(val)
		return float64(n)
	case []byte:
		n, _ := parseInt64(string(val))
		return float64(n)
	default:
		return -1
	}
}

func parseInt64(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	var n int64
	neg := false
	i := 0
	if s[0] == '-' {
		neg = true
		i = 1
	}
	for ; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int64(c-'0')
	}
	if neg {
		n = -n
	}
	return n, true
}

// loadJSONBlob parses a JSON blob from a sql.Scan value (string, []byte, or already map).
func loadJSONBlob(v interface{}) map[string]interface{} {
	if v == nil {
		return nil
	}
	switch val := v.(type) {
	case map[string]interface{}:
		return val
	case string:
		if val == "" {
			return nil
		}
		var result map[string]interface{}
		if json.Unmarshal([]byte(val), &result) == nil {
			return result
		}
		return nil
	case []byte:
		if len(val) == 0 {
			return nil
		}
		var result map[string]interface{}
		if json.Unmarshal(val, &result) == nil {
			return result
		}
		return nil
	default:
		return nil
	}
}

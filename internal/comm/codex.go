// Codex communication module (log-driven version).
//
// Reads replies from ~/.codex/sessions JSONL logs.
//
// Source: claude_code_bridge/lib/codex_comm.py
package comm

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

// DefaultCodexSessionRoot returns the default Codex session root directory.
func DefaultCodexSessionRoot() string {
	if env := os.Getenv("CODEX_SESSION_ROOT"); env != "" {
		return expandHome(env)
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codex", "sessions")
}

// sessionIDPattern matches UUID-style Codex session IDs.
var sessionIDPattern = regexp.MustCompile(
	`(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`,
)

// ---------------------------------------------------------------------------
// CodexLogState tracks the read cursor for Codex JSONL sessions.
// ---------------------------------------------------------------------------

// CodexLogState is the cursor into a Codex session JSONL file.
type CodexLogState struct {
	LogPath string
	Offset  int64
}

// ---------------------------------------------------------------------------
// CodexLogReader reads Codex official logs from ~/.codex/sessions.
// ---------------------------------------------------------------------------

// CodexLogReader reads Codex official JSONL logs.
type CodexLogReader struct {
	Root            string
	preferredLog    string
	sessionIDFilter string
	workDir         string
	pollInterval    time.Duration
	mu              sync.Mutex
}

// NewCodexLogReader creates a new CodexLogReader.
func NewCodexLogReader(root, logPath, sessionIDFilter, workDir string) *CodexLogReader {
	if root == "" {
		root = DefaultCodexSessionRoot()
	}
	if workDir == "" {
		workDir, _ = os.Getwd()
	}
	poll := envFloat("CODEX_POLL_INTERVAL", 0.05)
	poll = clampPollInterval(poll, 0.01, 0.5)
	return &CodexLogReader{
		Root:            root,
		preferredLog:    logPath,
		sessionIDFilter: sessionIDFilter,
		workDir:         workDir,
		pollInterval:    time.Duration(poll * float64(time.Second)),
	}
}

// SetPreferredLog sets the preferred log path.
func (r *CodexLogReader) SetPreferredLog(path string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if path != "" {
		r.preferredLog = path
	}
}

// CurrentLogPath returns the path of the latest log file.
func (r *CodexLogReader) CurrentLogPath() string {
	return r.latestLog()
}

func (r *CodexLogReader) extractCWDFromLog(logPath string) string {
	f, err := os.Open(logPath)
	if err != nil {
		return ""
	}
	defer f.Close()

	// Use bufio.NewReader to read the full first line regardless of length.
	// Codex session_meta lines can be >14KB due to embedded base instructions.
	reader := bufio.NewReaderSize(f, 64*1024)
	lineBytes, err := reader.ReadBytes('\n')
	if len(lineBytes) == 0 {
		return ""
	}
	firstLine := strings.TrimSpace(string(lineBytes))
	if firstLine == "" {
		return ""
	}

	var entry map[string]interface{}
	if json.Unmarshal([]byte(firstLine), &entry) != nil {
		return ""
	}
	if strOrEmpty(entry["type"]) != "session_meta" {
		return ""
	}
	payload, _ := entry["payload"].(map[string]interface{})
	if payload == nil {
		return ""
	}
	cwd := strings.TrimSpace(strOrEmpty(payload["cwd"]))
	if cwd == "" {
		return ""
	}
	return strings.ToLower(filepath.Clean(cwd))
}

func (r *CodexLogReader) normalizedWorkDir() string {
	if r.workDir == "" {
		return ""
	}
	cleaned := filepath.Clean(r.workDir)
	return strings.ToLower(cleaned)
}

func (r *CodexLogReader) scanLatest() string {
	if _, err := os.Stat(r.Root); err != nil {
		return ""
	}
	var latest string
	var latestMtime time.Time

	normWD := r.normalizedWorkDir()
	_ = filepath.Walk(r.Root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || filepath.Ext(path) != ".jsonl" {
			return nil
		}
		if r.sessionIDFilter != "" {
			if !strings.Contains(strings.ToLower(path), strings.ToLower(r.sessionIDFilter)) {
				return nil
			}
		}
		if normWD != "" {
			cwd := r.extractCWDFromLog(path)
			if cwd == "" || cwd != normWD {
				return nil
			}
		}
		if info.ModTime().After(latestMtime) || latest == "" {
			latestMtime = info.ModTime()
			latest = path
		}
		return nil
	})
	return latest
}

func (r *CodexLogReader) scanLatestAny() string {
	if _, err := os.Stat(r.Root); err != nil {
		return ""
	}
	var latest string
	var latestMtime time.Time

	normWD := r.normalizedWorkDir()
	_ = filepath.Walk(r.Root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || filepath.Ext(path) != ".jsonl" {
			return nil
		}
		if normWD != "" {
			cwd := r.extractCWDFromLog(path)
			if cwd == "" || cwd != normWD {
				return nil
			}
		}
		if info.ModTime().After(latestMtime) || latest == "" {
			latestMtime = info.ModTime()
			latest = path
		}
		return nil
	})
	return latest
}

func (r *CodexLogReader) latestLog() string {
	r.mu.Lock()
	preferred := r.preferredLog
	r.mu.Unlock()

	if preferred != "" {
		if _, err := os.Stat(preferred); err == nil {
			if r.sessionIDFilter != "" {
				latestAny := r.scanLatestAny()
				if latestAny != "" && latestAny != preferred {
					threshold := envFloat("CCB_CODEX_STALE_LOG_SECONDS", 10.0)
					if threshold > 0 {
						pi, _ := os.Stat(preferred)
						li, _ := os.Stat(latestAny)
						if pi != nil && li != nil {
							if li.ModTime().Sub(pi.ModTime()).Seconds() >= threshold {
								r.mu.Lock()
								r.preferredLog = latestAny
								r.mu.Unlock()
								return latestAny
							}
						}
					}
				}
				return preferred
			}
			latest := r.scanLatest()
			if latest != "" && latest != preferred {
				pi, _ := os.Stat(preferred)
				li, _ := os.Stat(latest)
				if pi != nil && li != nil && li.ModTime().After(pi.ModTime()) {
					r.mu.Lock()
					r.preferredLog = latest
					r.mu.Unlock()
					return latest
				}
			}
			return preferred
		}
	}

	latest := r.scanLatest()
	if latest != "" {
		r.mu.Lock()
		r.preferredLog = latest
		r.mu.Unlock()
		return latest
	}
	return ""
}

// CaptureState captures current log path and offset.
func (r *CodexLogReader) CaptureState() CodexLogState {
	log := r.latestLog()
	var offset int64 = -1
	if log != "" {
		if fi, err := os.Stat(log); err == nil {
			offset = fi.Size()
		}
	}
	return CodexLogState{LogPath: log, Offset: offset}
}

// WaitForMessage blocks until a new assistant reply appears or timeout expires.
func (r *CodexLogReader) WaitForMessage(state CodexLogState, timeout time.Duration) (string, CodexLogState) {
	return r.readSince(state, timeout, true)
}

// TryGetMessage performs a non-blocking read for a new assistant reply.
func (r *CodexLogReader) TryGetMessage(state CodexLogState) (string, CodexLogState) {
	return r.readSince(state, 0, false)
}

// WaitForEvent blocks until a new event appears or timeout expires.
func (r *CodexLogReader) WaitForEvent(state CodexLogState, timeout time.Duration) (*Event, CodexLogState) {
	return r.readEventSince(state, timeout, true)
}

// TryGetEvent performs a non-blocking read for a new event.
func (r *CodexLogReader) TryGetEvent(state CodexLogState) (*Event, CodexLogState) {
	return r.readEventSince(state, 0, false)
}

// LatestMessage returns the latest assistant reply from the log.
func (r *CodexLogReader) LatestMessage() string {
	logPath := r.latestLog()
	if logPath == "" {
		return ""
	}
	tailBytes := envInt("CODEX_LOG_TAIL_BYTES", 8*1024*1024)
	tailLines := envInt("CODEX_LOG_TAIL_LINES", 5000)
	lines := iterLinesReverse(logPath, tailBytes, tailLines)
	for _, line := range lines {
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var entry map[string]interface{}
		if json.Unmarshal([]byte(line), &entry) != nil {
			continue
		}
		if msg := codexExtractMessage(entry); msg != "" {
			return msg
		}
	}
	return ""
}

// LatestConversations returns up to n recent (question, reply) pairs.
func (r *CodexLogReader) LatestConversations(n int) []ConvPair {
	logPath := r.latestLog()
	if logPath == "" || n <= 0 {
		return nil
	}
	tailBytes := envInt("CODEX_LOG_CONV_TAIL_BYTES", 32*1024*1024)
	tailLines := envInt("CODEX_LOG_CONV_TAIL_LINES", 20000)
	lines := iterLinesReverse(logPath, tailBytes, tailLines)

	var pairsRev []ConvPair
	var pendingReply string
	for _, line := range lines {
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var entry map[string]interface{}
		if json.Unmarshal([]byte(line), &entry) != nil {
			continue
		}
		if pendingReply == "" {
			aiMsg := codexExtractMessage(entry)
			if aiMsg != "" {
				pendingReply = aiMsg
			}
			continue
		}
		userMsg := codexExtractUserMessage(entry)
		if userMsg != "" {
			pairsRev = append(pairsRev, ConvPair{User: userMsg, Assistant: pendingReply})
			pendingReply = ""
			if len(pairsRev) >= n {
				break
			}
		}
	}
	// Reverse
	pairs := make([]ConvPair, len(pairsRev))
	for i, p := range pairsRev {
		pairs[len(pairsRev)-1-i] = p
	}
	return pairs
}

// ExtractSessionID extracts a UUID session ID from a log file path or contents.
func ExtractSessionID(logPath string) string {
	base := filepath.Base(logPath)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	for _, src := range []string{stem, base} {
		m := sessionIDPattern.FindString(src)
		if m != "" {
			return m
		}
	}
	f, err := os.Open(logPath)
	if err != nil {
		return ""
	}
	defer f.Close()

	reader := bufio.NewReaderSize(f, 64*1024)
	lineBytes, _ := reader.ReadBytes('\n')
	if len(lineBytes) == 0 {
		return ""
	}
	firstLine := string(lineBytes)
	if idx := strings.IndexByte(firstLine, '\n'); idx >= 0 {
		firstLine = firstLine[:idx]
	}
	m := sessionIDPattern.FindString(firstLine)
	if m != "" {
		return m
	}
	var entry map[string]interface{}
	if json.Unmarshal([]byte(strings.TrimSpace(firstLine)), &entry) != nil {
		return ""
	}
	payload, _ := entry["payload"].(map[string]interface{})
	candidates := []string{
		strOrEmpty(entry["session_id"]),
	}
	if payload != nil {
		candidates = append(candidates, strOrEmpty(payload["id"]))
		if sess, ok := payload["session"].(map[string]interface{}); ok {
			candidates = append(candidates, strOrEmpty(sess["id"]))
		}
	}
	for _, c := range candidates {
		m := sessionIDPattern.FindString(c)
		if m != "" {
			return m
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// Codex JSONL message extraction
// ---------------------------------------------------------------------------

// codexExtractMessage extracts an assistant reply from a Codex JSONL entry.
func codexExtractMessage(entry map[string]interface{}) string {
	entryType := strOrEmpty(entry["type"])
	payload, _ := entry["payload"].(map[string]interface{})
	if payload == nil {
		payload = map[string]interface{}{}
	}

	if entryType == "response_item" {
		if strOrEmpty(payload["type"]) != "message" {
			return ""
		}
		if strOrEmpty(payload["role"]) == "user" {
			return ""
		}
		content := payload["content"]
		if items, ok := content.([]interface{}); ok {
			var texts []string
			for _, raw := range items {
				item, ok := raw.(map[string]interface{})
				if !ok {
					continue
				}
				itype := strOrEmpty(item["type"])
				if itype == "output_text" || itype == "text" {
					t := strings.TrimSpace(strOrEmpty(item["text"]))
					if t != "" {
						texts = append(texts, t)
					}
				}
			}
			if len(texts) > 0 {
				return strings.TrimSpace(strings.Join(texts, "\n"))
			}
		}
		if s, ok := content.(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
		if msg := strings.TrimSpace(strOrEmpty(payload["message"])); msg != "" {
			return msg
		}
		return ""
	}

	if entryType == "event_msg" {
		ptype := strOrEmpty(payload["type"])
		if ptype == "agent_message" || ptype == "assistant_message" || ptype == "assistant" || ptype == "assistant_response" || ptype == "message" {
			if strOrEmpty(payload["role"]) == "user" {
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

	// Fallback
	if strOrEmpty(payload["role"]) == "assistant" {
		for _, key := range []string{"message", "content", "text"} {
			if msg := strings.TrimSpace(strOrEmpty(payload[key])); msg != "" {
				return msg
			}
		}
	}
	return ""
}

// codexExtractUserMessage extracts a user question from a Codex JSONL entry.
func codexExtractUserMessage(entry map[string]interface{}) string {
	entryType := strOrEmpty(entry["type"])
	payload, _ := entry["payload"].(map[string]interface{})
	if payload == nil {
		payload = map[string]interface{}{}
	}

	if entryType == "event_msg" && strOrEmpty(payload["type"]) == "user_message" {
		if msg := strings.TrimSpace(strOrEmpty(payload["message"])); msg != "" {
			return msg
		}
	}

	if entryType == "response_item" {
		if strOrEmpty(payload["type"]) == "message" && strOrEmpty(payload["role"]) == "user" {
			if content, ok := payload["content"].([]interface{}); ok {
				var texts []string
				for _, raw := range content {
					item, ok := raw.(map[string]interface{})
					if !ok {
						continue
					}
					if strOrEmpty(item["type"]) == "input_text" {
						t := strings.TrimSpace(strOrEmpty(item["text"]))
						if t != "" {
							texts = append(texts, t)
						}
					}
				}
				if len(texts) > 0 {
					return strings.TrimSpace(strings.Join(texts, "\n"))
				}
			}
		}
	}
	return ""
}

// codexExtractEvent extracts a (role, text) event from a Codex JSONL entry.
func codexExtractEvent(entry map[string]interface{}) *Event {
	if userMsg := codexExtractUserMessage(entry); userMsg != "" {
		return &Event{Role: "user", Text: userMsg}
	}
	if aiMsg := codexExtractMessage(entry); aiMsg != "" {
		return &Event{Role: "assistant", Text: aiMsg}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Codex read loops
// ---------------------------------------------------------------------------

func (r *CodexLogReader) readSince(state CodexLogState, timeout time.Duration, block bool) (string, CodexLogState) {
	deadline := time.Now().Add(timeout)
	currentPath := state.LogPath
	offset := state.Offset

	rescanInterval := timeout / 2
	if rescanInterval < 200*time.Millisecond {
		rescanInterval = 200 * time.Millisecond
	}
	if rescanInterval > 2*time.Second {
		rescanInterval = 2 * time.Second
	}
	lastRescan := time.Now()

	for {
		logPath := r.ensureLog(currentPath)
		if logPath == "" {
			if !block {
				return "", CodexLogState{LogPath: "", Offset: 0}
			}
			pollSleep(r.pollInterval)
			if !time.Now().Before(deadline) {
				return "", CodexLogState{LogPath: "", Offset: 0}
			}
			continue
		}

		fi, _ := os.Stat(logPath)
		var size int64
		if fi != nil {
			size = fi.Size()
		}

		if offset < 0 {
			offset = size
		}
		if offset > size {
			offset = size
		}

		msg, newOffset := r.readLinesFrom(logPath, offset)
		offset = newOffset
		if msg != "" {
			return msg, CodexLogState{LogPath: logPath, Offset: offset}
		}

		if time.Since(lastRescan) >= rescanInterval {
			latest := r.scanLatest()
			if latest != "" && latest != logPath {
				currentPath = latest
				r.mu.Lock()
				r.preferredLog = latest
				r.mu.Unlock()
				offset = 0
				if !block {
					return "", CodexLogState{LogPath: currentPath, Offset: offset}
				}
				pollSleep(r.pollInterval)
				lastRescan = time.Now()
				continue
			}
			lastRescan = time.Now()
		}

		if !block {
			return "", CodexLogState{LogPath: logPath, Offset: offset}
		}
		pollSleep(r.pollInterval)
		if !time.Now().Before(deadline) {
			return "", CodexLogState{LogPath: logPath, Offset: offset}
		}
	}
}

func (r *CodexLogReader) readEventSince(state CodexLogState, timeout time.Duration, block bool) (*Event, CodexLogState) {
	deadline := time.Now().Add(timeout)
	currentPath := state.LogPath
	offset := state.Offset

	rescanInterval := timeout / 2
	if rescanInterval < 200*time.Millisecond {
		rescanInterval = 200 * time.Millisecond
	}
	if rescanInterval > 2*time.Second {
		rescanInterval = 2 * time.Second
	}
	lastRescan := time.Now()

	for {
		logPath := r.ensureLog(currentPath)
		if logPath == "" {
			if !block {
				return nil, CodexLogState{LogPath: "", Offset: 0}
			}
			pollSleep(r.pollInterval)
			if !time.Now().Before(deadline) {
				return nil, CodexLogState{LogPath: "", Offset: 0}
			}
			continue
		}

		fi, _ := os.Stat(logPath)
		var size int64
		if fi != nil {
			size = fi.Size()
		}
		if offset < 0 {
			offset = size
		}
		if offset > size {
			offset = size
		}

		event, newOffset := r.readEventLinesFrom(logPath, offset)
		offset = newOffset
		if event != nil {
			return event, CodexLogState{LogPath: logPath, Offset: offset}
		}

		if time.Since(lastRescan) >= rescanInterval {
			latest := r.scanLatest()
			if latest != "" && latest != logPath {
				currentPath = latest
				r.mu.Lock()
				r.preferredLog = latest
				r.mu.Unlock()
				offset = 0
				if !block {
					return nil, CodexLogState{LogPath: currentPath, Offset: offset}
				}
				pollSleep(r.pollInterval)
				lastRescan = time.Now()
				continue
			}
			lastRescan = time.Now()
		}

		if !block {
			return nil, CodexLogState{LogPath: logPath, Offset: offset}
		}
		pollSleep(r.pollInterval)
		if !time.Now().Before(deadline) {
			return nil, CodexLogState{LogPath: logPath, Offset: offset}
		}
	}
}

func (r *CodexLogReader) ensureLog(currentPath string) string {
	r.mu.Lock()
	preferred := r.preferredLog
	r.mu.Unlock()
	if preferred != "" {
		if _, err := os.Stat(preferred); err == nil {
			return preferred
		}
	}
	if currentPath != "" {
		if _, err := os.Stat(currentPath); err == nil {
			return currentPath
		}
	}
	latest := r.scanLatest()
	if latest != "" {
		r.mu.Lock()
		r.preferredLog = latest
		r.mu.Unlock()
	}
	return latest
}

func (r *CodexLogReader) readLinesFrom(logPath string, offset int64) (string, int64) {
	f, err := os.Open(logPath)
	if err != nil {
		return "", offset
	}
	defer f.Close()

	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return "", offset
	}

	reader := bufio.NewReader(f)
	pos := offset

	for {
		lineBytes, err := reader.ReadBytes('\n')
		if len(lineBytes) > 0 {
			// Complete line (ends with \n)
			if lineBytes[len(lineBytes)-1] == '\n' {
				pos += int64(len(lineBytes))
				line := strings.TrimSpace(string(lineBytes))
				if line == "" {
					continue
				}
				var entry map[string]interface{}
				if json.Unmarshal([]byte(line), &entry) != nil {
					continue
				}
				if msg := codexExtractMessage(entry); msg != "" {
					return msg, pos
				}
				continue
			}
			// Incomplete line (no trailing \n, hit EOF) — don't advance offset
			return "", pos
		}
		if err != nil {
			return "", pos
		}
	}
}

func (r *CodexLogReader) readEventLinesFrom(logPath string, offset int64) (*Event, int64) {
	f, err := os.Open(logPath)
	if err != nil {
		return nil, offset
	}
	defer f.Close()

	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, offset
	}

	reader := bufio.NewReader(f)
	pos := offset

	for {
		lineBytes, err := reader.ReadBytes('\n')
		if len(lineBytes) > 0 {
			// Complete line (ends with \n)
			if lineBytes[len(lineBytes)-1] == '\n' {
				pos += int64(len(lineBytes))
				line := strings.TrimSpace(string(lineBytes))
				if line == "" {
					continue
				}
				var entry map[string]interface{}
				if json.Unmarshal([]byte(line), &entry) != nil {
					continue
				}
				if event := codexExtractEvent(entry); event != nil {
					return event, pos
				}
				continue
			}
			// Incomplete line (no trailing \n, hit EOF) — don't advance offset
			return nil, pos
		}
		if err != nil {
			return nil, pos
		}
	}
}

// ---------------------------------------------------------------------------
// Reverse line iteration (for latest_message/latest_conversations)
// ---------------------------------------------------------------------------

// iterLinesReverse reads lines from the end of a file, bounded by maxBytes and maxLines.
// Returns lines in reverse chronological order (last line first).
func iterLinesReverse(logPath string, maxBytes, maxLines int) []string {
	if maxBytes <= 0 || maxLines <= 0 {
		return nil
	}
	f, err := os.Open(logPath)
	if err != nil {
		return nil
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return nil
	}
	position := fi.Size()
	bytesRead := 0
	var lines []string
	var buffer []byte

	for position > 0 && bytesRead < maxBytes && len(lines) < maxLines {
		remaining := maxBytes - bytesRead
		readSize := 8192
		if int(position) < readSize {
			readSize = int(position)
		}
		if remaining < readSize {
			readSize = remaining
		}
		position -= int64(readSize)
		f.Seek(position, io.SeekStart)
		chunk := make([]byte, readSize)
		n, _ := f.Read(chunk)
		if n == 0 {
			break
		}
		bytesRead += n
		buffer = append(chunk[:n], buffer...)

		parts := splitByteLines(buffer)
		buffer = parts[0]
		for i := len(parts) - 1; i >= 1; i-- {
			if len(lines) >= maxLines {
				break
			}
			text := strings.TrimSpace(string(parts[i]))
			if text != "" {
				lines = append(lines, text)
			}
		}
	}
	if position == 0 && len(buffer) > 0 && len(lines) < maxLines {
		text := strings.TrimSpace(string(buffer))
		if text != "" {
			lines = append(lines, text)
		}
	}
	return lines
}

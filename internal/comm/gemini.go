// Gemini communication module.
//
// Reads replies from ~/.gemini/tmp/<hash>/chats/session-*.json files.
//
// Source: claude_code_bridge/lib/gemini_comm.py
package comm

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Gemini session root
// ---------------------------------------------------------------------------

// DefaultGeminiRoot returns the default Gemini tmp root.
func DefaultGeminiRoot() string {
	if env := os.Getenv("GEMINI_ROOT"); env != "" {
		return expandHome(env)
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".gemini", "tmp")
}

// ---------------------------------------------------------------------------
// Project hash computation
// ---------------------------------------------------------------------------

var slugifyRE = regexp.MustCompile(`[^a-z0-9]+`)

func slugifyProjectHash(name string) string {
	text := strings.ToLower(strings.TrimSpace(name))
	text = slugifyRE.ReplaceAllString(text, "-")
	return strings.Trim(text, "-")
}

func computeProjectHashes(workDir string) (slugHash, sha256Hash string) {
	if workDir == "" {
		workDir, _ = os.Getwd()
	}
	absPath, err := filepath.Abs(workDir)
	if err != nil {
		absPath = workDir
	}
	basename := filepath.Base(absPath)
	slugHash = slugifyProjectHash(basename)
	h := sha256.Sum256([]byte(absPath))
	sha256Hash = fmt.Sprintf("%x", h)
	return slugHash, sha256Hash
}

// ProjectHashCandidates returns ordered project-hash candidates for a work directory.
func ProjectHashCandidates(workDir, root string) []string {
	if workDir == "" {
		workDir, _ = os.Getwd()
	}
	absPath, _ := filepath.Abs(workDir)
	if absPath == "" {
		absPath = workDir
	}
	rawBase := strings.TrimSpace(filepath.Base(absPath))
	slugBase, sha256Hash := computeProjectHashes(absPath)
	suffixRE := regexp.MustCompile(`^` + regexp.QuoteMeta(slugBase) + `-\d+$`)

	var candidates []string
	seen := make(map[string]bool)
	add := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			return
		}
		seen[v] = true
		candidates = append(candidates, v)
	}

	// Discover matching directories by mtime
	type discovered struct {
		mtime float64
		name  string
	}
	var disc []discovered
	if root != "" && slugBase != "" {
		if entries, err := os.ReadDir(root); err == nil {
			for _, e := range entries {
				if !e.IsDir() {
					continue
				}
				chatsDir := filepath.Join(root, e.Name(), "chats")
				if fi, err := os.Stat(chatsDir); err != nil || !fi.IsDir() {
					continue
				}
				name := e.Name()
				if name == slugBase || name == rawBase || suffixRE.MatchString(name) {
					latestMtime := float64(0)
					pattern := filepath.Join(chatsDir, "session-*.json")
					matches, _ := filepath.Glob(pattern)
					for _, m := range matches {
						if fi, err := os.Stat(m); err == nil {
							mt := float64(fi.ModTime().UnixMilli()) / 1000.0
							if mt > latestMtime {
								latestMtime = mt
							}
						}
					}
					if latestMtime == 0 {
						if fi, err := os.Stat(chatsDir); err == nil {
							latestMtime = float64(fi.ModTime().UnixMilli()) / 1000.0
						}
					}
					disc = append(disc, discovered{mtime: latestMtime, name: name})
				}
			}
		}
	}
	sort.Slice(disc, func(i, j int) bool { return disc[i].mtime > disc[j].mtime })
	for _, d := range disc {
		add(d.name)
	}
	add(slugBase)
	add(rawBase)
	add(sha256Hash)
	return candidates
}

// GetProjectHash returns the Gemini session directory name for a work dir.
func GetProjectHash(workDir string) string {
	root := DefaultGeminiRoot()
	candidates := ProjectHashCandidates(workDir, root)
	for _, ph := range candidates {
		chats := filepath.Join(root, ph, "chats")
		if fi, err := os.Stat(chats); err == nil && fi.IsDir() {
			return ph
		}
	}
	if len(candidates) > 0 {
		return candidates[0]
	}
	return ""
}

// ---------------------------------------------------------------------------
// GeminiLogState tracks the read cursor for Gemini session JSON files.
// ---------------------------------------------------------------------------

// GeminiLogState is the cursor into a Gemini session JSON file.
type GeminiLogState struct {
	SessionPath   string
	MsgCount      int
	Mtime         float64
	MtimeNs       int64
	Size          int64
	LastGeminiID  string
	LastGeminiHash string
}

// ---------------------------------------------------------------------------
// GeminiLogReader reads Gemini session files.
// ---------------------------------------------------------------------------

// GeminiLogReader reads Gemini session files from ~/.gemini/tmp/<hash>/chats.
type GeminiLogReader struct {
	Root             string
	WorkDir          string
	projectHash      string
	allKnownHashes   map[string]bool
	preferredSession string
	pollInterval     time.Duration
	forceReadInterval time.Duration
	mu               sync.Mutex
}

// NewGeminiLogReader creates a new GeminiLogReader.
func NewGeminiLogReader(root, workDir string) *GeminiLogReader {
	if root == "" {
		root = DefaultGeminiRoot()
	}
	if workDir == "" {
		workDir, _ = os.Getwd()
	}
	forcedHash := strings.TrimSpace(os.Getenv("GEMINI_PROJECT_HASH"))
	allHashes := make(map[string]bool)

	var projectHash string
	if forcedHash != "" {
		projectHash = forcedHash
		allHashes[forcedHash] = true
	} else {
		projectHash = GetProjectHash(workDir)
		for _, c := range ProjectHashCandidates(workDir, root) {
			allHashes[c] = true
		}
		allHashes[projectHash] = true
	}

	poll := envFloat("GEMINI_POLL_INTERVAL", 0.05)
	poll = clampPollInterval(poll, 0.02, 0.5)
	force := envFloat("GEMINI_FORCE_READ_INTERVAL", 1.0)
	if force < 0.2 {
		force = 0.2
	}
	if force > 5.0 {
		force = 5.0
	}

	return &GeminiLogReader{
		Root:              root,
		WorkDir:           workDir,
		projectHash:       projectHash,
		allKnownHashes:    allHashes,
		pollInterval:      time.Duration(poll * float64(time.Second)),
		forceReadInterval: time.Duration(force * float64(time.Second)),
	}
}

// SetPreferredSession sets the preferred session file path.
func (r *GeminiLogReader) SetPreferredSession(path string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if path == "" {
		return
	}
	if _, err := os.Stat(path); err == nil {
		r.preferredSession = path
	}
}

// CurrentSessionPath returns the path of the latest session file.
func (r *GeminiLogReader) CurrentSessionPath() string {
	return r.latestSession()
}

func (r *GeminiLogReader) scanLatestSession() string {
	r.mu.Lock()
	primaryHash := r.projectHash
	hashes := make([]string, 0, len(r.allKnownHashes))
	for h := range r.allKnownHashes {
		if h != primaryHash {
			hashes = append(hashes, h)
		}
	}
	r.mu.Unlock()

	scanOrder := []string{primaryHash}
	sort.Strings(hashes)
	scanOrder = append(scanOrder, hashes...)

	var best string
	var bestMtime float64
	winningHash := primaryHash

	for _, ph := range scanOrder {
		chatsDir := filepath.Join(r.Root, ph, "chats")
		if fi, err := os.Stat(chatsDir); err != nil || !fi.IsDir() {
			continue
		}
		entries, err := os.ReadDir(chatsDir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			if filepath.Ext(e.Name()) != ".json" || !strings.HasPrefix(e.Name(), "session-") {
				continue
			}
			path := filepath.Join(chatsDir, e.Name())
			fi, err := os.Stat(path)
			if err != nil {
				continue
			}
			mt := float64(fi.ModTime().UnixMilli()) / 1000.0
			if mt > bestMtime {
				bestMtime = mt
				best = path
				winningHash = ph
			}
		}
	}

	if best != "" && winningHash != primaryHash {
		r.mu.Lock()
		r.projectHash = winningHash
		r.mu.Unlock()
	}
	return best
}

func (r *GeminiLogReader) latestSession() string {
	r.mu.Lock()
	preferred := r.preferredSession
	r.mu.Unlock()

	scanned := r.scanLatestSession()

	if preferred != "" {
		if pi, err := os.Stat(preferred); err == nil {
			if scanned != "" {
				if si, err := os.Stat(scanned); err == nil && si.ModTime().After(pi.ModTime()) {
					r.mu.Lock()
					r.preferredSession = scanned
					r.mu.Unlock()
					return scanned
				}
			}
			return preferred
		}
	}

	if scanned != "" {
		r.mu.Lock()
		r.preferredSession = scanned
		r.mu.Unlock()
		return scanned
	}

	if envBool("GEMINI_ALLOW_ANY_PROJECT_SCAN") {
		// Scan all projects
		var allSessions []string
		pattern := filepath.Join(r.Root, "*", "chats", "session-*.json")
		matches, _ := filepath.Glob(pattern)
		for _, m := range matches {
			if fi, err := os.Stat(m); err == nil && !fi.IsDir() && !strings.HasPrefix(filepath.Base(m), ".") {
				allSessions = append(allSessions, m)
			}
		}
		if len(allSessions) > 0 {
			sort.Slice(allSessions, func(i, j int) bool {
				fi, _ := os.Stat(allSessions[i])
				fj, _ := os.Stat(allSessions[j])
				if fi == nil || fj == nil {
					return false
				}
				return fi.ModTime().Before(fj.ModTime())
			})
			last := allSessions[len(allSessions)-1]
			r.mu.Lock()
			r.preferredSession = last
			r.mu.Unlock()
			return last
		}
	}
	return ""
}

func (r *GeminiLogReader) readSessionJSON(path string) map[string]interface{} {
	if path == "" {
		return nil
	}
	for attempt := 0; attempt < 10; attempt++ {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		var result map[string]interface{}
		if json.Unmarshal(data, &result) == nil {
			return result
		}
		if attempt < 9 {
			time.Sleep(time.Duration(r.pollInterval))
			if r.pollInterval > 50*time.Millisecond {
				time.Sleep(50 * time.Millisecond)
			}
		}
	}
	return nil
}

// CaptureState captures current session file and message count.
func (r *GeminiLogReader) CaptureState() GeminiLogState {
	session := r.latestSession()
	state := GeminiLogState{SessionPath: session}
	if session == "" {
		return state
	}
	fi, err := os.Stat(session)
	if err != nil {
		return state
	}
	state.Mtime = float64(fi.ModTime().UnixMilli()) / 1000.0
	state.MtimeNs = fi.ModTime().UnixNano()
	state.Size = fi.Size()

	data := r.readSessionJSON(session)
	if data == nil {
		state.MsgCount = -1
		return state
	}
	messages, _ := data["messages"].([]interface{})
	state.MsgCount = len(messages)

	lastID, lastContent := extractLastGemini(data)
	if lastContent != "" {
		state.LastGeminiID = lastID
		h := sha256.Sum256([]byte(lastContent))
		state.LastGeminiHash = fmt.Sprintf("%x", h)
	}
	return state
}

// WaitForMessage blocks until a new Gemini reply appears or timeout expires.
func (r *GeminiLogReader) WaitForMessage(state GeminiLogState, timeout time.Duration) (string, GeminiLogState) {
	return r.readSince(state, timeout, true)
}

// TryGetMessage performs a non-blocking read for a new Gemini reply.
func (r *GeminiLogReader) TryGetMessage(state GeminiLogState) (string, GeminiLogState) {
	return r.readSince(state, 0, false)
}

// LatestMessage returns the latest Gemini reply from the session.
func (r *GeminiLogReader) LatestMessage() string {
	session := r.latestSession()
	if session == "" {
		return ""
	}
	data := r.readSessionJSON(session)
	if data == nil {
		return ""
	}
	_, content := extractLastGemini(data)
	return content
}

// LatestConversations returns up to n recent (question, reply) pairs.
func (r *GeminiLogReader) LatestConversations(n int) []ConvPair {
	session := r.latestSession()
	if session == "" {
		return nil
	}
	data := r.readSessionJSON(session)
	if data == nil {
		return nil
	}
	messages, _ := data["messages"].([]interface{})

	var pairs []ConvPair
	var pendingQuestion string
	for _, raw := range messages {
		msg, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		msgType := strOrEmpty(msg["type"])
		content := strOrEmpty(msg["content"])
		if msgType == "user" {
			pendingQuestion = strings.TrimSpace(content)
		} else if msgType == "gemini" && strings.TrimSpace(content) != "" {
			pairs = append(pairs, ConvPair{User: pendingQuestion, Assistant: strings.TrimSpace(content)})
			pendingQuestion = ""
		}
	}
	if n > 0 && len(pairs) > n {
		return pairs[len(pairs)-n:]
	}
	return pairs
}

func (r *GeminiLogReader) readSince(state GeminiLogState, timeout time.Duration, block bool) (string, GeminiLogState) {
	deadline := time.Now().Add(timeout)
	prevCount := state.MsgCount
	unknownBaseline := prevCount < 0
	prevMtimeNs := state.MtimeNs
	prevSize := state.Size
	prevSession := state.SessionPath
	prevLastGeminiID := state.LastGeminiID
	prevLastGeminiHash := state.LastGeminiHash

	rescanInterval := timeout / 2
	if rescanInterval < 200*time.Millisecond {
		rescanInterval = 200 * time.Millisecond
	}
	if rescanInterval > 2*time.Second {
		rescanInterval = 2 * time.Second
	}
	lastRescan := time.Now()
	lastForcedRead := time.Now()

	for {
		if time.Since(lastRescan) >= rescanInterval {
			latest := r.scanLatestSession()
			if latest != "" {
				r.mu.Lock()
				curPref := r.preferredSession
				r.mu.Unlock()
				if latest != curPref {
					r.mu.Lock()
					r.preferredSession = latest
					r.mu.Unlock()
					if latest != prevSession {
						prevCount = 0
						prevMtimeNs = 0
						prevSize = 0
						prevLastGeminiID = ""
						prevLastGeminiHash = ""
					}
				}
			}
			lastRescan = time.Now()
		}

		session := r.latestSession()
		if session == "" {
			if !block {
				return "", GeminiLogState{
					MsgCount: 0, LastGeminiID: prevLastGeminiID, LastGeminiHash: prevLastGeminiHash,
				}
			}
			pollSleep(r.pollInterval)
			if !time.Now().Before(deadline) {
				return "", state
			}
			continue
		}

		fi, err := os.Stat(session)
		if err != nil {
			if !block || !time.Now().Before(deadline) {
				return "", state
			}
			pollSleep(r.pollInterval)
			continue
		}
		currentMtimeNs := fi.ModTime().UnixNano()
		currentSize := fi.Size()

		if block && currentMtimeNs <= prevMtimeNs && currentSize == prevSize {
			if time.Since(lastForcedRead) < r.forceReadInterval {
				pollSleep(r.pollInterval)
				if !time.Now().Before(deadline) {
					return "", GeminiLogState{
						SessionPath: session, MsgCount: prevCount, MtimeNs: prevMtimeNs,
						Size: prevSize, LastGeminiID: prevLastGeminiID, LastGeminiHash: prevLastGeminiHash,
					}
				}
				continue
			}
		}

		data := r.readSessionJSON(session)
		if data == nil {
			if !block || !time.Now().Before(deadline) {
				return "", state
			}
			pollSleep(r.pollInterval)
			continue
		}
		lastForcedRead = time.Now()
		messages, _ := data["messages"].([]interface{})
		currentCount := len(messages)

		if unknownBaseline {
			prevMtimeNs = currentMtimeNs
			prevSize = currentSize
			prevCount = currentCount
			lastID, content := extractLastGemini(data)
			if content != "" {
				prevLastGeminiID = lastID
				h := sha256.Sum256([]byte(content))
				prevLastGeminiHash = fmt.Sprintf("%x", h)
			}
			unknownBaseline = false
			if !block {
				return "", GeminiLogState{
					SessionPath: session, MsgCount: prevCount, MtimeNs: prevMtimeNs,
					Size: prevSize, LastGeminiID: prevLastGeminiID, LastGeminiHash: prevLastGeminiHash,
				}
			}
			pollSleep(r.pollInterval)
			if !time.Now().Before(deadline) {
				return "", GeminiLogState{
					SessionPath: session, MsgCount: prevCount, MtimeNs: prevMtimeNs,
					Size: prevSize, LastGeminiID: prevLastGeminiID, LastGeminiHash: prevLastGeminiHash,
				}
			}
			continue
		}

		if currentCount > prevCount {
			// Find the last new gemini message
			var lastGeminiContent string
			var lastGeminiID string
			var lastGeminiHash string
			for _, raw := range messages[prevCount:] {
				msg, ok := raw.(map[string]interface{})
				if !ok {
					continue
				}
				if strOrEmpty(msg["type"]) != "gemini" {
					continue
				}
				content := strings.TrimSpace(strOrEmpty(msg["content"]))
				if content == "" {
					continue
				}
				h := sha256.Sum256([]byte(content))
				contentHash := fmt.Sprintf("%x", h)
				msgID := strOrEmpty(msg["id"])
				if msgID != "" && msgID == prevLastGeminiID && contentHash == prevLastGeminiHash {
					continue
				}
				lastGeminiContent = content
				lastGeminiID = msgID
				lastGeminiHash = contentHash
			}
			if lastGeminiContent != "" {
				return lastGeminiContent, GeminiLogState{
					SessionPath: session, MsgCount: currentCount, MtimeNs: currentMtimeNs,
					Size: currentSize, LastGeminiID: lastGeminiID, LastGeminiHash: lastGeminiHash,
				}
			}
		} else {
			// Check for in-place content update
			lastID, content := extractLastGemini(data)
			if content != "" {
				h := sha256.Sum256([]byte(content))
				currentHash := fmt.Sprintf("%x", h)
				if lastID != prevLastGeminiID || currentHash != prevLastGeminiHash {
					return content, GeminiLogState{
						SessionPath: session, MsgCount: currentCount, MtimeNs: currentMtimeNs,
						Size: currentSize, LastGeminiID: lastID, LastGeminiHash: currentHash,
					}
				}
			}
		}

		prevMtimeNs = currentMtimeNs
		prevCount = currentCount
		prevSize = currentSize
		lastID, content := extractLastGemini(data)
		if content != "" {
			prevLastGeminiID = lastID
			h := sha256.Sum256([]byte(content))
			prevLastGeminiHash = fmt.Sprintf("%x", h)
		}

		if !block {
			return "", GeminiLogState{
				SessionPath: session, MsgCount: prevCount, MtimeNs: prevMtimeNs,
				Size: prevSize, LastGeminiID: prevLastGeminiID, LastGeminiHash: prevLastGeminiHash,
			}
		}
		pollSleep(r.pollInterval)
		if !time.Now().Before(deadline) {
			return "", GeminiLogState{
				SessionPath: session, MsgCount: prevCount, MtimeNs: prevMtimeNs,
				Size: prevSize, LastGeminiID: prevLastGeminiID, LastGeminiHash: prevLastGeminiHash,
			}
		}
	}
}

// extractLastGemini returns the (id, content) of the last gemini message in a session.
func extractLastGemini(payload map[string]interface{}) (string, string) {
	messages, _ := payload["messages"].([]interface{})
	for i := len(messages) - 1; i >= 0; i-- {
		msg, ok := messages[i].(map[string]interface{})
		if !ok {
			continue
		}
		if strOrEmpty(msg["type"]) != "gemini" {
			continue
		}
		content := strOrEmpty(msg["content"])
		return strOrEmpty(msg["id"]), strings.TrimSpace(content)
	}
	return "", ""
}

// Package laskddaemon provides the Claude-specific daemon (laskd).
// Source: claude_code_bridge/lib/laskd_daemon.py + lib/laskd_registry.py
//
// It manages Claude session bindings with background refresh,
// monitors active sessions, and adapts to session switches.
package laskddaemon

import (
	"container/heap"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/curdx/curdx-bridge/internal/envutil"
	"github.com/curdx/curdx-bridge/internal/filewatcher"
	"github.com/curdx/curdx-bridge/internal/memory"
	"github.com/curdx/curdx-bridge/internal/projectid"
	"github.com/curdx/curdx-bridge/internal/session"
	"github.com/curdx/curdx-bridge/internal/sessionutil"
)

// ClaudeProjectsRoot returns the root directory for Claude project logs.
func ClaudeProjectsRoot() string {
	root := os.Getenv("CLAUDE_PROJECTS_ROOT")
	if root == "" {
		root = os.Getenv("CLAUDE_PROJECT_ROOT")
	}
	if root == "" {
		home, _ := os.UserHomeDir()
		root = filepath.Join(home, ".claude", "projects")
	}
	return root
}

var sessionIDPattern = regexp.MustCompile(`(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
var projectKeyRe = regexp.MustCompile(`[^A-Za-z0-9]`)

// ── Env helpers ──

func envFloat(key string, def float64) float64 {
	raw := os.Getenv(key)
	if raw == "" {
		return def
	}
	var v float64
	_, err := fmt.Sscanf(raw, "%f", &v)
	if err != nil {
		return def
	}
	return v
}

func envInt(key string, def int) int {
	return envutil.EnvInt(key, def)
}

// ── Path helpers ──

func projectKeyForPath(path string) string {
	return projectKeyRe.ReplaceAllString(path, "-")
}

func normalizeProjectPath(value string) string {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return ""
	}
	// Expand ~
	if strings.HasPrefix(raw, "~/") || raw == "~" {
		home, err := os.UserHomeDir()
		if err == nil {
			if raw == "~" {
				raw = home
			} else {
				raw = filepath.Join(home, raw[2:])
			}
		}
	}
	// Resolve to absolute
	abs, err := filepath.Abs(raw)
	if err == nil {
		raw = abs
	}
	raw = filepath.ToSlash(raw)
	raw = strings.TrimRight(raw, "/")
	if runtime.GOOS == "windows" {
		raw = strings.ToLower(raw)
	}
	return raw
}

func candidateProjectPaths(workDir string) []string {
	var candidates []string
	if envPWD := os.Getenv("PWD"); envPWD != "" {
		candidates = append(candidates, envPWD)
	}
	candidates = append(candidates, workDir)
	if abs, err := filepath.Abs(workDir); err == nil && abs != workDir {
		candidates = append(candidates, abs)
	}
	// Resolve symlinks
	if resolved, err := filepath.EvalSymlinks(workDir); err == nil && resolved != workDir {
		candidates = append(candidates, resolved)
	}

	var out []string
	seen := map[string]bool{}
	for _, c := range candidates {
		normalized := normalizeProjectPath(c)
		if normalized == "" || seen[normalized] {
			continue
		}
		seen[normalized] = true
		out = append(out, normalized)
	}
	return out
}

func extractSessionIDFromStartCmd(startCmd string) string {
	if startCmd == "" {
		return ""
	}
	match := sessionIDPattern.FindString(startCmd)
	return match
}

func findLogForSessionID(sessionID, root string) string {
	if sessionID == "" {
		return ""
	}
	if root == "" {
		root = ClaudeProjectsRoot()
	}
	if _, err := os.Stat(root); err != nil {
		return ""
	}

	var latestPath string
	var latestMtime time.Time
	seen := map[string]bool{}

	// Pattern 1: **/sessionID.jsonl
	exactName := sessionID + ".jsonl"
	filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		if seen[p] {
			return nil
		}
		base := filepath.Base(p)
		if base == exactName || strings.Contains(base, sessionID) {
			if filepath.Ext(p) != ".jsonl" {
				return nil
			}
			seen[p] = true
			if info.ModTime().After(latestMtime) || latestPath == "" {
				latestMtime = info.ModTime()
				latestPath = p
			}
		}
		return nil
	})

	return latestPath
}

// readSessionMeta reads cwd, session_id, isSidechain from the first 30 lines.
func readSessionMeta(logPath string) (cwd string, sid string, isSidechain *bool) {
	f, err := os.Open(logPath)
	if err != nil {
		return "", "", nil
	}
	defer f.Close()

	buf := make([]byte, 64*1024)
	n, _ := f.Read(buf)
	if n == 0 {
		return "", "", nil
	}

	lines := strings.SplitN(string(buf[:n]), "\n", 31)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if entry == nil {
			continue
		}

		var cwdStr, sidStr string
		if v, ok := entry["cwd"].(string); ok && v != "" {
			cwdStr = strings.TrimSpace(v)
		} else if v, ok := entry["projectPath"].(string); ok && v != "" {
			cwdStr = strings.TrimSpace(v)
		}
		if v, ok := entry["sessionId"].(string); ok && v != "" {
			sidStr = strings.TrimSpace(v)
		} else if v, ok := entry["id"].(string); ok && v != "" {
			sidStr = strings.TrimSpace(v)
		}

		var scPtr *bool
		if v, ok := entry["isSidechain"].(bool); ok {
			scPtr = &v
		}

		if cwdStr != "" || sidStr != "" {
			return cwdStr, sidStr, scPtr
		}
	}
	return "", "", nil
}

func pathWithin(child, parent string) bool {
	// Expand ~ and resolve
	if strings.HasPrefix(child, "~/") {
		home, _ := os.UserHomeDir()
		child = filepath.Join(home, child[2:])
	}
	if strings.HasPrefix(parent, "~/") {
		home, _ := os.UserHomeDir()
		parent = filepath.Join(home, parent[2:])
	}
	if abs, err := filepath.Abs(child); err == nil {
		child = abs
	}
	if abs, err := filepath.Abs(parent); err == nil {
		parent = abs
	}

	child = filepath.ToSlash(child)
	parent = filepath.ToSlash(parent)
	if runtime.GOOS == "windows" {
		child = strings.ToLower(child)
		parent = strings.ToLower(parent)
	}
	child = strings.TrimRight(child, "/")
	parent = strings.TrimRight(parent, "/")

	if child == parent {
		return true
	}
	return strings.HasPrefix(child, parent+"/")
}

func inferWorkDirFromSessionFile(sessionFile string) string {
	parent := filepath.Dir(sessionFile)
	base := filepath.Base(parent)
	if base == sessionutil.CURDXProjectConfigDirname || base == sessionutil.CURDXProjectConfigLegacyDirname {
		return filepath.Dir(parent)
	}
	return parent
}

func ensureClaudeSessionWorkDirFields(payload map[string]any, sessionFile string) string {
	if payload == nil {
		return ""
	}

	var workDir string
	if raw, ok := payload["work_dir"].(string); ok && strings.TrimSpace(raw) != "" {
		workDir = strings.TrimSpace(raw)
	}
	if workDir == "" {
		workDir = inferWorkDirFromSessionFile(sessionFile)
	}
	if workDir == "" {
		return ""
	}

	payload["work_dir"] = workDir

	if norm, ok := payload["work_dir_norm"].(string); !ok || strings.TrimSpace(norm) == "" {
		payload["work_dir_norm"] = projectid.NormalizeWorkDir(workDir)
	}

	pidRaw, _ := payload["curdx_project_id"].(string)
	if strings.TrimSpace(pidRaw) == "" {
		payload["curdx_project_id"] = projectid.ComputeCURDXProjectID(workDir)
	}

	return workDir
}

// ── Heap-based session scan ──

type mtimeItem struct {
	mtime float64
	path  string
}

type mtimeHeap []mtimeItem

func (h mtimeHeap) Len() int           { return len(h) }
func (h mtimeHeap) Less(i, j int) bool { return h[i].mtime < h[j].mtime }
func (h mtimeHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *mtimeHeap) Push(x any)        { *h = append(*h, x.(mtimeItem)) }
func (h *mtimeHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}

// scanLatestLogForWorkDir scans Claude projects and finds the latest log
// whose cwd/projectPath is within workDir. Uses a bounded heap.
func scanLatestLogForWorkDir(workDir, root string, scanLimit int) (logPath string, sid string) {
	if root == "" {
		root = ClaudeProjectsRoot()
	}
	if _, err := os.Stat(root); err != nil {
		return "", ""
	}

	h := &mtimeHeap{}
	heap.Init(h)

	filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		if filepath.Ext(p) != ".jsonl" || strings.HasPrefix(filepath.Base(p), ".") {
			return nil
		}
		mtime := float64(info.ModTime().UnixMilli()) / 1000.0
		item := mtimeItem{mtime, p}
		if h.Len() < scanLimit {
			heap.Push(h, item)
		} else if mtime > (*h)[0].mtime {
			heap.Pop(h)
			heap.Push(h, item)
		}
		return nil
	})

	// Sort candidates by mtime descending
	items := make([]mtimeItem, h.Len())
	for i := h.Len() - 1; i >= 0; i-- {
		items[i] = heap.Pop(h).(mtimeItem)
	}

	for _, item := range items {
		cwd, sessID, isSidechain := readSessionMeta(item.path)
		if isSidechain != nil && *isSidechain {
			continue
		}
		if cwd == "" {
			continue
		}
		if pathWithin(cwd, workDir) {
			return item.path, sessID
		}
	}
	return "", ""
}

// parseSessionsIndex parses sessions-index.json to find the correct session.
func parseSessionsIndex(workDir, root string) string {
	if root == "" {
		root = ClaudeProjectsRoot()
	}
	candidates := make(map[string]bool)
	for _, c := range candidateProjectPaths(workDir) {
		candidates[c] = true
	}

	projectKey := projectKeyForPath(workDir)
	projectDir := filepath.Join(root, projectKey)
	indexPath := filepath.Join(projectDir, "sessions-index.json")

	if _, err := os.Stat(indexPath); err != nil {
		// Try resolved path
		if abs, err := filepath.Abs(workDir); err == nil && abs != workDir {
			altKey := projectKeyForPath(abs)
			indexPath = filepath.Join(root, altKey, "sessions-index.json")
			projectDir = filepath.Join(root, altKey)
		}
		if _, err := os.Stat(indexPath); err != nil {
			// Try with symlink resolution
			if resolved, err := filepath.EvalSymlinks(workDir); err == nil && resolved != workDir {
				altKey := projectKeyForPath(resolved)
				indexPath = filepath.Join(root, altKey, "sessions-index.json")
				projectDir = filepath.Join(root, altKey)
			}
		}
	}
	if _, err := os.Stat(indexPath); err != nil {
		return ""
	}

	data, err := os.ReadFile(indexPath)
	if err != nil {
		return ""
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return ""
	}

	entriesRaw, ok := payload["entries"]
	if !ok {
		return ""
	}
	entriesSlice, ok := entriesRaw.([]any)
	if !ok {
		return ""
	}

	var bestPath string
	bestMtime := int64(-1)

	for _, eRaw := range entriesSlice {
		entry, ok := eRaw.(map[string]any)
		if !ok {
			continue
		}
		if sc, ok := entry["isSidechain"].(bool); ok && sc {
			continue
		}
		if pp, ok := entry["projectPath"].(string); ok && strings.TrimSpace(pp) != "" {
			normalized := normalizeProjectPath(pp)
			if len(candidates) > 0 && normalized != "" && !candidates[normalized] {
				continue
			}
		} else if len(candidates) > 0 {
			continue
		}

		fullPath, ok := entry["fullPath"].(string)
		if !ok || strings.TrimSpace(fullPath) == "" {
			continue
		}

		sessionPath := fullPath
		if strings.HasPrefix(sessionPath, "~") {
			home, _ := os.UserHomeDir()
			if sessionPath == "~" {
				sessionPath = home
			} else if strings.HasPrefix(sessionPath, "~/") {
				sessionPath = filepath.Join(home, sessionPath[2:])
			}
		}
		if !filepath.IsAbs(sessionPath) {
			sessionPath = filepath.Join(projectDir, sessionPath)
		}
		if _, err := os.Stat(sessionPath); err != nil {
			continue
		}

		var mtime int64
		switch v := entry["fileMtime"].(type) {
		case float64:
			mtime = int64(v)
		case string:
			fmt.Sscanf(strings.TrimSpace(v), "%d", &mtime)
		default:
			info, err := os.Stat(sessionPath)
			if err != nil {
				continue
			}
			mtime = info.ModTime().UnixMilli()
		}

		if mtime > bestMtime {
			bestMtime = mtime
			bestPath = sessionPath
		}
	}
	return bestPath
}

func shouldOverwriteBinding(current, candidate string) bool {
	if current == "" {
		return true
	}
	if _, err := os.Stat(current); err != nil {
		return true
	}
	currentInfo, err := os.Stat(current)
	if err != nil {
		return true
	}
	candidateInfo, err := os.Stat(candidate)
	if err != nil {
		return true
	}
	return candidateInfo.ModTime().After(currentInfo.ModTime())
}

// refreshClaudeLogBinding refreshes a session's Claude log binding.
// Priority: 1) session_id from start_cmd, 2) sessions-index.json, 3) scan by work_dir.
func refreshClaudeLogBinding(
	sess *session.ClaudeProjectSession,
	root string,
	scanLimit int,
	forceScan bool,
) bool {
	currentLogStr := sess.ClaudeSessionPath()
	currentLog := currentLogStr

	data := sess.GetDataSnapshot()
	startCmd := ""
	if v, ok := data["claude_start_cmd"].(string); ok && v != "" {
		startCmd = strings.TrimSpace(v)
	} else if v, ok := data["start_cmd"].(string); ok && v != "" {
		startCmd = strings.TrimSpace(v)
	}

	intendedSID := extractSessionIDFromStartCmd(startCmd)
	var intendedLog string
	if intendedSID != "" {
		intendedLog = findLogForSessionID(intendedSID, root)
		if intendedLog != "" {
			if _, err := os.Stat(intendedLog); err == nil {
				if shouldOverwriteBinding(currentLog, intendedLog) || sess.ClaudeSessionID() != intendedSID {
					sess.UpdateClaudeBinding(intendedLog, intendedSID)
					return true
				}
				return false
			}
		}
	}

	indexSession := parseSessionsIndex(sess.WorkDir(), root)
	if indexSession != "" {
		if _, err := os.Stat(indexSession); err == nil {
			indexSID := strings.TrimSuffix(filepath.Base(indexSession), filepath.Ext(indexSession))
			if shouldOverwriteBinding(currentLog, indexSession) || sess.ClaudeSessionID() != indexSID {
				sess.UpdateClaudeBinding(indexSession, indexSID)
				return true
			}
			if !forceScan {
				return false
			}
		}
	}

	needScan := forceScan || (intendedLog == "" && indexSession == "")
	if !needScan {
		return false
	}

	candidateLog, candidateSID := scanLatestLogForWorkDir(sess.WorkDir(), root, scanLimit)
	if candidateLog == "" {
		return false
	}
	if _, err := os.Stat(candidateLog); err != nil {
		return false
	}

	if shouldOverwriteBinding(currentLog, candidateLog) || (candidateSID != "" && candidateSID != sess.ClaudeSessionID()) {
		sess.UpdateClaudeBinding(candidateLog, candidateSID)
		return true
	}
	return false
}

// ── Auto-extract ──

var (
	autoTransferMu   sync.Mutex
	autoTransferSeen = map[string]float64{}
)

func autoTransferKey(workDir, sessionPath string) string {
	return workDir + "::" + sessionPath
}

func maybeAutoExtractOldSession(oldSessionPath, workDir string) {
	if !envutil.EnvBool("CURDX_CTX_TRANSFER_ON_SESSION_SWITCH", true) {
		return
	}
	if oldSessionPath == "" {
		return
	}
	if _, err := os.Stat(oldSessionPath); err != nil {
		return
	}

	key := autoTransferKey(workDir, oldSessionPath)
	now := float64(time.Now().Unix())

	autoTransferMu.Lock()
	if _, exists := autoTransferSeen[key]; exists {
		autoTransferMu.Unlock()
		return
	}
	// Prune stale keys (1h)
	for k, ts := range autoTransferSeen {
		if now-ts > 3600 {
			delete(autoTransferSeen, k)
		}
	}
	autoTransferSeen[key] = now
	autoTransferMu.Unlock()

	go func() {
		defer func() { recover() }()
		lastN := envutil.EnvInt("CURDX_CTX_TRANSFER_LAST_N", 0)
		maxTokens := envutil.EnvInt("CURDX_CTX_TRANSFER_MAX_TOKENS", 8000)
		format := strings.TrimSpace(strings.ToLower(os.Getenv("CURDX_CTX_TRANSFER_FORMAT")))
		if format == "" {
			format = "markdown"
		}
		provider := strings.TrimSpace(strings.ToLower(os.Getenv("CURDX_CTX_TRANSFER_PROVIDER")))
		if provider == "" {
			provider = "auto"
		}

		transfer := memory.NewContextTransfer(maxTokens, workDir)
		ctx, err := transfer.ExtractConversations(oldSessionPath, lastN, false, "", "", "")
		if err != nil || ctx == nil || len(ctx.Conversations) == 0 {
			return
		}
		ts := time.Now().Format("20060102-150405")
		base := strings.TrimSuffix(filepath.Base(oldSessionPath), filepath.Ext(oldSessionPath))
		filename := fmt.Sprintf("claude-%s-%s", ts, base)
		transfer.SaveTransfer(ctx, format, provider, filename)
	}()
}

// ── Logging ──

func writeLog(line string) {
	// Best-effort log to laskd.log; import cycle prevention means we inline the path logic.
	cacheDir := os.Getenv("CURDX_RUN_DIR")
	if cacheDir == "" {
		cacheDir = os.Getenv("XDG_CACHE_HOME")
		if cacheDir != "" {
			cacheDir = filepath.Join(cacheDir, "curdx")
		} else {
			home, _ := os.UserHomeDir()
			cacheDir = filepath.Join(home, ".cache", "curdx")
		}
	}
	logPath := filepath.Join(cacheDir, "cxb-claude-askd.log")
	os.MkdirAll(filepath.Dir(logPath), 0o755)
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	f.WriteString(strings.TrimRight(line, " \t\r\n") + "\n")
}

// ── Session Entry ──

type sessionEntry struct {
	workDir         string
	session         *session.ClaudeProjectSession
	sessionFile     string
	fileMtime       float64
	lastCheck       float64
	valid           bool
	nextBindRefresh float64
	bindBackoffS    float64
}

// ── Watcher Entry ──

type watcherEntry struct {
	watcher *filewatcher.SessionFileWatcher
	keys    map[string]bool
}

// ── LaskdSessionRegistry ──

// LaskdSessionRegistry manages and monitors all active Claude sessions.
type LaskdSessionRegistry struct {
	mu            sync.Mutex
	sessions      map[string]*sessionEntry // key: work_dir string
	stopCh        chan struct{}
	stopped       bool
	claudeRoot    string
	watchers      map[string]*watcherEntry // key: project dir path
	rootWatcher   *filewatcher.SessionFileWatcher
	pendingLogs   map[string]float64
	logLastCheck  map[string]float64
	checkInterval float64
}

const defaultCheckInterval = 10.0

// NewLaskdSessionRegistry creates a new registry.
func NewLaskdSessionRegistry(claudeRoot string) *LaskdSessionRegistry {
	if claudeRoot == "" {
		claudeRoot = ClaudeProjectsRoot()
	}
	return &LaskdSessionRegistry{
		sessions:      make(map[string]*sessionEntry),
		stopCh:        make(chan struct{}),
		claudeRoot:    claudeRoot,
		watchers:      make(map[string]*watcherEntry),
		pendingLogs:   make(map[string]float64),
		logLastCheck:  make(map[string]float64),
		checkInterval: defaultCheckInterval,
	}
}

// StartMonitor starts the background monitor goroutine and root watcher.
func (r *LaskdSessionRegistry) StartMonitor() {
	r.startRootWatcher()
	go r.monitorLoop()
}

// StopMonitor stops the background monitor and all watchers.
func (r *LaskdSessionRegistry) StopMonitor() {
	r.mu.Lock()
	if !r.stopped {
		r.stopped = true
		close(r.stopCh)
	}
	r.mu.Unlock()

	r.stopRootWatcher()
	r.stopAllWatchers()
}

// GetSession returns the current valid session for the given work directory.
func (r *LaskdSessionRegistry) GetSession(workDir string) *session.ClaudeProjectSession {
	key := workDir
	r.mu.Lock()
	entry, exists := r.sessions[key]

	if exists {
		sessionFile := entry.sessionFile
		if sessionFile == "" {
			sf := sessionutil.FindProjectSessionFile(workDir, ".claude-session")
			if sf == "" {
				sf = filepath.Join(sessionutil.ResolveProjectConfigDir(workDir), ".claude-session")
			}
			sessionFile = sf
		}

		if info, err := os.Stat(sessionFile); err == nil {
			mtime := float64(info.ModTime().UnixMilli()) / 1000.0
			if entry.sessionFile == "" || sessionFile != entry.sessionFile || mtime != entry.fileMtime {
				writeLog(fmt.Sprintf("[INFO] Session file changed, reloading: %s", workDir))
				r.mu.Unlock()
				newEntry := r.loadAndCache(workDir)
				if newEntry != nil && newEntry.valid {
					return newEntry.session
				}
				return nil
			}
		}

		if entry.valid {
			r.mu.Unlock()
			return entry.session
		}
		r.mu.Unlock()
		return nil
	}

	r.mu.Unlock()
	newEntry := r.loadAndCache(workDir)
	if newEntry != nil && newEntry.valid {
		return newEntry.session
	}
	return nil
}

// RegisterSession registers an active session for monitoring.
func (r *LaskdSessionRegistry) RegisterSession(workDir string, sess *session.ClaudeProjectSession) {
	key := workDir
	sessionFile := sess.SessionFile
	var mtime float64
	if info, err := os.Stat(sessionFile); err == nil {
		mtime = float64(info.ModTime().UnixMilli()) / 1000.0
	}

	entry := &sessionEntry{
		workDir:         workDir,
		session:         sess,
		sessionFile:     sessionFile,
		fileMtime:       mtime,
		lastCheck:       float64(time.Now().Unix()),
		valid:           true,
		nextBindRefresh: 0,
		bindBackoffS:    0,
	}

	r.mu.Lock()
	r.sessions[key] = entry
	r.mu.Unlock()

	r.ensureWatchersForWorkDir(workDir, key)
}

func (r *LaskdSessionRegistry) loadAndCache(workDir string) *sessionEntry {
	sess := session.LoadClaudeSession(workDir, "")

	sessionFile := ""
	if sess != nil {
		sessionFile = sess.SessionFile
	}
	if sessionFile == "" {
		sf := sessionutil.FindProjectSessionFile(workDir, ".claude-session")
		if sf == "" {
			sf = filepath.Join(sessionutil.ResolveProjectConfigDir(workDir), ".claude-session")
		}
		sessionFile = sf
	}

	var mtime float64
	if info, err := os.Stat(sessionFile); err == nil {
		mtime = float64(info.ModTime().UnixMilli()) / 1000.0
	}

	valid := false
	if sess != nil {
		result := sess.EnsurePane()
		valid = result.OK
	}

	entry := &sessionEntry{
		workDir:         workDir,
		session:         sess,
		sessionFile:     sessionFile,
		fileMtime:       mtime,
		lastCheck:       float64(time.Now().Unix()),
		valid:           valid,
		nextBindRefresh: 0,
		bindBackoffS:    0,
	}

	r.mu.Lock()
	r.sessions[workDir] = entry
	r.mu.Unlock()

	if valid {
		return entry
	}
	return nil
}

// Invalidate marks a session as invalid.
func (r *LaskdSessionRegistry) Invalidate(workDir string) {
	key := workDir
	r.mu.Lock()
	if entry, ok := r.sessions[key]; ok {
		entry.valid = false
		writeLog(fmt.Sprintf("[INFO] Session invalidated: %s", workDir))
	}
	r.mu.Unlock()
	r.releaseWatchersForWorkDir(workDir, key)
}

// Remove removes a session from the registry.
func (r *LaskdSessionRegistry) Remove(workDir string) {
	key := workDir
	r.mu.Lock()
	if _, ok := r.sessions[key]; ok {
		delete(r.sessions, key)
		writeLog(fmt.Sprintf("[INFO] Session removed: %s", workDir))
	}
	r.mu.Unlock()
	r.releaseWatchersForWorkDir(workDir, key)
}

func (r *LaskdSessionRegistry) monitorLoop() {
	ticker := time.NewTicker(time.Duration(r.checkInterval * float64(time.Second)))
	defer ticker.Stop()

	for {
		select {
		case <-r.stopCh:
			return
		case <-ticker.C:
			r.checkAllSessions()
		}
	}
}

func (r *LaskdSessionRegistry) checkAllSessions() {
	now := float64(time.Now().Unix())
	refreshIntervalS := envFloat("CURDX_LASKD_BIND_REFRESH_INTERVAL", 60.0)
	scanLimit := max(envInt("CURDX_LASKD_BIND_SCAN_LIMIT", 400), 50)
	if scanLimit > 20000 {
		scanLimit = 20000
	}

	r.mu.Lock()
	var snapshot []struct {
		key     string
		workDir string
	}
	for key, entry := range r.sessions {
		if entry.valid {
			snapshot = append(snapshot, struct {
				key     string
				workDir string
			}{key, entry.workDir})
		}
	}
	r.mu.Unlock()

	for _, item := range snapshot {
		func() {
			defer func() { recover() }()
			r.checkOne(item.key, item.workDir, now, refreshIntervalS, scanLimit)
		}()
	}

	// Cleanup stale invalid sessions
	r.mu.Lock()
	var toRemove []string
	var removedWorkDirs []string
	for key, entry := range r.sessions {
		if !entry.valid && now-entry.lastCheck > 300 {
			toRemove = append(toRemove, key)
			removedWorkDirs = append(removedWorkDirs, entry.workDir)
		}
	}
	for _, key := range toRemove {
		delete(r.sessions, key)
	}
	r.mu.Unlock()

	for _, wd := range removedWorkDirs {
		r.releaseWatchersForWorkDir(wd, wd)
	}
}

func (r *LaskdSessionRegistry) checkOne(key, workDir string, now, refreshIntervalS float64, scanLimit int) {
	sf := sessionutil.FindProjectSessionFile(workDir, ".claude-session")
	if sf == "" {
		sf = filepath.Join(sessionutil.ResolveProjectConfigDir(workDir), ".claude-session")
	}
	sessionFile := sf

	info, err := os.Stat(sessionFile)
	if err != nil {
		r.mu.Lock()
		entry := r.sessions[key]
		if entry != nil && entry.valid {
			writeLog(fmt.Sprintf("[WARN] Session file deleted: %s", workDir))
			entry.valid = false
			entry.lastCheck = now
		}
		r.mu.Unlock()
		return
	}
	currentMtime := float64(info.ModTime().UnixMilli()) / 1000.0

	var sess *session.ClaudeProjectSession
	fileChanged := false

	r.mu.Lock()
	entry := r.sessions[key]
	if entry == nil || !entry.valid {
		r.mu.Unlock()
		return
	}
	fileChanged = entry.sessionFile != sessionFile || entry.fileMtime != currentMtime
	if fileChanged || entry.session == nil {
		sess = session.LoadClaudeSession(workDir, "")
		entry.session = sess
		entry.sessionFile = sessionFile
		entry.fileMtime = currentMtime
	} else {
		sess = entry.session
	}
	r.mu.Unlock()

	if sess == nil {
		r.mu.Lock()
		entry2 := r.sessions[key]
		if entry2 != nil && entry2.valid {
			entry2.valid = false
			entry2.lastCheck = now
		}
		r.mu.Unlock()
		return
	}

	result := sess.EnsurePane()
	if !result.OK {
		r.mu.Lock()
		entry2 := r.sessions[key]
		if entry2 != nil && entry2.valid {
			writeLog(fmt.Sprintf("[WARN] Session pane invalid: %s", workDir))
			entry2.valid = false
			entry2.lastCheck = now
		}
		r.mu.Unlock()
		return
	}

	r.mu.Lock()
	entry3 := r.sessions[key]
	if entry3 == nil || !entry3.valid {
		r.mu.Unlock()
		return
	}
	due := now >= entry3.nextBindRefresh
	if !due && !fileChanged {
		entry3.lastCheck = now
		r.mu.Unlock()
		return
	}
	backoff := entry3.bindBackoffS
	if backoff == 0 {
		backoff = refreshIntervalS
	}
	r.mu.Unlock()

	forceScan := fileChanged
	updated := false
	func() {
		defer func() { recover() }()
		updated = refreshClaudeLogBinding(sess, r.claudeRoot, scanLimit, forceScan)
	}()

	r.mu.Lock()
	entry4 := r.sessions[key]
	if entry4 == nil || !entry4.valid {
		r.mu.Unlock()
		return
	}
	if updated {
		entry4.bindBackoffS = refreshIntervalS
	} else {
		newBackoff := backoff * 2.0
		if newBackoff > 600.0 {
			newBackoff = 600.0
		}
		if newBackoff < refreshIntervalS {
			newBackoff = refreshIntervalS
		}
		entry4.bindBackoffS = newBackoff
	}
	entry4.nextBindRefresh = now + entry4.bindBackoffS
	if info, err := os.Stat(sessionFile); err == nil {
		entry4.fileMtime = float64(info.ModTime().UnixMilli()) / 1000.0
	}
	entry4.lastCheck = now
	r.mu.Unlock()
}

// ── Watcher management ──

func (r *LaskdSessionRegistry) projectDirsForWorkDir(workDir string, includeMissing bool) []string {
	var dirs []string
	primary := filepath.Join(r.claudeRoot, projectKeyForPath(workDir))
	if includeMissing {
		dirs = append(dirs, primary)
	} else if _, err := os.Stat(primary); err == nil {
		dirs = append(dirs, primary)
	}

	abs, _ := filepath.Abs(workDir)
	resolved, _ := filepath.EvalSymlinks(workDir)
	for _, wd := range []string{abs, resolved} {
		if wd == "" || wd == workDir {
			continue
		}
		alt := filepath.Join(r.claudeRoot, projectKeyForPath(wd))
		if alt == primary {
			continue
		}
		alreadyIn := slices.Contains(dirs, alt)
		if alreadyIn {
			continue
		}
		if includeMissing {
			dirs = append(dirs, alt)
		} else if _, err := os.Stat(alt); err == nil {
			dirs = append(dirs, alt)
		}
	}
	return dirs
}

func (r *LaskdSessionRegistry) ensureWatchersForWorkDir(workDir, key string) {
	for _, projectDir := range r.projectDirsForWorkDir(workDir, false) {
		projectKey := projectDir
		r.mu.Lock()
		existing := r.watchers[projectKey]
		if existing != nil {
			existing.keys[key] = true
			r.mu.Unlock()
			continue
		}

		// Capture for closure
		pk := projectKey
		w := filewatcher.New(projectDir, func(path string) {
			r.onNewLogFile(pk, path)
		}, false, nil)

		r.watchers[projectKey] = &watcherEntry{
			watcher: w,
			keys:    map[string]bool{key: true},
		}
		r.mu.Unlock()

		if err := w.Start(); err != nil {
			r.mu.Lock()
			delete(r.watchers, projectKey)
			r.mu.Unlock()
		}
	}
}

func (r *LaskdSessionRegistry) releaseWatchersForWorkDir(workDir, key string) {
	for _, projectDir := range r.projectDirsForWorkDir(workDir, true) {
		projectKey := projectDir
		var w *filewatcher.SessionFileWatcher

		r.mu.Lock()
		entry := r.watchers[projectKey]
		if entry == nil {
			r.mu.Unlock()
			continue
		}
		delete(entry.keys, key)
		if len(entry.keys) > 0 {
			r.mu.Unlock()
			continue
		}
		w = entry.watcher
		delete(r.watchers, projectKey)
		r.mu.Unlock()

		if w != nil {
			func() {
				defer func() { recover() }()
				w.Stop()
			}()
		}
	}
}

func (r *LaskdSessionRegistry) stopAllWatchers() {
	r.mu.Lock()
	entries := make([]*watcherEntry, 0, len(r.watchers))
	for _, e := range r.watchers {
		entries = append(entries, e)
	}
	r.watchers = make(map[string]*watcherEntry)
	r.mu.Unlock()

	for _, e := range entries {
		func() {
			defer func() { recover() }()
			e.watcher.Stop()
		}()
	}
}

func (r *LaskdSessionRegistry) startRootWatcher() {
	if r.rootWatcher != nil {
		return
	}
	root := r.claudeRoot
	if _, err := os.Stat(root); err != nil {
		return
	}
	w := filewatcher.New(root, r.onNewLogFileGlobal, true, nil)
	r.rootWatcher = w
	if err := w.Start(); err != nil {
		r.rootWatcher = nil
	}
}

func (r *LaskdSessionRegistry) stopRootWatcher() {
	w := r.rootWatcher
	r.rootWatcher = nil
	if w == nil {
		return
	}
	func() {
		defer func() { recover() }()
		w.Stop()
	}()
}

// ── Log meta reading with retry ──

func (r *LaskdSessionRegistry) readLogMetaWithRetry(logPath string) (cwd string, sid string, isSidechain *bool) {
	for attempt := range 2 {
		c, s, sc := readSessionMeta(logPath)
		if c != "" || s != "" || (sc != nil && *sc) {
			return c, s, sc
		}
		if attempt == 0 {
			time.Sleep(200 * time.Millisecond)
		}
	}
	return "", "", nil
}

func (r *LaskdSessionRegistry) logHasUserMessages(logPath string, scanLines int) bool {
	if scanLines <= 0 {
		scanLines = 80
	}
	f, err := os.Open(logPath)
	if err != nil {
		return false
	}
	defer f.Close()

	buf := make([]byte, 128*1024)
	n, _ := f.Read(buf)
	if n == 0 {
		return false
	}

	lines := strings.SplitN(string(buf[:n]), "\n", scanLines+1)
	for i, line := range lines {
		if i >= scanLines {
			break
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if sc, ok := entry["isSidechain"].(bool); ok && sc {
			return false
		}
		entryType := strings.TrimSpace(strings.ToLower(fmt.Sprintf("%v", entry["type"])))
		if entryType == "user" || entryType == "assistant" {
			return true
		}
		if msg, ok := entry["message"].(map[string]any); ok {
			role := strings.TrimSpace(strings.ToLower(fmt.Sprintf("%v", msg["role"])))
			if role == "user" || role == "assistant" {
				return true
			}
		}
	}
	return false
}

func (r *LaskdSessionRegistry) findClaudeSessionFile(workDir string) string {
	sf := sessionutil.FindProjectSessionFile(workDir, ".claude-session")
	if sf == "" {
		sf = filepath.Join(sessionutil.ResolveProjectConfigDir(workDir), ".claude-session")
	}
	return sf
}

func (r *LaskdSessionRegistry) updateSessionFileDirect(sessionFile, logPath, sessionID string) {
	if _, err := os.Stat(sessionFile); err != nil {
		return
	}
	raw, err := os.ReadFile(sessionFile)
	var payload map[string]any
	if err == nil {
		json.Unmarshal(raw, &payload)
	}
	if payload == nil {
		payload = map[string]any{}
	}

	oldPath, _ := payload["claude_session_path"].(string)
	oldPath = strings.TrimSpace(oldPath)
	oldID, _ := payload["claude_session_id"].(string)
	oldID = strings.TrimSpace(oldID)

	workDirPath := ensureClaudeSessionWorkDirFields(payload, sessionFile)
	newPath := logPath
	newID := strings.TrimSpace(sessionID)

	if oldID != "" && oldID != newID {
		payload["old_claude_session_id"] = oldID
	}
	if oldPath != "" && oldPath != newPath {
		payload["old_claude_session_path"] = oldPath
	}
	if (oldID != "" && oldID != newID) || (oldPath != "" && oldPath != newPath) {
		payload["old_updated_at"] = time.Now().Format("2006-01-02 15:04:05")
	}
	payload["claude_session_path"] = logPath
	payload["claude_session_id"] = sessionID
	payload["updated_at"] = time.Now().Format("2006-01-02 15:04:05")
	if active, ok := payload["active"]; ok && active == false {
		payload["active"] = true
	}

	content, _ := json.MarshalIndent(payload, "", "  ")
	ok, _ := sessionutil.SafeWriteSession(sessionFile, string(content)+"\n")
	if !ok {
		return
	}
	if oldPath != "" && oldPath != newPath && workDirPath != "" {
		maybeAutoExtractOldSession(oldPath, workDirPath)
	}
}

// ── File event handlers ──

func (r *LaskdSessionRegistry) onNewLogFileGlobal(path string) {
	if filepath.Base(path) == "sessions-index.json" {
		r.onSessionsIndex(filepath.Dir(path), path)
		return
	}
	if _, err := os.Stat(path); err != nil {
		return
	}
	cwd, sid, isSidechain := r.readLogMetaWithRetry(path)
	if isSidechain != nil && *isSidechain {
		return
	}
	if cwd == "" {
		return
	}
	sessionID := sid
	if sessionID == "" {
		sessionID = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	if sessionID == "" {
		return
	}

	workDir := cwd
	sessionFile := r.findClaudeSessionFile(workDir)
	if sessionFile != "" {
		r.updateSessionFileDirect(sessionFile, path, sessionID)
	}

	key := workDir
	r.mu.Lock()
	entry := r.sessions[key]
	var sess *session.ClaudeProjectSession
	if entry != nil {
		sess = entry.session
	}
	r.mu.Unlock()

	if sess != nil {
		func() {
			defer func() { recover() }()
			sess.UpdateClaudeBinding(path, sessionID)
		}()
	}
}

func (r *LaskdSessionRegistry) onNewLogFile(projectKey, path string) {
	if filepath.Base(path) == "sessions-index.json" {
		r.onSessionsIndex(projectKey, path)
		return
	}
	if _, err := os.Stat(path); err != nil {
		return
	}

	now := float64(time.Now().Unix())
	pathKey := path

	r.mu.Lock()
	lastCheck := r.logLastCheck[pathKey]
	if now-lastCheck < 0.4 {
		r.mu.Unlock()
		return
	}
	r.logLastCheck[pathKey] = now
	// Prune stale pending
	for k, ts := range r.pendingLogs {
		if now-ts > 120 {
			delete(r.pendingLogs, k)
		}
	}
	r.mu.Unlock()

	cwd, sid, isSidechain := r.readLogMetaWithRetry(path)
	if isSidechain != nil && *isSidechain {
		r.mu.Lock()
		delete(r.pendingLogs, pathKey)
		r.mu.Unlock()
		return
	}
	sessionID := sid
	if sessionID == "" {
		sessionID = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	if sessionID == "" {
		return
	}

	r.mu.Lock()
	we := r.watchers[projectKey]
	if we == nil {
		r.mu.Unlock()
		return
	}
	var keys []string
	for k := range we.keys {
		keys = append(keys, k)
	}
	type entryPair struct {
		key   string
		entry *sessionEntry
	}
	var entries []entryPair
	for _, k := range keys {
		entries = append(entries, entryPair{k, r.sessions[k]})
	}
	r.mu.Unlock()

	if cwd == "" {
		// No cwd info: try to update all sessions in this watcher
		updatedAny := false
		for _, ep := range entries {
			if ep.entry == nil || !ep.entry.valid {
				continue
			}
			sess := ep.entry.session
			if sess == nil {
				sess = session.LoadClaudeSession(ep.entry.workDir, "")
			}
			if sess == nil {
				continue
			}
			currentPath := sess.ClaudeSessionPath()
			if !shouldOverwriteBinding(currentPath, path) && sess.ClaudeSessionID() == sessionID {
				continue
			}
			func() {
				defer func() { recover() }()
				sess.UpdateClaudeBinding(path, sessionID)
				updatedAny = true
			}()
		}
		r.mu.Lock()
		if updatedAny {
			delete(r.pendingLogs, pathKey)
		} else {
			r.pendingLogs[pathKey] = now
		}
		r.mu.Unlock()
		return
	}

	updatedAny := false
	for _, ep := range entries {
		if ep.entry == nil || !ep.entry.valid {
			continue
		}
		if !pathWithin(cwd, ep.entry.workDir) {
			continue
		}
		sess := ep.entry.session
		if sess == nil {
			sess = session.LoadClaudeSession(ep.entry.workDir, "")
		}
		if sess == nil {
			continue
		}
		func() {
			defer func() { recover() }()
			sess.UpdateClaudeBinding(path, sessionID)
			updatedAny = true
		}()
	}
	if updatedAny {
		r.mu.Lock()
		delete(r.pendingLogs, pathKey)
		r.mu.Unlock()
	}
}

func (r *LaskdSessionRegistry) onSessionsIndex(projectKey, indexPath string) {
	if _, err := os.Stat(indexPath); err != nil {
		return
	}

	r.mu.Lock()
	we := r.watchers[projectKey]
	if we == nil {
		r.mu.Unlock()
		return
	}
	var keys []string
	for k := range we.keys {
		keys = append(keys, k)
	}
	type entryPair struct {
		key   string
		entry *sessionEntry
	}
	var entries []entryPair
	for _, k := range keys {
		entries = append(entries, entryPair{k, r.sessions[k]})
	}
	r.mu.Unlock()

	for _, ep := range entries {
		if ep.entry == nil {
			continue
		}
		workDir := ep.entry.workDir
		sessionPath := parseSessionsIndex(workDir, r.claudeRoot)
		if sessionPath == "" {
			continue
		}
		if _, err := os.Stat(sessionPath); err != nil {
			continue
		}
		sessionID := strings.TrimSuffix(filepath.Base(sessionPath), filepath.Ext(sessionPath))
		sessionFile := r.findClaudeSessionFile(workDir)
		if sessionFile != "" {
			r.updateSessionFileDirect(sessionFile, sessionPath, sessionID)
		}
		sess := ep.entry.session
		if sess == nil {
			sess = session.LoadClaudeSession(workDir, "")
		}
		if sess == nil {
			continue
		}
		func() {
			defer func() { recover() }()
			sess.UpdateClaudeBinding(sessionPath, sessionID)
		}()
	}
}

// GetStatus returns the current status of the registry.
func (r *LaskdSessionRegistry) GetStatus() map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()

	total := len(r.sessions)
	valid := 0
	var sessionList []map[string]any
	for _, entry := range r.sessions {
		if entry.valid {
			valid++
		}
		sessionList = append(sessionList, map[string]any{
			"work_dir": entry.workDir,
			"valid":    entry.valid,
		})
	}

	return map[string]any{
		"total":    total,
		"valid":    valid,
		"sessions": sessionList,
	}
}

// ── Singleton ──

var (
	singletonMu        sync.Mutex
	sessionRegistryPtr *LaskdSessionRegistry
)

// GetSessionRegistry returns the singleton session registry, creating it if needed.
func GetSessionRegistry() *LaskdSessionRegistry {
	singletonMu.Lock()
	defer singletonMu.Unlock()
	if sessionRegistryPtr == nil {
		sessionRegistryPtr = NewLaskdSessionRegistry("")
		sessionRegistryPtr.StartMonitor()
	}
	return sessionRegistryPtr
}

// autoloop - AutoFlow autoloop daemon: trigger /tr when state advances.
//
// Monitors state.json for cursor changes and triggers /tr via `ask claude`.
// Optionally clears context first if usage exceeds threshold.
//
// Source: claude_skills/tr/scripts/autoloop.py
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Cursor
// ---------------------------------------------------------------------------

// Cursor represents the current position in the AutoFlow state machine.
type Cursor struct {
	Type      string `json:"type"`
	StepIndex *int   `json:"stepIndex"`
	SubIndex  *int   `json:"subIndex"`
}

func cursorFromState(state map[string]any) Cursor {
	c := Cursor{Type: "none"}
	currentRaw, ok := state["current"]
	if !ok || currentRaw == nil {
		return c
	}
	current, ok := currentRaw.(map[string]any)
	if !ok {
		return c
	}
	if t, ok := current["type"]; ok && t != nil {
		c.Type = fmt.Sprintf("%v", t)
	}
	if v, ok := current["stepIndex"]; ok && v != nil {
		if n, ok := toInt(v); ok {
			c.StepIndex = &n
		}
	}
	if v, ok := current["subIndex"]; ok && v != nil {
		if n, ok := toInt(v); ok {
			c.SubIndex = &n
		}
	}
	return c
}

func cursorFromJSON(raw any) *Cursor {
	m, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	c := Cursor{Type: "none"}
	if t, ok := m["type"]; ok && t != nil {
		c.Type = fmt.Sprintf("%v", t)
	}
	if v, ok := m["stepIndex"]; ok && v != nil {
		if n, ok := toInt(v); ok {
			c.StepIndex = &n
		}
	}
	if v, ok := m["subIndex"]; ok && v != nil {
		if n, ok := toInt(v); ok {
			c.SubIndex = &n
		}
	}
	return &c
}

func (c Cursor) equal(other Cursor) bool {
	if c.Type != other.Type {
		return false
	}
	if !intPtrEqual(c.StepIndex, other.StepIndex) {
		return false
	}
	return intPtrEqual(c.SubIndex, other.SubIndex)
}

func intPtrEqual(a, b *int) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	case int64:
		return int(n), true
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return 0, false
		}
		return int(i), true
	}
	return 0, false
}

// ---------------------------------------------------------------------------
// JSON helpers
// ---------------------------------------------------------------------------

func loadJSON(path string) map[string]any {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil
	}
	return m
}

func atomicWriteJSON(path string, value any) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ---------------------------------------------------------------------------
// Pane ID
// ---------------------------------------------------------------------------

func getPaneID(repo string) string {
	if pane := os.Getenv("CLAUDE_PANE_ID"); pane != "" {
		return pane
	}
	candidates := []string{
		filepath.Join(repo, ".ccb", ".claude-session"),
		filepath.Join(repo, ".ccb_config", ".claude-session"),
		filepath.Join(repo, ".claude-session"),
	}
	for _, path := range candidates {
		session := loadJSON(path)
		if session == nil {
			continue
		}
		if pane, ok := session["pane_id"]; ok && pane != nil {
			s := fmt.Sprintf("%v", pane)
			if s != "" {
				return s
			}
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// lask: send command via ask cli
// ---------------------------------------------------------------------------

func lask(repo, text string) error {
	askPath, err := exec.LookPath("ask")
	if err != nil {
		home, _ := os.UserHomeDir()
		askPath = filepath.Join(home, ".local", "bin", "ask")
	}
	env := os.Environ()
	env = append(env, "CCB_CALLER=autoloop")
	cmd := exec.Command(askPath, "claude", "--no-wrap", "--foreground", text)
	cmd.Dir = repo
	cmd.Env = env
	cmd.Stdout = nil
	cmd.Stderr = nil
	output, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(output))
		if msg == "" {
			msg = "ask claude failed"
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Claude project directory resolution
// ---------------------------------------------------------------------------

func claudeProjectsRoot() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "projects")
}

func candidateProjectDirnames(repo string) []string {
	absRepo, err := filepath.Abs(repo)
	if err != nil {
		absRepo = repo
	}
	// Split path into parts, excluding empty strings and "/"
	parts := strings.Split(absRepo, string(filepath.Separator))
	var cleaned []string
	for _, p := range parts {
		if p != "" {
			cleaned = append(cleaned, p)
		}
	}
	joined := strings.Join(cleaned, "-")
	joinedDash := strings.ReplaceAll(joined, "_", "-")
	return []string{"-" + joined, "-" + joinedDash}
}

func findProjectDir(repo string) string {
	root := claudeProjectsRoot()
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return ""
	}

	for _, name := range candidateProjectDirnames(repo) {
		candidate := filepath.Join(root, name)
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}

	// Fallback: search by repo name hint
	repoName := filepath.Base(repo)
	hints := map[string]bool{
		repoName:                                true,
		strings.ReplaceAll(repoName, "_", "-"): true,
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		return ""
	}

	var best string
	var bestMtime time.Time
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		matched := false
		for h := range hints {
			if strings.Contains(entry.Name(), h) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if best == "" || info.ModTime().After(bestMtime) {
			best = filepath.Join(root, entry.Name())
			bestMtime = info.ModTime()
		}
	}
	return best
}

// ---------------------------------------------------------------------------
// Session JSONL
// ---------------------------------------------------------------------------

func findLatestSessionJSONL(projectDir string) string {
	entries, err := os.ReadDir(projectDir)
	if err != nil {
		return ""
	}
	var best string
	var bestMtime time.Time
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		if strings.HasPrefix(entry.Name(), "agent-") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if best == "" || info.ModTime().After(bestMtime) {
			best = filepath.Join(projectDir, entry.Name())
			bestMtime = info.ModTime()
		}
	}
	return best
}

// extractMessageModelAndUsage extracts (model, usage) from a JSONL record.
func extractMessageModelAndUsage(obj map[string]any) (string, map[string]any) {
	messageRaw, ok := obj["message"]
	if !ok || messageRaw == nil {
		return "", nil
	}
	message, ok := messageRaw.(map[string]any)
	if !ok {
		return "", nil
	}
	var model string
	if m, ok := message["model"].(string); ok {
		model = m
	}
	usage, _ := message["usage"].(map[string]any)
	return model, usage
}

// readLastJSONLWithUsage reads from the end of a JSONL file to find the last
// record that contains message.usage. Returns (model, usage).
func readLastJSONLWithUsage(path string) (string, map[string]any) {
	f, err := os.Open(path)
	if err != nil {
		return "", nil
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return "", nil
	}
	size := info.Size()
	if size == 0 {
		return "", nil
	}

	const blockSize int64 = 64 * 1024
	var buf []byte
	pos := size

	for pos > 0 {
		readSize := blockSize
		if pos < readSize {
			readSize = pos
		}
		pos -= readSize

		chunk := make([]byte, readSize)
		_, err := f.ReadAt(chunk, pos)
		if err != nil && err != io.EOF {
			return "", nil
		}
		buf = append(chunk, buf...)

		lines := splitLines(buf)
		// If we haven't reached the beginning and the buffer doesn't start with
		// a newline, the first "line" may be incomplete -- keep it for next round.
		if pos > 0 && len(buf) > 0 && buf[0] != '\n' && len(lines) > 0 {
			buf = []byte(lines[0])
			lines = lines[1:]
		} else {
			buf = nil
		}

		// Scan lines from the end.
		for i := len(lines) - 1; i >= 0; i-- {
			line := strings.TrimSpace(lines[i])
			if line == "" {
				continue
			}
			var obj map[string]any
			if err := json.Unmarshal([]byte(line), &obj); err != nil {
				continue
			}
			model, usage := extractMessageModelAndUsage(obj)
			if usage != nil {
				return model, usage
			}
		}
	}
	return "", nil
}

func splitLines(data []byte) []string {
	var lines []string
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	scanner.Buffer(make([]byte, 0, len(data)+1), len(data)+1)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines
}

// ---------------------------------------------------------------------------
// models.toml parsing (simple line-based, no external toml library)
// ---------------------------------------------------------------------------

type modelEntry struct {
	pattern      string
	contextLimit int
}

// parseModelsToml does a simple line-based parse of models.toml to extract
// [[models]] entries with pattern and context_limit fields.
func parseModelsToml(path string) []modelEntry {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var entries []modelEntry
	var current *modelEntry
	for _, rawLine := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "[[models]]" {
			if current != nil && current.pattern != "" && current.contextLimit > 0 {
				entries = append(entries, *current)
			}
			current = &modelEntry{}
			continue
		}
		if current == nil {
			continue
		}
		// Parse key = value
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		switch key {
		case "pattern":
			// Strip surrounding quotes
			val = strings.Trim(val, `"'`)
			current.pattern = val
		case "context_limit":
			n := 0
			// Handle underscores in numbers (e.g. 200_000)
			val = strings.ReplaceAll(val, "_", "")
			fmt.Sscanf(val, "%d", &n)
			current.contextLimit = n
		}
	}
	// Don't forget the last entry
	if current != nil && current.pattern != "" && current.contextLimit > 0 {
		entries = append(entries, *current)
	}
	return entries
}

func getContextLimitForModel(model string, defaultLimit int) int {
	if model == "" {
		return defaultLimit
	}

	home, _ := os.UserHomeDir()
	modelsFile := filepath.Join(home, ".claude", "ccline", "models.toml")
	entries := parseModelsToml(modelsFile)
	for _, entry := range entries {
		re, err := regexp.Compile(entry.pattern)
		if err != nil {
			// Fallback to substring match
			if strings.Contains(model, entry.pattern) {
				return entry.contextLimit
			}
			continue
		}
		if re.MatchString(model) {
			return entry.contextLimit
		}
	}

	lowered := strings.ToLower(model)
	switch {
	case strings.Contains(lowered, "opus"):
		return 200_000
	case strings.Contains(lowered, "sonnet"):
		return 200_000
	case strings.Contains(lowered, "haiku"):
		return 200_000
	}
	return defaultLimit
}

// ---------------------------------------------------------------------------
// Usage calculation
// ---------------------------------------------------------------------------

func promptTokensForUsage(usage map[string]any) int {
	if pt, ok := usage["prompt_tokens"]; ok && pt != nil {
		if n, ok := toInt(pt); ok && n > 0 {
			return n
		}
	}
	total := 0
	for _, key := range []string{
		"input_tokens",
		"cache_creation_input_tokens",
		"cache_read_input_tokens",
		"cache_creation_prompt_tokens",
		"cache_read_prompt_tokens",
	} {
		if v, ok := usage[key]; ok && v != nil {
			if n, ok := toInt(v); ok {
				total += n
			}
		}
	}
	if total < 0 {
		return 0
	}
	return total
}

func getContextPercent(repo string, contextLimit int) int {
	projectDir := findProjectDir(repo)
	if projectDir == "" {
		return 100
	}
	sessionFile := findLatestSessionJSONL(projectDir)
	if sessionFile == "" {
		return 100
	}
	model, usage := readLastJSONLWithUsage(sessionFile)
	if usage == nil {
		return 100
	}
	limit := getContextLimitForModel(model, contextLimit)
	if limit <= 0 {
		return 100
	}
	used := promptTokensForUsage(usage)
	percent := int(math.Round(float64(used) / float64(limit) * 100))
	if percent < 0 {
		return 0
	}
	if percent > 100 {
		return 100
	}
	return percent
}

// ---------------------------------------------------------------------------
// Remaining work check
// ---------------------------------------------------------------------------

func hasRemainingWork(state map[string]any) bool {
	cursor := cursorFromState(state)
	if cursor.Type == "none" {
		return false
	}
	stepsRaw, ok := state["steps"]
	if !ok {
		return true
	}
	steps, ok := stepsRaw.([]any)
	if !ok {
		return true
	}
	for _, stepRaw := range steps {
		step, ok := stepRaw.(map[string]any)
		if !ok {
			continue
		}
		status, _ := step["status"].(string)
		if status == "todo" || status == "doing" {
			return true
		}
		subsRaw, ok := step["substeps"]
		if !ok {
			continue
		}
		subs, ok := subsRaw.([]any)
		if !ok {
			continue
		}
		for _, subRaw := range subs {
			sub, ok := subRaw.(map[string]any)
			if !ok {
				continue
			}
			subStatus, _ := sub["status"].(string)
			if subStatus == "todo" || subStatus == "doing" {
				return true
			}
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// File lock (platform-specific, see flock_unix.go / flock_windows.go)
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Trigger
// ---------------------------------------------------------------------------

func trigger(repo string, doClear bool) {
	time.Sleep(5 * time.Second)
	if doClear {
		_ = lask(repo, "/clear")
		time.Sleep(2 * time.Second)
	}
	_ = lask(repo, "/tr")
}

// ---------------------------------------------------------------------------
// Core logic
// ---------------------------------------------------------------------------

type runResult struct {
	code    int
	summary map[string]any
}

func runOnceLocked(
	repo, statePath, stateFile string,
	threshold, contextLimit, cooldownS int,
	triggerOnMissingState bool,
) runResult {
	state := loadJSON(statePath)
	if state == nil {
		return runResult{0, map[string]any{"status": "noop", "reason": "no state.json"}}
	}

	paneID := getPaneID(repo)
	if paneID == "" {
		return runResult{1, map[string]any{"status": "fail", "reason": "no pane_id (.claude-session/CLAUDE_PANE_ID missing)"}}
	}

	cursor := cursorFromState(state)
	saved := loadJSON(stateFile)
	if saved == nil {
		saved = map[string]any{}
	}

	var lastCursor *Cursor
	if lc, ok := saved["last_cursor"]; ok {
		lastCursor = cursorFromJSON(lc)
	}
	lastTS := 0
	if ts, ok := saved["last_trigger_ts"]; ok && ts != nil {
		if n, ok := toInt(ts); ok {
			lastTS = n
		}
	}

	cursorJSON := map[string]any{
		"type":      cursor.Type,
		"stepIndex": cursor.StepIndex,
		"subIndex":  cursor.SubIndex,
	}

	if cursor.Type == "none" {
		_ = atomicWriteJSON(stateFile, map[string]any{
			"last_cursor":    cursorJSON,
			"task_complete":  true,
			"last_trigger_ts": lastTS,
		})
		return runResult{0, map[string]any{"status": "ok", "taskComplete": true, "cursor": cursorJSON}}
	}

	if !hasRemainingWork(state) {
		_ = atomicWriteJSON(stateFile, map[string]any{
			"last_cursor":    cursorJSON,
			"task_complete":  true,
			"last_trigger_ts": lastTS,
		})
		return runResult{0, map[string]any{"status": "ok", "taskComplete": true, "cursor": cursorJSON}}
	}

	now := int(time.Now().Unix())
	if now-lastTS < cooldownS {
		return runResult{0, map[string]any{"status": "noop", "reason": "cooldown", "cursor": cursorJSON}}
	}

	shouldTrigger := false
	if lastCursor == nil {
		shouldTrigger = triggerOnMissingState
	} else if !cursor.equal(*lastCursor) {
		shouldTrigger = true
	}

	if !shouldTrigger {
		_ = atomicWriteJSON(stateFile, map[string]any{
			"last_cursor":    cursorJSON,
			"task_complete":  false,
			"last_trigger_ts": lastTS,
		})
		return runResult{0, map[string]any{"status": "noop", "reason": "cursor unchanged", "cursor": cursorJSON}}
	}

	usage := getContextPercent(repo, contextLimit)
	doClear := usage > threshold
	trigger(repo, doClear)
	_ = atomicWriteJSON(stateFile, map[string]any{
		"last_cursor":    cursorJSON,
		"task_complete":  false,
		"last_trigger_ts": int(time.Now().Unix()),
	})
	return runResult{0, map[string]any{"status": "triggered", "didClear": doClear, "contextPercent": usage, "cursor": cursorJSON}}
}

func runOnce(
	repo, statePath, stateFile, lockPath string,
	threshold, contextLimit, cooldownS int,
	triggerOnMissingState bool,
) int {
	lock, err := acquireLock(lockPath)
	if err != nil {
		printJSON(map[string]any{"status": "noop", "reason": "locked"})
		return 0
	}
	defer lock.release()

	result := runOnceLocked(repo, statePath, stateFile, threshold, contextLimit, cooldownS, triggerOnMissingState)
	printJSON(result.summary)
	return result.code
}

func daemon(
	repo, statePath, stateFile, lockPath string,
	threshold, contextLimit, cooldownS int,
	pollS float64,
) int {
	lock, err := acquireLock(lockPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "autoloop already running")
		return 0
	}
	defer lock.release()

	pollDuration := time.Duration(pollS * float64(time.Second))
	var lastMtime *time.Time

	for {
		info, err := os.Stat(statePath)
		if err != nil {
			time.Sleep(pollDuration)
			continue
		}

		mtime := info.ModTime()

		if lastMtime == nil {
			lastMtime = &mtime

			initial := loadJSON(statePath)
			if initial != nil {
				cursor := cursorFromState(initial)
				cursorJSON := map[string]any{
					"type":      cursor.Type,
					"stepIndex": cursor.StepIndex,
					"subIndex":  cursor.SubIndex,
				}
				_ = atomicWriteJSON(stateFile, map[string]any{
					"last_cursor":    cursorJSON,
					"task_complete":  false,
					"last_trigger_ts": 0,
				})

				// Auto-trigger /tr on first state.json detection if work remains
				if hasRemainingWork(initial) {
					usage := getContextPercent(repo, contextLimit)
					doClear := usage > threshold
					trigger(repo, doClear)
					_ = atomicWriteJSON(stateFile, map[string]any{
						"last_cursor":    cursorJSON,
						"task_complete":  false,
						"last_trigger_ts": int(time.Now().Unix()),
					})
					printJSON(map[string]any{
						"status":         "triggered",
						"reason":         "initial_state",
						"didClear":       doClear,
						"contextPercent": usage,
						"cursor":         cursorJSON,
					})
				}
			}
			time.Sleep(pollDuration)
			continue
		}

		if mtime.Equal(*lastMtime) {
			time.Sleep(pollDuration)
			continue
		}

		lastMtime = &mtime
		result := runOnceLocked(repo, statePath, stateFile, threshold, contextLimit, cooldownS, false)
		printJSON(result.summary)
		time.Sleep(pollDuration)
	}
}

func printJSON(v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	fmt.Println(string(data))
}

// ---------------------------------------------------------------------------
// Repo root resolution
// ---------------------------------------------------------------------------

func resolveRepoRoot(value string) string {
	if value != "" {
		// Expand ~ prefix
		if strings.HasPrefix(value, "~/") {
			home, _ := os.UserHomeDir()
			value = filepath.Join(home, value[2:])
		}
		abs, err := filepath.Abs(value)
		if err != nil {
			return value
		}
		return abs
	}
	cwd, _ := os.Getwd()
	return cwd
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

func main() {
	os.Exit(run())
}

func run() int {
	var (
		repoRoot     string
		once         bool
		threshold    int
		contextLimit int
		cooldown     int
		poll         float64
	)

	flag.StringVar(&repoRoot, "repo-root", "", "AutoFlow project root directory (default: current working directory)")
	flag.BoolVar(&once, "once", false, "Run a single evaluation/trigger (for FileOps run op)")
	flag.IntVar(&threshold, "threshold", 70, "Clear only if computed usage percent > threshold")
	flag.IntVar(&contextLimit, "context-limit", 200000, "Claude context limit for percent calculation")
	flag.IntVar(&cooldown, "cooldown", 20, "Minimum seconds between triggers")
	flag.Float64Var(&poll, "poll", 0.5, "Poll interval seconds (daemon mode)")

	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: autoloop [options]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "AutoFlow autoloop daemon: trigger /tr when state advances")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Options:")
		flag.PrintDefaults()
	}

	flag.Parse()

	repo := resolveRepoRoot(repoRoot)
	statePath := filepath.Join(repo, ".ccb", "state.json")
	stateFile := filepath.Join(repo, ".ccb", "autoloop_state.json")
	lockPath := filepath.Join(repo, ".ccb", "autoloop.lock")

	if once {
		return runOnce(repo, statePath, stateFile, lockPath, threshold, contextLimit, cooldown, true)
	}
	return daemon(repo, statePath, stateFile, lockPath, threshold, contextLimit, cooldown, poll)
}

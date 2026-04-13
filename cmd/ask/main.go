package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/curdx/curdx-bridge/internal/cliutil"
	"github.com/curdx/curdx-bridge/internal/compat"
	"github.com/curdx/curdx-bridge/internal/envutil"
	"github.com/curdx/curdx-bridge/internal/providers"
	"github.com/curdx/curdx-bridge/internal/rpc"
	"github.com/curdx/curdx-bridge/internal/runtime"
	"github.com/curdx/curdx-bridge/internal/sessionutil"
)

// ProviderDaemons maps provider names to their daemon command names.
var ProviderDaemons = map[string]string{
	"gemini":    "gask",
	"codex":     "cask",
	"opencode":  "oask",
	"claude":    "lask",
}

// ProviderDisplayNames maps provider names to their display names.
var ProviderDisplayNames = map[string]string{
	"opencode": "OpenCode",
}

// CallerSessionFiles maps caller names to their session file names.
var CallerSessionFiles = map[string]string{
	"claude":    ".claude-session",
	"codex":     ".codex-session",
	"gemini":    ".gemini-session",
	"opencode":  ".opencode-session",
}

// CallerPaneEnvHints maps callers to env vars that may carry their pane info.
var CallerPaneEnvHints = map[string][2]string{
	"codex":     {"CODEX_TMUX_SESSION", "CODEX_WEZTERM_PANE"},
	"gemini":    {"GEMINI_TMUX_SESSION", "GEMINI_WEZTERM_PANE"},
	"opencode":  {"OPENCODE_TMUX_SESSION", "OPENCODE_WEZTERM_PANE"},
}

// CallerEnvHints maps callers to env vars that indicate the caller is active.
var CallerEnvHints = map[string][2]string{
	"codex":     {"CODEX_SESSION_ID", "CODEX_RUNTIME_DIR"},
	"gemini":    {"GEMINI_SESSION_ID", "GEMINI_RUNTIME_DIR"},
	"opencode":  {"OPENCODE_SESSION_ID", "OPENCODE_RUNTIME_DIR"},
}

var validCallers = map[string]bool{
	"claude": true, "codex": true, "gemini": true, "opencode": true,
	"email": true, "manual": true,
}

func displayName(provider string) string {
	if name, ok := ProviderDisplayNames[provider]; ok {
		return name
	}
	if len(provider) > 0 {
		return strings.ToUpper(provider[:1]) + provider[1:]
	}
	return provider
}

func callerPaneInfo() (paneID, terminalType string) {
	wez := strings.TrimSpace(os.Getenv("WEZTERM_PANE"))
	if wez != "" {
		return wez, "wezterm"
	}
	tmux := strings.TrimSpace(os.Getenv("TMUX_PANE"))
	if tmux != "" {
		return tmux, "tmux"
	}
	return "", ""
}

func envInt(name string, defaultVal int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return defaultVal
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return defaultVal
	}
	return v
}

func cleanupTaskLogs(logDir string) {
	maxFiles := envInt("CURDX_TASK_LOG_MAX_FILES", 100)
	if maxFiles <= 0 {
		return
	}
	pattern := filepath.Join(logDir, "ask-*.log")
	logs, err := filepath.Glob(pattern)
	if err != nil || len(logs) <= maxFiles {
		return
	}
	type logEntry struct {
		path  string
		mtime time.Time
	}
	var entries []logEntry
	for _, p := range logs {
		info, err := os.Stat(p)
		if err != nil {
			entries = append(entries, logEntry{p, time.Time{}})
			continue
		}
		entries = append(entries, logEntry{p, info.ModTime()})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].mtime.After(entries[j].mtime)
	})
	for _, entry := range entries[maxFiles:] {
		prefix := strings.TrimSuffix(filepath.Base(entry.path), ".log")
		for _, ext := range []string{".log", ".sh", ".ps1", ".msg", ".status"} {
			os.Remove(filepath.Join(logDir, prefix+ext))
		}
	}
}

func normalizeCaller(raw string) string {
	caller := strings.ToLower(strings.TrimSpace(raw))
	if validCallers[caller] {
		return caller
	}
	return ""
}

func loadJSONDict(path string) map[string]interface{} {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		return nil
	}
	return m
}

func inferCallerFromPane() string {
	paneID := strings.TrimSpace(os.Getenv("TMUX_PANE"))
	if paneID == "" {
		paneID = strings.TrimSpace(os.Getenv("WEZTERM_PANE"))
	}
	if paneID == "" {
		return ""
	}

	// Fast-path: check pane env hints
	for caller, keys := range CallerPaneEnvHints {
		for _, key := range keys {
			value := strings.TrimSpace(os.Getenv(key))
			if value != "" && value == paneID {
				return caller
			}
		}
	}

	// Fallback: resolve by local session files
	cwd, _ := os.Getwd()
	if cwd == "" {
		cwd = "."
	}
	for caller, sessionFilename := range CallerSessionFiles {
		sessionFile := sessionutil.FindProjectSessionFile(cwd, sessionFilename)
		if sessionFile == "" {
			continue
		}
		data := loadJSONDict(sessionFile)
		if data == nil {
			continue
		}
		sessionPane := ""
		if v, ok := data["pane_id"]; ok {
			sessionPane = strings.TrimSpace(fmt.Sprintf("%v", v))
		}
		if sessionPane == "" {
			if v, ok := data["tmux_session"]; ok {
				sessionPane = strings.TrimSpace(fmt.Sprintf("%v", v))
			}
		}
		if sessionPane != "" && sessionPane == paneID {
			return caller
		}
	}

	return ""
}

func inferCallerFromEnvHints() string {
	var matches []string
	for caller, keys := range CallerEnvHints {
		for _, key := range keys {
			if strings.TrimSpace(os.Getenv(key)) != "" {
				matches = append(matches, caller)
				break
			}
		}
	}
	if len(matches) == 1 {
		return matches[0]
	}
	return ""
}

func detectCaller() string {
	direct := normalizeCaller(os.Getenv("CURDX_CALLER"))
	if direct != "" {
		return direct
	}

	if strings.TrimSpace(os.Getenv("CURDX_EMAIL_REQ_ID")) != "" {
		return "email"
	}

	pane := inferCallerFromPane()
	if pane != "" {
		return pane
	}

	hinted := inferCallerFromEnvHints()
	if hinted != "" {
		return hinted
	}

	return ""
}

func appendTaskStatusLine(statusFile, line string) {
	ts := time.Now().Format("2006-01-02T15:04:05-0700")
	dir := filepath.Dir(statusFile)
	_ = os.MkdirAll(dir, 0o755)
	f, err := os.OpenFile(statusFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s\n", ts, line)
}

func useUnifiedDaemon() bool {
	val := strings.ToLower(strings.TrimSpace(os.Getenv("CURDX_UNIFIED_ASKD")))
	return val != "0" && val != "false" && val != "no" && val != "off"
}

func maybeStartUnifiedDaemon() bool {
	autostart := strings.ToLower(strings.TrimSpace(os.Getenv("CURDX_ASKD_AUTOSTART")))
	if autostart == "0" || autostart == "false" || autostart == "no" || autostart == "off" {
		return false
	}

	stateFile := runtime.StateFilePath("askd.json")
	if rpc.PingDaemon("ask", 0.5, stateFile) {
		return true
	}

	// Find askd binary
	selfPath, _ := os.Executable()
	var candidates []string
	if selfPath != "" {
		selfDir := filepath.Dir(selfPath)
		local := filepath.Join(selfDir, "askd")
		if _, err := os.Stat(local); err == nil {
			candidates = append(candidates, local)
		}
	}
	if found, err := exec.LookPath("askd"); err == nil {
		candidates = append(candidates, found)
	}
	if len(candidates) == 0 {
		return false
	}

	entry := candidates[0]
	cmd := exec.Command(entry)
	var filteredEnv []string
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "CURDX_PARENT_PID=") && !strings.HasPrefix(e, "CURDX_MANAGED=") {
			filteredEnv = append(filteredEnv, e)
		}
	}
	cmd.Env = filteredEnv
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	setSysProcAttr(cmd)
	if err := cmd.Start(); err != nil {
		return false
	}
	go func() { _ = cmd.Wait() }()

	// Wait for daemon to be ready
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if rpc.PingDaemon("ask", 0.2, stateFile) {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

func makeTaskID() string {
	now := time.Now()
	ms := now.Nanosecond() / 1000000
	return fmt.Sprintf("%s-%03d-%d", now.Format("20060102-150405"), ms, os.Getpid())
}

func sendViaUnifiedDaemon(provider, message string, timeout float64, noWrap bool, caller string) int {
	stateFile := runtime.StateFilePath("askd.json")
	state := rpc.ReadState(stateFile)
	if state == nil {
		if maybeStartUnifiedDaemon() {
			state = rpc.ReadState(stateFile)
		}
		if state == nil {
			fmt.Fprintln(os.Stderr, "[ERROR] Unified askd daemon not running")
			return cliutil.ExitError
		}
	}

	host := getStringField(state, "connect_host")
	if host == "" {
		host = getStringField(state, "host")
	}
	if host == "" {
		host = "127.0.0.1"
	}
	port := getIntField(state, "port")
	token := getStringField(state, "token")

	rawWorkDir := getStringField(state, "work_dir")
	daemonWorkDir := strings.TrimSpace(rawWorkDir)
	if daemonWorkDir != "" {
		if info, err := os.Stat(daemonWorkDir); err != nil || !info.IsDir() {
			fmt.Fprintf(os.Stderr, "[WARN] daemon work_dir not found: %s, falling back to cwd\n", daemonWorkDir)
			daemonWorkDir = ""
		}
	}
	if daemonWorkDir == "" {
		daemonWorkDir, _ = os.Getwd()
	}

	if port == 0 {
		fmt.Fprintln(os.Stderr, "[ERROR] Invalid askd state")
		return cliutil.ExitError
	}

	callerPaneID, callerTerminal := callerPaneInfo()

	req := map[string]interface{}{
		"type":             "ask.request",
		"v":               1,
		"id":              makeTaskID(),
		"token":           token,
		"provider":        provider,
		"work_dir":        daemonWorkDir,
		"timeout_s":       timeout,
		"message":         message,
		"no_wrap":         noWrap,
		"caller":          caller,
		"caller_pane_id":  callerPaneID,
		"caller_terminal": callerTerminal,
	}

	if caller == "email" {
		req["email_req_id"] = os.Getenv("CURDX_EMAIL_REQ_ID")
		req["email_msg_id"] = os.Getenv("CURDX_EMAIL_MSG_ID")
		req["email_from"] = os.Getenv("CURDX_EMAIL_FROM")
	}

	// Try with one retry on connection failure
	requestSent := false
	for attempt := 0; attempt < 2; attempt++ {
		var connTimeout time.Duration
		if timeout > 0 {
			connTimeout = time.Duration(timeout+10) * time.Second
		} else {
			connTimeout = 3610 * time.Second
		}
		conn, err := net.DialTimeout("tcp",
			net.JoinHostPort(host, strconv.Itoa(port)), connTimeout)
		if err != nil {
			if attempt == 0 && !requestSent {
				if maybeStartUnifiedDaemon() {
					state = rpc.ReadState(stateFile)
					if state != nil {
						host = getStringField(state, "connect_host")
						if host == "" {
							host = getStringField(state, "host")
						}
						if host == "" {
							host = "127.0.0.1"
						}
						port = getIntField(state, "port")
						token = getStringField(state, "token")
						req["token"] = token
						continue
					}
				}
			}
			fmt.Fprintf(os.Stderr, "[ERROR] %v\n", err)
			return cliutil.ExitError
		}

		requestSent = true
		reqBytes, _ := json.Marshal(req)
		reqBytes = append(reqBytes, '\n')

		conn.SetDeadline(time.Now().Add(connTimeout))
		_, err = conn.Write(reqBytes)
		if err != nil {
			conn.Close()
			fmt.Fprintf(os.Stderr, "[ERROR] %v\n", err)
			return cliutil.ExitError
		}

		var data []byte
		buf := make([]byte, 4096)
		for {
			n, readErr := conn.Read(buf)
			if n > 0 {
				data = append(data, buf[:n]...)
			}
			if readErr != nil || strings.Contains(string(data), "\n") {
				break
			}
		}
		conn.Close()

		var resp map[string]interface{}
		if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &resp); err != nil {
			fmt.Fprintf(os.Stderr, "[ERROR] %v\n", err)
			return cliutil.ExitError
		}

		exitCode := getIntField(resp, "exit_code")
		reply := getStringField(resp, "reply")
		if reply != "" {
			fmt.Println(reply)
		}
		return exitCode
	}

	return cliutil.ExitError
}

func getStringField(m map[string]interface{}, key string) string {
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	s, ok := v.(string)
	if ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

func getIntField(m map[string]interface{}, key string) int {
	v, ok := m[key]
	if !ok || v == nil {
		return 0
	}
	switch val := v.(type) {
	case float64:
		return int(val)
	case int:
		return val
	case string:
		i, _ := strconv.Atoi(val)
		return i
	}
	return 0
}

func defaultForeground() bool {
	if envutil.EnvBool("CURDX_ASK_BACKGROUND", false) {
		return false
	}
	if envutil.EnvBool("CURDX_ASK_FOREGROUND", false) {
		return true
	}
	caller := detectCaller()
	if caller == "claude" {
		return false
	}
	return true
}

func shouldEmitAsyncGuardrail(caller string) bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv("CURDX_ASK_EMIT_GUARDRAIL")))
	if raw != "" {
		return raw != "0" && raw != "false" && raw != "no" && raw != "off"
	}
	return strings.ToLower(strings.TrimSpace(caller)) == "claude"
}

func requireCaller() string {
	caller := detectCaller()
	if caller != "" {
		return caller
	}
	defaultCaller := normalizeCaller(os.Getenv("CURDX_CALLER_DEFAULT"))
	if defaultCaller == "" {
		defaultCaller = "manual"
	}
	fmt.Fprintf(os.Stderr,
		"[WARN] CURDX_CALLER not set; using '%s'. Set CURDX_CALLER explicitly to override.\n",
		defaultCaller)
	return defaultCaller
}

func usage() {
	fmt.Fprintln(os.Stderr, "Usage: ask <provider> [options] <message>")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Providers:")
	fmt.Fprintln(os.Stderr, "  gemini, codex, opencode, claude")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Options:")
	fmt.Fprintln(os.Stderr, "  -h, --help              Show this help message")
	fmt.Fprintln(os.Stderr, "  -t, --timeout SECONDS   Request timeout (default: 3600)")
	fmt.Fprintln(os.Stderr, "  --notify                Sync send, no wait for reply (for notifications)")
	fmt.Fprintln(os.Stderr, "  --foreground            Run in foreground (no nohup/background)")
	fmt.Fprintln(os.Stderr, "  --background            Force background mode")
	fmt.Fprintln(os.Stderr, "  --no-wrap               Don't wrap with CURDX protocol markers")
}

func main() {
	os.Exit(run(os.Args))
}

func run(argv []string) int {
	if len(argv) <= 1 {
		usage()
		return cliutil.ExitError
	}

	rawProvider := strings.ToLower(argv[1])
	if rawProvider == "-h" || rawProvider == "--help" {
		usage()
		return cliutil.ExitOK
	}

	baseProvider, _ := providers.ParseQualifiedProvider(rawProvider)
	if _, ok := ProviderDaemons[baseProvider]; !ok {
		fmt.Fprintf(os.Stderr, "[ERROR] Unknown provider: %s\n", baseProvider)
		providerNames := make([]string, 0, len(ProviderDaemons))
		for k := range ProviderDaemons {
			providerNames = append(providerNames, k)
		}
		sort.Strings(providerNames)
		fmt.Fprintf(os.Stderr, "[ERROR] Available: %s\n", strings.Join(providerNames, ", "))
		return cliutil.ExitError
	}

	daemonCmd := ProviderDaemons[baseProvider]
	provider := rawProvider // keep full qualified key

	// Parse remaining arguments
	timeout := 3600.0
	notifyMode := false
	noWrap := false
	foregroundMode := defaultForeground()
	var parts []string

	args := argv[2:]
	for i := 0; i < len(args); i++ {
		token := args[i]
		if token == "-h" || token == "--help" {
			usage()
			return cliutil.ExitOK
		}
		if token == "-t" || token == "--timeout" {
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "[ERROR] --timeout requires a number")
				return cliutil.ExitError
			}
			v, err := strconv.ParseFloat(args[i], 64)
			if err != nil {
				fmt.Fprintln(os.Stderr, "[ERROR] --timeout must be a number")
				return cliutil.ExitError
			}
			timeout = v
			continue
		}
		if token == "--notify" {
			notifyMode = true
			continue
		}
		if token == "--foreground" {
			foregroundMode = true
			continue
		}
		if token == "--background" {
			foregroundMode = false
			continue
		}
		if token == "--no-wrap" {
			noWrap = true
			continue
		}
		parts = append(parts, token)
	}

	message := strings.TrimSpace(strings.Join(parts, " "))
	if message == "" {
		fi, _ := os.Stdin.Stat()
		if fi != nil && (fi.Mode()&os.ModeCharDevice) == 0 {
			message = strings.TrimSpace(compat.ReadStdinText())
		}
	}
	if message == "" {
		fmt.Fprintln(os.Stderr, "[ERROR] Message cannot be empty")
		return cliutil.ExitError
	}

	// Notify mode: sync send, no wait for reply
	if notifyMode {
		requireCaller()
		if useUnifiedDaemon() {
			fmt.Fprintln(os.Stderr, "[WARN] Notify mode not yet supported with unified daemon, using legacy")
		}
		cmdArgs := []string{"--sync"}
		if noWrap {
			cmdArgs = append(cmdArgs, "--no-wrap")
		}
		cmd := exec.Command(daemonCmd, cmdArgs...)
		cmd.Stdin = strings.NewReader(message)
		cmd.Stdout = nil
		cmd.Stderr = nil
		_ = cmd.Run()
		if cmd.ProcessState != nil {
			return cmd.ProcessState.ExitCode()
		}
		return cliutil.ExitOK
	}

	// Foreground mode
	if foregroundMode {
		if useUnifiedDaemon() {
			caller := requireCaller()
			return sendViaUnifiedDaemon(provider, message, timeout, noWrap, caller)
		}
		cmdArgs := []string{"--sync", "--timeout", fmt.Sprintf("%g", timeout)}
		if noWrap && provider == "claude" {
			cmdArgs = append(cmdArgs, "--no-wrap")
		}
		cmd := exec.Command(daemonCmd, cmdArgs...)
		cmd.Stdin = strings.NewReader(message)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		env := os.Environ()
		env = append(env, "CURDX_CALLER="+requireCaller())
		cmd.Env = env
		if err := cmd.Run(); err != nil {
			if cmd.ProcessState != nil {
				return cmd.ProcessState.ExitCode()
			}
			fmt.Fprintf(os.Stderr, "[ERROR] %v\n", err)
			return cliutil.ExitError
		}
		return cliutil.ExitOK
	}

	// Default async mode: background task
	if useUnifiedDaemon() {
		stateFile := runtime.StateFilePath("askd.json")
		if !rpc.PingDaemon("ask", 0.5, stateFile) && !maybeStartUnifiedDaemon() {
			fmt.Fprintln(os.Stderr, "[ERROR] Unified askd daemon not running")
			return cliutil.ExitError
		}
	}

	taskID := makeTaskID()
	logDir := filepath.Join(os.TempDir(), "curdx-tasks")
	_ = os.MkdirAll(logDir, 0o700)
	logFile := filepath.Join(logDir, fmt.Sprintf("ask-%s-%s.log", provider, taskID))
	statusFile := filepath.Join(logDir, fmt.Sprintf("ask-%s-%s.status", provider, taskID))

	touchFile(logFile)
	touchFile(statusFile)
	cleanupTaskLogs(logDir)

	caller := requireCaller()
	cwd := mustGetwd()
	appendTaskStatusLine(statusFile,
		fmt.Sprintf("submitted task=%s provider=%s caller=%s work_dir=%s",
			taskID, provider, caller, cwd))

	askCmd, _ := os.Executable()

	// Build background env lines
	bgPaneID, bgTerminal := callerPaneInfo()
	paneEnvLines := ""
	if bgPaneID != "" {
		paneEnvLines += fmt.Sprintf("export CURDX_CALLER_PANE_ID=%s\n", shellQuote(bgPaneID))
	}
	if bgTerminal != "" {
		paneEnvLines += fmt.Sprintf("export CURDX_CALLER_TERMINAL=%s\n", shellQuote(bgTerminal))
	}

	emailEnvLines := ""
	if caller == "email" {
		for _, key := range []string{"CURDX_EMAIL_REQ_ID", "CURDX_EMAIL_MSG_ID", "CURDX_EMAIL_FROM"} {
			val := os.Getenv(key)
			if val != "" {
				emailEnvLines += fmt.Sprintf("export %s=%s\n", key, shellQuote(val))
			}
		}
	}

	curdxRunDir := os.Getenv("CURDX_RUN_DIR")
	runDirLine := ""
	if curdxRunDir != "" {
		runDirLine = fmt.Sprintf("export CURDX_RUN_DIR=%s\n", shellQuote(curdxRunDir))
	}

	// Generate a random heredoc delimiter to prevent injection when
	// the user message contains a line matching the delimiter.
	delimRand := make([]byte, 16)
	_, _ = rand.Read(delimRand)
	heredocDelim := "ASKEOF_" + hex.EncodeToString(delimRand)

	bgScript := fmt.Sprintf(`#!/bin/sh
set +e
_now() {
  date '+%%Y-%%m-%%dT%%H:%%M:%%S%%z'
}
echo "$(_now) running pid=$$" >> %s
echo "[CURDX_TASK_START] task=%s provider=%s caller=%s pid=$$"
export CURDX_REQ_ID=%s
export CURDX_CALLER=%s
export CURDX_WORK_DIR=%s
%s%s%s%s %s --foreground --timeout %g <<'`+heredocDelim+`'
%s
`+heredocDelim+`
rc=$?
echo "[CURDX_TASK_END] task=%s provider=%s exit_code=$rc"
echo "$(_now) finished exit_code=$rc" >> %s
if [ "$rc" -ne 0 ]; then
  echo "$(_now) failed exit_code=$rc" >> %s
fi
exit "$rc"
`,
		shellQuote(statusFile),
		taskID, provider, caller,
		shellQuote(taskID), shellQuote(caller), shellQuote(cwd),
		runDirLine, emailEnvLines, paneEnvLines,
		shellQuote(askCmd), shellQuote(provider), timeout,
		message,
		taskID, provider,
		shellQuote(statusFile),
		shellQuote(statusFile),
	)

	scriptFile := filepath.Join(logDir, fmt.Sprintf("ask-%s-%s.sh", provider, taskID))
	_ = os.WriteFile(scriptFile, []byte(bgScript), 0o700)

	// Run detached
	logHandle, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] Failed to open log file: %v\n", err)
		return cliutil.ExitError
	}

	proc := exec.Command("sh", scriptFile)
	proc.Stdin = nil
	proc.Stdout = logHandle
	proc.Stderr = logHandle
	setSysProcAttr(proc)
	err = proc.Start()
	logHandle.Close()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] Failed to start background task: %v\n", err)
		return cliutil.ExitError
	}

	bgPid := proc.Process.Pid
	go func() { _ = proc.Wait() }()

	appendTaskStatusLine(statusFile, fmt.Sprintf("spawned pid=%d", bgPid))

	fmt.Printf("[CURDX_ASYNC_SUBMITTED provider=%s]\n", provider)
	fmt.Printf("%s processing (task: %s)\n", displayName(provider), taskID)
	fmt.Printf("[CURDX_ASYNC_PID task=%s pid=%d]\n", taskID, bgPid)
	fmt.Printf("[CURDX_ASYNC_STATUS_FILE task=%s] %s\n", taskID, statusFile)
	fmt.Printf("[CURDX_ASYNC_LOG_FILE task=%s] %s\n", taskID, logFile)
	if shouldEmitAsyncGuardrail(caller) {
		fmt.Printf("MANDATORY: END YOUR TURN NOW. Reply ONLY '%s processing...', then stop. See 'Async Guardrail' in CLAUDE.md.\n",
			displayName(provider))
	}

	return cliutil.ExitOK
}

func touchFile(path string) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err == nil {
		f.Close()
	}
}

func mustGetwd() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return cwd
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

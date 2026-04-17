package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/curdx/curdx-bridge/internal/compat"
	"github.com/curdx/curdx-bridge/internal/completionhook"
	"github.com/curdx/curdx-bridge/internal/envutil"
	"github.com/curdx/curdx-bridge/internal/sessionutil"
)

// defaultTerminalKind picks the assumed terminal backend when a session file
// doesn't carry one. Windows native has no tmux, so WezTerm is the sane
// default there.
func defaultTerminalKind() string {
	if runtime.GOOS == "windows" {
		return "wezterm"
	}
	return "tmux"
}

func envFloat(name string, defaultVal float64) float64 {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return defaultVal
	}
	var v float64
	_, err := fmt.Sscanf(raw, "%f", &v)
	if err != nil {
		return defaultVal
	}
	return v
}

func debugEnabled() bool {
	return envutil.EnvBool("CURDX_COMPLETION_HOOK_DEBUG", true)
}

func debugLogPath() string {
	raw := strings.TrimSpace(os.Getenv("CURDX_COMPLETION_HOOK_DEBUG_LOG"))
	if raw != "" {
		if strings.HasPrefix(raw, "~/") {
			home, _ := os.UserHomeDir()
			if home != "" {
				return filepath.Join(home, raw[2:])
			}
		}
		return raw
	}
	return filepath.Join(os.TempDir(), "curdx-completion-hook.debug.log")
}

func debugLog(message string) {
	if !debugEnabled() {
		return
	}
	path := debugLogPath()
	dir := filepath.Dir(path)
	_ = os.MkdirAll(dir, 0o755)
	ts := time.Now().Format("2006-01-02T15:04:05.000000-0700")
	line := fmt.Sprintf("%s pid=%d %s\n", ts, os.Getpid(), message)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err == nil {
		f.WriteString(line)
		f.Close()
	}
	fmt.Fprintf(os.Stderr, "[completion-hook-debug] %s\n", message)
}

func loadSessionFile(sessionPath string) map[string]interface{} {
	data, err := os.ReadFile(sessionPath)
	if err != nil {
		return nil
	}
	// Handle BOM (utf-8-sig)
	if len(data) >= 3 && data[0] == 0xef && data[1] == 0xbb && data[2] == 0xbf {
		data = data[3:]
	}
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		return nil
	}
	return m
}

func normalizePathForMatch(value string) string {
	s := strings.TrimSpace(value)
	if s == "" {
		return ""
	}
	abs, err := filepath.Abs(s)
	if err != nil {
		abs = s
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		resolved = abs
	}
	normalized := strings.ReplaceAll(resolved, "\\", "/")
	normalized = strings.TrimRight(normalized, "/")
	return normalized
}

func pathIsSameOrParent(parent, child string) bool {
	p := normalizePathForMatch(parent)
	c := normalizePathForMatch(child)
	if p == "" || c == "" {
		return false
	}
	if p == c {
		return true
	}
	if !strings.HasPrefix(c, p) {
		return false
	}
	return strings.HasPrefix(c[len(p):], "/")
}

func workDirsCompatible(sessionWorkDir, requestWorkDir string) bool {
	if sessionWorkDir == "" || requestWorkDir == "" {
		return true
	}
	return pathIsSameOrParent(sessionWorkDir, requestWorkDir)
}

func sendViaTerminal(paneID, message, terminal string, sessionData map[string]interface{}) bool {
	if terminal == "wezterm" {
		return sendViaWezterm(paneID, message, sessionData)
	}
	return sendViaTmux(paneID, message)
}

func findWeztermCLI() string {
	// Check environment variable first
	wezBin := strings.TrimSpace(os.Getenv("CURDX_WEZTERM_BIN"))
	if wezBin != "" {
		if _, err := os.Stat(wezBin); err == nil {
			return wezBin
		}
	}
	if found, err := exec.LookPath("wezterm"); err == nil {
		return found
	}
	return ""
}

func envTrue(name string) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	return v == "1" || v == "true" || v == "yes" || v == "on"
}

func weztermCLIBaseArgs(weztermBin string) []string {
	args := []string{weztermBin, "cli"}
	weztermClass := strings.TrimSpace(os.Getenv("CODEX_WEZTERM_CLASS"))
	if weztermClass == "" {
		weztermClass = strings.TrimSpace(os.Getenv("WEZTERM_CLASS"))
	}
	if weztermClass != "" {
		args = append(args, "--class", weztermClass)
	}
	if envTrue("CODEX_WEZTERM_PREFER_MUX") {
		args = append(args, "--prefer-mux")
	}
	if envTrue("CODEX_WEZTERM_NO_AUTO_START") {
		args = append(args, "--no-auto-start")
	}
	return args
}

func sendWeztermEnter(baseArgs []string, paneID string) bool {
	variants := []string{"Enter", "Return", "enter"}
	for _, variant := range variants {
		cmd := exec.Command(baseArgs[0], append(baseArgs[1:], "send-key", "--pane-id", paneID, "--key", variant)...)
		if err := cmd.Run(); err == nil {
			return true
		}
		cmd = exec.Command(baseArgs[0], append(baseArgs[1:], "send-key", "--pane-id", paneID, variant)...)
		if err := cmd.Run(); err == nil {
			return true
		}
	}
	return false
}

func sendViaWezterm(paneID, message string, sessionData map[string]interface{}) bool {
	wezterm := findWeztermCLI()
	if wezterm == "" {
		return false
	}

	baseArgs := weztermCLIBaseArgs(wezterm)
	payload := strings.ReplaceAll(message, "\r", "")
	payload = strings.TrimRight(payload, "\n")
	if payload == "" {
		return false
	}

	// Preferred path: paste full content, then send Enter
	sendCmd := exec.Command(baseArgs[0], append(baseArgs[1:], "send-text", "--pane-id", paneID)...)
	sendCmd.Stdin = strings.NewReader(payload)
	if err := sendCmd.Run(); err == nil {
		debugLog(fmt.Sprintf("wezterm send-text pane=%q rc=0", paneID))
		time.Sleep(100 * time.Millisecond)
		if sendWeztermEnter(baseArgs, paneID) {
			debugLog(fmt.Sprintf("wezterm submit via send-key pane=%q rc=0", paneID))
			return true
		}
		// Fallback: send CR byte
		submitCmd := exec.Command(baseArgs[0], append(baseArgs[1:], "send-text", "--pane-id", paneID, "--no-paste")...)
		submitCmd.Stdin = strings.NewReader("\r")
		if err := submitCmd.Run(); err == nil {
			debugLog(fmt.Sprintf("wezterm submit via no-paste pane=%q rc=0", paneID))
			return true
		}
	}

	// Fallback: no-paste with trailing CR
	time.Sleep(100 * time.Millisecond)
	fallbackCmd := exec.Command(baseArgs[0], append(baseArgs[1:], "send-text", "--pane-id", paneID, "--no-paste")...)
	fallbackCmd.Stdin = strings.NewReader(payload + "\r")
	if err := fallbackCmd.Run(); err == nil {
		debugLog(fmt.Sprintf("wezterm fallback no-paste pane=%q rc=0", paneID))
		return true
	}
	debugLog(fmt.Sprintf("wezterm exception pane=%q", paneID))
	return false
}

func sendViaTmux(paneID, message string) bool {
	sendEnter := func() bool {
		keyVariants := []string{"Enter", "Return", "C-m"}
		maxRetries := 3
		enterDelay := envFloat("CURDX_TMUX_ENTER_DELAY", 0.5)
		retryDelay := envFloat("CURDX_TMUX_ENTER_RETRY_DELAY", 0.08)
		for attempt := 0; attempt < maxRetries; attempt++ {
			if enterDelay > 0 {
				time.Sleep(time.Duration(enterDelay * float64(time.Second)))
			}
			for _, key := range keyVariants {
				cmd := exec.Command("tmux", "send-keys", "-t", paneID, key)
				if err := cmd.Run(); err == nil {
					debugLog(fmt.Sprintf("tmux send-keys pane=%q key=%q rc=0", paneID, key))
					return true
				}
			}
			if retryDelay > 0 && attempt < maxRetries-1 {
				time.Sleep(time.Duration(retryDelay * float64(time.Second)))
			}
		}
		return false
	}

	// Ensure pane is not in copy mode
	modeCmd := exec.Command("tmux", "display-message", "-p", "-t", paneID, "#{pane_in_mode}")
	out, err := modeCmd.Output()
	if err == nil {
		modeStr := strings.TrimSpace(string(out))
		if modeStr == "1" || modeStr == "on" || modeStr == "yes" {
			exec.Command("tmux", "send-keys", "-t", paneID, "-X", "cancel").Run()
		}
	}

	bufferName := fmt.Sprintf("curdx-hook-%d-%d", os.Getpid(), time.Now().UnixMilli())

	// Load message into tmux buffer
	loadCmd := exec.Command("tmux", "load-buffer", "-b", bufferName, "-")
	loadCmd.Stdin = strings.NewReader(message)
	if err := loadCmd.Run(); err != nil {
		debugLog(fmt.Sprintf("tmux load-buffer pane=%q failed", paneID))
		return false
	}

	defer func() {
		exec.Command("tmux", "delete-buffer", "-b", bufferName).Run()
	}()

	// Paste buffer to pane
	pasteCmd := exec.Command("tmux", "paste-buffer", "-p", "-t", paneID, "-b", bufferName)
	if err := pasteCmd.Run(); err != nil {
		debugLog(fmt.Sprintf("tmux paste-buffer pane=%q failed", paneID))
		return false
	}

	debugLog(fmt.Sprintf("tmux paste-buffer pane=%q rc=0", paneID))
	return sendEnter()
}

func findAskCommand() string {
	selfPath, _ := os.Executable()
	var candidates []string
	if selfPath != "" {
		selfDir := filepath.Dir(selfPath)
		candidates = append(candidates, filepath.Join(selfDir, "cxb-ask"))
	}
	home, _ := os.UserHomeDir()
	if home != "" {
		candidates = append(candidates,
			filepath.Join(home, ".local", "share", "codex-dual", "bin", "cxb-ask"),
			filepath.Join(home, ".local", "bin", "cxb-ask"),
		)
	}
	for _, p := range candidates {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p
		}
	}
	return ""
}

func renderTerminalMessage(providerDisplay, reqID, replyContent string, outputFile string, status string) string {
	marker := completionhook.CompletionStatusMarker(status, true)
	statusLabel := completionhook.CompletionStatusLabel(status, true)
	if outputFile != "" {
		return fmt.Sprintf("CURDX_REQ_ID: %s\n\n%s\nProvider: %s\nStatus: %s\nOutput file: %s\n\nResult: %s\n",
			reqID, marker, providerDisplay, statusLabel, outputFile, replyContent)
	}
	return fmt.Sprintf("CURDX_REQ_ID: %s\n\n%s\nProvider: %s\nStatus: %s\n\nResult: %s\n",
		reqID, marker, providerDisplay, statusLabel, replyContent)
}

func parseArgs(argv []string) (provider, caller, outputFile, reply, reqID string) {
	provider = ""
	caller = "claude"
	outputFile = ""
	reply = ""
	reqID = ""

	args := argv[1:]
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--provider":
			if i+1 < len(args) {
				i++
				provider = args[i]
			}
		case "--caller":
			if i+1 < len(args) {
				i++
				caller = args[i]
			}
		case "--output":
			if i+1 < len(args) {
				i++
				outputFile = args[i]
			}
		case "--reply":
			if i+1 < len(args) {
				i++
				reply = args[i]
			}
		case "--req-id":
			if i+1 < len(args) {
				i++
				reqID = args[i]
			}
		}
	}
	return
}

func main() {
	os.Exit(run(os.Args))
}

func run(argv []string) int {
	if !envutil.EnvBool("CURDX_COMPLETION_HOOK_ENABLED", true) {
		return 0
	}

	provider, argCaller, outputFile, argReply, reqID := parseArgs(argv)
	provider = strings.ToLower(provider)

	// CURDX_CALLER env takes priority over --caller arg
	caller := strings.ToLower(strings.TrimSpace(os.Getenv("CURDX_CALLER")))
	if caller == "" {
		caller = strings.ToLower(argCaller)
	}

	// Read reply from stdin
	replyContent := ""
	fi, _ := os.Stdin.Stat()
	if fi != nil && (fi.Mode()&os.ModeCharDevice) == 0 {
		replyContent = compat.ReadStdinText()
	}
	if replyContent == "" {
		replyContent = argReply
	}

	doneSeen := envutil.EnvBool("CURDX_DONE_SEEN", true)
	status := completionhook.NormalizeCompletionStatus(os.Getenv("CURDX_COMPLETION_STATUS"), doneSeen)

	debugLog(fmt.Sprintf("start provider=%s caller=%s req_id=%s status=%s done_seen=%v",
		provider, caller, orDefault(reqID, "unknown"), status, doneSeen))

	if replyContent == "" {
		replyContent = completionhook.DefaultReplyForStatus(status, doneSeen)
	}

	// Handle email caller
	if caller == "email" {
		// Email reply sending not yet ported to Go
		fmt.Fprintln(os.Stderr, "[completion-hook] Email reply sending not yet implemented in Go")
		return 1
	}

	if caller == "manual" {
		return 0
	}

	// Terminal caller - construct notification message
	providerNames := map[string]string{
		"codex":    "Codex",
		"gemini":   "Gemini",
		"opencode": "OpenCode",
	}
	providerDisplay := providerNames[provider]
	if providerDisplay == "" {
		if len(provider) > 0 {
			providerDisplay = strings.ToUpper(provider[:1]) + provider[1:]
		} else {
			providerDisplay = provider
		}
	}

	if reqID == "" {
		reqID = "unknown"
	}

	message := renderTerminalMessage(providerDisplay, reqID, replyContent, outputFile, status)

	// Direct pane routing
	directPaneID := strings.TrimSpace(os.Getenv("CURDX_CALLER_PANE_ID"))
	directTerminal := strings.TrimSpace(os.Getenv("CURDX_CALLER_TERMINAL"))

	var paneID string
	terminal := defaultTerminalKind()
	var sessionData map[string]interface{}

	if directPaneID != "" {
		paneID = directPaneID
		if directTerminal != "" {
			terminal = directTerminal
		}
		debugLog(fmt.Sprintf("routing direct pane=%q terminal=%s", paneID, terminal))
	} else {
		// Fallback: find caller's pane_id from session file
		sessionFiles := map[string]string{
			"claude":   ".claude-session",
			"codex":    ".codex-session",
			"gemini":   ".gemini-session",
			"opencode": ".opencode-session",
		}
		sessionFilename := sessionFiles[caller]
		if sessionFilename == "" {
			sessionFilename = ".claude-session"
		}

		workDir := os.Getenv("CURDX_WORK_DIR")
		type searchPath struct {
			path string
		}
		var searchPaths []string
		seen := map[string]bool{}

		addPath := func(p string) {
			if p == "" || seen[p] {
				return
			}
			seen[p] = true
			searchPaths = append(searchPaths, p)
		}

		if workDir != "" {
			found := sessionutil.FindProjectSessionFile(workDir, sessionFilename)
			if found != "" {
				addPath(found)
			}
		}

		cwd, _ := os.Getwd()
		if cwd != "" && cwd != workDir {
			found := sessionutil.FindProjectSessionFile(cwd, sessionFilename)
			if found != "" {
				addPath(found)
			}
		}

		home, _ := os.UserHomeDir()
		if home != "" {
			for _, dirname := range []string{".curdx", ".curdx_config"} {
				addPath(filepath.Join(home, ".local", "share", "codex-dual", dirname, sessionFilename))
			}
		}

		for _, sessionPath := range searchPaths {
			if _, err := os.Stat(sessionPath); err != nil {
				continue
			}
			sessionData = loadSessionFile(sessionPath)
			if sessionData == nil {
				continue
			}
			if v, ok := sessionData["pane_id"]; ok {
				paneID = strings.TrimSpace(fmt.Sprintf("%v", v))
			}
			if v, ok := sessionData["terminal"]; ok {
				terminal = strings.TrimSpace(fmt.Sprintf("%v", v))
			}
			if terminal == "" {
				terminal = defaultTerminalKind()
			}
			if paneID != "" {
				// Validate work_dir
				sessionWorkDir := ""
				if v, ok := sessionData["work_dir"]; ok {
					sessionWorkDir = strings.TrimSpace(fmt.Sprintf("%v", v))
				}
				if workDir != "" && sessionWorkDir != "" && !workDirsCompatible(sessionWorkDir, workDir) {
					debugLog(fmt.Sprintf("session mismatch session=%s session_work_dir=%q request_work_dir=%q",
						sessionPath, sessionWorkDir, workDir))
					paneID = ""
					continue
				}
				debugLog(fmt.Sprintf("routing session pane=%q terminal=%s session=%s", paneID, terminal, sessionPath))
				break
			}
		}
	}

	if paneID == "" {
		debugLog("no pane found, falling back to ask --notify")
		timeout := envFloat("CURDX_COMPLETION_HOOK_TIMEOUT", 10.0)
		askCmd := findAskCommand()
		if askCmd == "" {
			return 0
		}
		env := os.Environ()
		if caller != "" {
			env = append(env, "CURDX_CALLER="+caller)
		}
		cmd := exec.Command(askCmd, caller, "--notify", "--no-wrap", message)
		cmd.Env = env
		cmd.Stdout = nil
		cmd.Stderr = nil
		done := make(chan error, 1)
		cmd.Start()
		go func() { done <- cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(time.Duration(timeout * float64(time.Second))):
		}
		debugLog(fmt.Sprintf("ask --notify fallback completed"))
		return 0
	}

	// Send directly via terminal backend
	terminalOK := sendViaTerminal(paneID, message, terminal, sessionData)
	debugLog(fmt.Sprintf("direct terminal send pane=%q terminal=%s ok=%v", paneID, terminal, terminalOK))
	if terminalOK {
		return 0
	}

	// Fallback to ask command if terminal send failed
	timeout := envFloat("CURDX_COMPLETION_HOOK_TIMEOUT", 10.0)
	askCmd := findAskCommand()
	if askCmd == "" {
		return 0
	}
	env := os.Environ()
	if caller != "" {
		env = append(env, "CURDX_CALLER="+caller)
	}
	cmd := exec.Command(askCmd, caller, "--notify", "--no-wrap", message)
	cmd.Env = env
	cmd.Stdout = nil
	cmd.Stderr = nil
	done := make(chan error, 1)
	_ = cmd.Start()
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(time.Duration(timeout * float64(time.Second))):
	}
	debugLog("post-failure ask --notify completed")
	return 0
}

func orDefault(val, def string) string {
	if val == "" {
		return def
	}
	return val
}

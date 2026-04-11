package terminal

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// WeztermBackend is a pane-oriented WezTerm CLI backend.
type WeztermBackend struct {
	lastListError string
}

// NewWeztermBackend creates a new WeztermBackend.
func NewWeztermBackend() *WeztermBackend {
	return &WeztermBackend{}
}

// CURDXTitleMarker is the default title marker.
const CURDXTitleMarker = "CURDX"

// LastListError returns the last error from list-panes.
func (w *WeztermBackend) LastListError() string {
	return w.lastListError
}

func weztermBin() string {
	found := getWeztermBin()
	if found != "" {
		return found
	}
	return "wezterm"
}

func weztermCLIBaseArgs() []string {
	args := []string{weztermBin(), "cli"}
	weztermClass := os.Getenv("CODEX_WEZTERM_CLASS")
	if weztermClass == "" {
		weztermClass = os.Getenv("WEZTERM_CLASS")
	}
	if weztermClass != "" {
		args = append(args, "--class", weztermClass)
	}
	if isTrueEnv("CODEX_WEZTERM_PREFER_MUX") {
		args = append(args, "--prefer-mux")
	}
	if isTrueEnv("CODEX_WEZTERM_NO_AUTO_START") {
		args = append(args, "--no-auto-start")
	}
	return args
}

func isTrueEnv(name string) bool {
	v := strings.ToLower(os.Getenv(name))
	return v == "1" || v == "true" || v == "yes" || v == "on"
}

func (w *WeztermBackend) sendKeyCLI(paneID string, key string) bool {
	key = strings.TrimSpace(key)
	if key == "" {
		return false
	}

	variants := []string{key}
	lower := strings.ToLower(key)
	if lower == "enter" {
		variants = []string{"Enter", "Return", key}
	} else if lower == "escape" || lower == "esc" {
		variants = []string{"Escape", "Esc", key}
	}

	baseArgs := weztermCLIBaseArgs()
	for _, variant := range variants {
		// Variant A: send-key --pane-id <id> --key <KeyName>
		argsA := append(append([]string{}, baseArgs...), "send-key", "--pane-id", paneID, "--key", variant)
		cmd := exec.Command(argsA[0], argsA[1:]...)
		setSysProcAttr(cmd)
		if out, err := cmd.CombinedOutput(); err == nil {
			_ = out
			return true
		}

		// Variant B: send-key --pane-id <id> <KeyName>
		argsB := append(append([]string{}, baseArgs...), "send-key", "--pane-id", paneID, variant)
		cmd2 := exec.Command(argsB[0], argsB[1:]...)
		setSysProcAttr(cmd2)
		if out, err := cmd2.CombinedOutput(); err == nil {
			_ = out
			return true
		}
	}
	return false
}

func (w *WeztermBackend) sendEnter(paneID string) {
	// Windows needs longer delay.
	defaultDelay := 0.01
	if IsWindows() {
		defaultDelay = 0.05
	}
	enterDelay := EnvFloat("CURDX_WEZTERM_ENTER_DELAY", defaultDelay)
	if enterDelay > 0 {
		time.Sleep(time.Duration(enterDelay * float64(time.Second)))
	}

	envMethod := os.Getenv("CURDX_WEZTERM_ENTER_METHOD")
	defaultMethod := "auto"
	method := strings.ToLower(strings.TrimSpace(envMethod))
	if method == "" {
		method = defaultMethod
	}
	if method != "auto" && method != "key" && method != "text" {
		method = defaultMethod
	}

	maxRetries := 3
	for attempt := 0; attempt < maxRetries; attempt++ {
		if method == "key" || method == "auto" {
			if w.sendKeyCLI(paneID, "Enter") {
				return
			}
		}

		if method == "auto" || method == "text" {
			baseArgs := weztermCLIBaseArgs()
			args := append(append([]string{}, baseArgs...), "send-text", "--pane-id", paneID, "--no-paste")
			cmd := exec.Command(args[0], args[1:]...)
			setSysProcAttr(cmd)
			cmd.Stdin = strings.NewReader("\r")
			if err := cmd.Run(); err == nil {
				return
			}
		}

		if attempt < maxRetries-1 {
			time.Sleep(50 * time.Millisecond)
		}
	}
}

// SendText sends text to a WezTerm pane.
func (w *WeztermBackend) SendText(paneID string, text string) error {
	sanitized := strings.TrimSpace(strings.ReplaceAll(text, "\r", ""))
	if sanitized == "" {
		return nil
	}

	hasNewlines := strings.Contains(sanitized, "\n")
	baseArgs := weztermCLIBaseArgs()

	// Single-line: always avoid paste mode.
	if !hasNewlines {
		if len(sanitized) <= 200 {
			args := append(append([]string{}, baseArgs...), "send-text", "--pane-id", paneID, "--no-paste", sanitized)
			cmd := exec.Command(args[0], args[1:]...)
			setSysProcAttr(cmd)
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("wezterm send-text failed: %w", err)
			}
		} else {
			args := append(append([]string{}, baseArgs...), "send-text", "--pane-id", paneID, "--no-paste")
			cmd := exec.Command(args[0], args[1:]...)
			setSysProcAttr(cmd)
			cmd.Stdin = strings.NewReader(sanitized)
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("wezterm send-text failed: %w", err)
			}
		}
		w.sendEnter(paneID)
		return nil
	}

	// Multiline: use paste mode (bracketed paste).
	args := append(append([]string{}, baseArgs...), "send-text", "--pane-id", paneID)
	cmd := exec.Command(args[0], args[1:]...)
	setSysProcAttr(cmd)
	cmd.Stdin = strings.NewReader(sanitized)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("wezterm send-text failed: %w", err)
	}

	pasteDelay := EnvFloat("CURDX_WEZTERM_PASTE_DELAY", 0.1)
	if pasteDelay > 0 {
		time.Sleep(time.Duration(pasteDelay * float64(time.Second)))
	}

	w.sendEnter(paneID)
	return nil
}

// PaneLogPath returns the pane log path for a WezTerm pane.
func (w *WeztermBackend) PaneLogPath(paneID string) string {
	pid := strings.TrimSpace(paneID)
	if pid == "" {
		return ""
	}
	return PaneLogPathFor(pid, "wezterm", "")
}

// EnsurePaneLog creates/touches the pane log file for consistency.
func (w *WeztermBackend) EnsurePaneLog(paneID string) string {
	logPath := w.PaneLogPath(paneID)
	if logPath == "" {
		return ""
	}
	CleanupPaneLogs(filepath.Dir(logPath))
	os.MkdirAll(filepath.Dir(logPath), 0o755)
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY, 0o644)
	if err == nil {
		f.Close()
	}
	return logPath
}

// WeztermPaneInfo represents a pane from wezterm cli list.
type WeztermPaneInfo struct {
	PaneID string
	Title  string
	CWD    string
}

// parseListOutput parses wezterm cli list text output.
func parseListOutput(text string) []WeztermPaneInfo {
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimRight(line, " \t\r")
		if strings.TrimSpace(trimmed) != "" {
			lines = append(lines, trimmed)
		}
	}
	if len(lines) == 0 {
		return nil
	}

	header := lines[0]
	headerUpper := strings.ToUpper(header)

	if strings.Contains(headerUpper, "PANE") {
		entries := parseWithHeader(lines)
		if len(entries) > 0 {
			return entries
		}
	}

	// Fallback: parse rows without headers.
	var entries []WeztermPaneInfo
	digitRe := regexp.MustCompile(`^\d+$`)
	for _, line := range lines {
		tokens := strings.Fields(line)
		for _, tok := range tokens {
			if digitRe.MatchString(tok) {
				entries = append(entries, WeztermPaneInfo{PaneID: tok})
				break
			}
		}
	}
	return entries
}

func parseWithHeader(lines []string) []WeztermPaneInfo {
	header := lines[0]
	colRe := regexp.MustCompile(`\S+`)
	matches := colRe.FindAllStringIndex(header, -1)
	if len(matches) == 0 {
		return nil
	}

	type colDef struct {
		name  string
		start int
		end   int // -1 means to end of line
	}
	var cols []colDef
	for i, m := range matches {
		name := strings.ToUpper(header[m[0]:m[1]])
		end := -1
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}
		cols = append(cols, colDef{name: name, start: m[0], end: end})
	}

	findCol := func(names ...string) *colDef {
		for i := range cols {
			for _, n := range names {
				if cols[i].name == n {
					return &cols[i]
				}
			}
		}
		return nil
	}

	paneCol := findCol("PANEID", "PANE_ID", "PANE")
	titleCol := findCol("TITLE")

	var entries []WeztermPaneInfo
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var entry WeztermPaneInfo
		if paneCol != nil {
			var raw string
			if paneCol.end == -1 {
				if paneCol.start < len(line) {
					raw = line[paneCol.start:]
				}
			} else {
				if paneCol.start < len(line) {
					end := paneCol.end
					if end > len(line) {
						end = len(line)
					}
					raw = line[paneCol.start:end]
				}
			}
			entry.PaneID = strings.TrimSpace(raw)
		}
		if titleCol != nil {
			var raw string
			if titleCol.end == -1 {
				if titleCol.start < len(line) {
					raw = line[titleCol.start:]
				}
			} else {
				if titleCol.start < len(line) {
					end := titleCol.end
					if end > len(line) {
						end = len(line)
					}
					raw = line[titleCol.start:end]
				}
			}
			entry.Title = strings.TrimSpace(raw)
		}
		if entry.PaneID != "" {
			entries = append(entries, entry)
		}
	}
	return entries
}

func (w *WeztermBackend) listPanes() ([]map[string]interface{}, error) {
	w.lastListError = ""

	baseArgs := weztermCLIBaseArgs()

	// Try JSON format first.
	args := append(append([]string{}, baseArgs...), "list", "--format", "json")
	cmd := exec.Command(args[0], args[1:]...)
	setSysProcAttr(cmd)
	out, err := cmd.Output()
	if err == nil {
		var panes []map[string]interface{}
		if jsonErr := json.Unmarshal(out, &panes); jsonErr != nil {
			w.lastListError = fmt.Sprintf("wezterm cli list json parse failed: %v", jsonErr)
		} else {
			return panes, nil
		}
	} else {
		if exitErr, ok := err.(*exec.ExitError); ok {
			errStr := strings.TrimSpace(string(exitErr.Stderr))
			if errStr == "" {
				errStr = strings.TrimSpace(string(out))
			}
			if errStr != "" {
				w.lastListError = fmt.Sprintf("wezterm cli list failed (%d): %s", exitErr.ExitCode(), errStr)
			} else {
				w.lastListError = fmt.Sprintf("wezterm cli list failed (%d)", exitErr.ExitCode())
			}
		} else {
			w.lastListError = fmt.Sprintf("wezterm cli list failed: %v", err)
		}
	}

	// Fallback: older WezTerm without --format json.
	args2 := append(append([]string{}, baseArgs...), "list")
	cmd2 := exec.Command(args2[0], args2[1:]...)
	setSysProcAttr(cmd2)
	var stdout2, stderr2 strings.Builder
	cmd2.Stdout = &stdout2
	cmd2.Stderr = &stderr2
	err2 := cmd2.Run()
	if err2 == nil {
		parsed := parseListOutput(stdout2.String())
		if len(parsed) > 0 {
			w.lastListError = ""
			// Convert to map format for compatibility.
			result := make([]map[string]interface{}, len(parsed))
			for i, p := range parsed {
				m := map[string]interface{}{"pane_id": p.PaneID}
				if p.Title != "" {
					m["title"] = p.Title
				}
				result[i] = m
			}
			return result, nil
		}
		if strings.TrimSpace(stdout2.String()) != "" {
			w.lastListError = "wezterm cli list returned unparseable output"
			return nil, fmt.Errorf("%s", w.lastListError)
		}
		return []map[string]interface{}{}, nil
	}
	if exitErr, ok := err2.(*exec.ExitError); ok {
		errStr := strings.TrimSpace(stderr2.String())
		if errStr == "" {
			errStr = strings.TrimSpace(stdout2.String())
		}
		if errStr != "" {
			w.lastListError = fmt.Sprintf("wezterm cli list failed (%d): %s", exitErr.ExitCode(), errStr)
		} else {
			w.lastListError = fmt.Sprintf("wezterm cli list failed (%d)", exitErr.ExitCode())
		}
	} else {
		w.lastListError = fmt.Sprintf("wezterm cli list failed: %v", err2)
	}
	return nil, fmt.Errorf("%s", w.lastListError)
}

// extractCWDPath extracts a filesystem path from a WezTerm file:// CWD URL.
func extractCWDPath(fileURL string) string {
	if fileURL == "" {
		return ""
	}
	u := strings.TrimSpace(fileURL)
	if !strings.HasPrefix(u, "file://") {
		return u
	}
	rest := u[7:] // strip "file://"
	var path string
	if strings.HasPrefix(rest, "/") {
		path = rest
	} else {
		// file://hostname/path -> /path
		slash := strings.Index(rest, "/")
		if slash >= 0 {
			path = rest[slash:]
		} else {
			path = ""
		}
	}
	// URL-decode percent-encoded characters.
	decoded, err := url.PathUnescape(path)
	if err == nil {
		path = decoded
	}
	// Windows: file:///C:/path -> /C:/path, strip leading slash before drive letter.
	if len(path) >= 3 && path[0] == '/' && path[2] == ':' {
		path = path[1:]
	}
	path = strings.TrimRight(path, "/")
	if path == "" {
		return "/"
	}
	return path
}

// cwdMatches checks if a pane's CWD matches the expected work directory.
func cwdMatches(paneCWD string, workDir string) bool {
	if paneCWD == "" || workDir == "" {
		return false
	}
	extracted := extractCWDPath(paneCWD)
	if extracted == "" {
		return false
	}
	return filepath.Clean(extracted) == filepath.Clean(workDir)
}

func paneIDByTitleMarker(panes []map[string]interface{}, marker string, cwdHint string) string {
	if marker == "" {
		return ""
	}
	cwdHint = strings.TrimSpace(cwdHint)
	// When cwdHint is provided, prefer panes matching both marker AND CWD.
	if cwdHint != "" {
		for _, pane := range panes {
			title, _ := pane["title"].(string)
			if strings.HasPrefix(title, marker) {
				cwd, _ := pane["cwd"].(string)
				if cwdMatches(cwd, cwdHint) {
					paneID := paneMapID(pane)
					if paneID != "" {
						return paneID
					}
				}
			}
		}
	}
	// Fallback: first marker match.
	for _, pane := range panes {
		title, _ := pane["title"].(string)
		if strings.HasPrefix(title, marker) {
			paneID := paneMapID(pane)
			if paneID != "" {
				return paneID
			}
		}
	}
	return ""
}

func paneMapID(pane map[string]interface{}) string {
	v, ok := pane["pane_id"]
	if !ok {
		return ""
	}
	switch id := v.(type) {
	case string:
		return id
	case float64:
		return fmt.Sprintf("%d", int(id))
	case json.Number:
		return id.String()
	default:
		return fmt.Sprintf("%v", id)
	}
}

// FindPaneByTitleMarker finds a pane whose title starts with marker.
func (w *WeztermBackend) FindPaneByTitleMarker(marker string, cwdHint string) string {
	panes, err := w.listPanes()
	if err != nil || panes == nil {
		return ""
	}
	return paneIDByTitleMarker(panes, marker, cwdHint)
}

// PaneBelongsToCWD returns true if pane's CWD matches workDir (or can't be verified).
func (w *WeztermBackend) PaneBelongsToCWD(paneID string, workDir string) bool {
	panes, err := w.listPanes()
	if err != nil || len(panes) == 0 {
		return true // Can't verify - assume OK.
	}
	for _, pane := range panes {
		if paneMapID(pane) == paneID {
			cwd, _ := pane["cwd"].(string)
			if cwd == "" {
				return true // No CWD info - assume OK.
			}
			return cwdMatches(cwd, workDir)
		}
	}
	return false // Pane not found.
}

// IsAlive returns true if the pane exists in the list.
func (w *WeztermBackend) IsAlive(paneID string) bool {
	panes, err := w.listPanes()
	if err != nil || panes == nil || len(panes) == 0 {
		return false
	}
	for _, p := range panes {
		if paneMapID(p) == paneID {
			return true
		}
	}
	return paneIDByTitleMarker(panes, paneID, "") != ""
}

// GetText gets text content from pane (last N lines).
func (w *WeztermBackend) GetText(paneID string, lines int) string {
	baseArgs := weztermCLIBaseArgs()
	args := append(append([]string{}, baseArgs...), "get-text", "--pane-id", paneID)
	cmd := exec.Command(args[0], args[1:]...)
	setSysProcAttr(cmd)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	text := string(out)
	if lines > 0 && text != "" {
		textLines := strings.Split(text, "\n")
		if len(textLines) > lines {
			textLines = textLines[len(textLines)-lines:]
		}
		return strings.Join(textLines, "\n")
	}
	return text
}

// SendKey sends a special key to a pane.
func (w *WeztermBackend) SendKey(paneID string, key string) bool {
	key = strings.TrimSpace(key)
	if paneID == "" || key == "" {
		return false
	}
	if w.sendKeyCLI(paneID, key) {
		return true
	}
	lower := strings.ToLower(key)
	var payload string
	switch lower {
	case "enter", "return":
		payload = "\r"
	case "escape", "esc":
		payload = "\x1b"
	default:
		if len(key) == 1 {
			payload = key
		} else {
			return false
		}
	}
	baseArgs := weztermCLIBaseArgs()
	args := append(append([]string{}, baseArgs...), "send-text", "--pane-id", paneID, "--no-paste")
	cmd := exec.Command(args[0], args[1:]...)
	setSysProcAttr(cmd)
	cmd.Stdin = strings.NewReader(payload)
	return cmd.Run() == nil
}

// KillPane kills a WezTerm pane.
func (w *WeztermBackend) KillPane(paneID string) {
	baseArgs := weztermCLIBaseArgs()
	args := append(append([]string{}, baseArgs...), "kill-pane", "--pane-id", paneID)
	cmd := exec.Command(args[0], args[1:]...)
	setSysProcAttr(cmd)
	cmd.Stderr = nil
	cmd.Run()
}

// Activate focuses a WezTerm pane.
func (w *WeztermBackend) Activate(paneID string) {
	baseArgs := weztermCLIBaseArgs()
	args := append(append([]string{}, baseArgs...), "activate-pane", "--pane-id", paneID)
	cmd := exec.Command(args[0], args[1:]...)
	setSysProcAttr(cmd)
	cmd.Run()
}

// CreatePane creates a new WezTerm pane.
func (w *WeztermBackend) CreatePane(cmdStr string, cwd string, direction string, percent int, parentPane string) (string, error) {
	baseArgs := weztermCLIBaseArgs()
	args := append(append([]string{}, baseArgs...), "split-pane")
	forceWSL := strings.EqualFold(os.Getenv("CURDX_BACKEND_ENV"), "wsl")
	wslUncCwd := extractWSLPathFromUNCLikePath(cwd)

	// If the caller is in a WSL UNC path, default to launching via wsl.exe.
	if IsWindows() && wslUncCwd != "" && !forceWSL {
		forceWSL = true
	}

	useWSLLaunch := (IsWSL() && isWindowsWezterm()) || (forceWSL && IsWindows())
	if useWSLLaunch {
		inWSLPane := os.Getenv("WSL_DISTRO_NAME") != "" || os.Getenv("WSL_INTEROP") != ""
		wslCwd := cwd
		if wslUncCwd != "" {
			wslCwd = wslUncCwd
		}
		if wslUncCwd == "" && (strings.Contains(cwd, "\\") || (len(cwd) > 2 && cwd[1] == ':')) {
			// Try to convert Windows path to WSL path.
			var wslpathArgs []string
			if IsWSL() {
				wslpathArgs = []string{"wslpath", "-a", cwd}
			} else {
				wslpathArgs = []string{"wsl.exe", "wslpath", "-a", cwd}
			}
			cmd := exec.Command(wslpathArgs[0], wslpathArgs[1:]...)
			setSysProcAttr(cmd)
			if out, err := cmd.Output(); err == nil {
				wslCwd = strings.TrimSpace(string(out))
			}
		}
		if direction == "right" {
			args = append(args, "--right")
		} else if direction == "bottom" {
			args = append(args, "--bottom")
		}
		args = append(args, "--percent", fmt.Sprintf("%d", percent))
		if parentPane != "" {
			args = append(args, "--pane-id", parentPane)
		}
		startupScript := fmt.Sprintf("cd %s && %s", shellQuote(wslCwd), cmdStr)
		if inWSLPane {
			args = append(args, "--", "bash", "-l", "-i", "-c", startupScript)
		} else {
			args = append(args, "--", "wsl.exe", "bash", "-l", "-i", "-c", startupScript)
		}
	} else {
		args = append(args, "--cwd", cwd)
		if direction == "right" {
			args = append(args, "--right")
		} else if direction == "bottom" {
			args = append(args, "--bottom")
		}
		args = append(args, "--percent", fmt.Sprintf("%d", percent))
		if parentPane != "" {
			args = append(args, "--pane-id", parentPane)
		}
		shell, flag := DefaultShell()
		args = append(args, "--", shell, flag, cmdStr)
	}

	cmd := exec.Command(args[0], args[1:]...)
	setSysProcAttr(cmd)
	if IsWSL() && isWindowsWezterm() {
		runCwd := chooseWeztermCLICWD()
		if runCwd != "" {
			cmd.Dir = runCwd
		}
	}
	out, err := cmd.Output()
	if err != nil {
		errMsg := ""
		if exitErr, ok := err.(*exec.ExitError); ok {
			errMsg = string(exitErr.Stderr)
		}
		return "", fmt.Errorf("WezTerm split-pane failed:\nCommand: %s\nStderr: %s",
			strings.Join(args, " "), errMsg)
	}
	return strings.TrimSpace(string(out)), nil
}

// WeztermCLIIsAlive probes if wezterm cli can reach a running instance.
func WeztermCLIIsAlive(timeoutS float64) bool {
	wez := getWeztermBin()
	if wez == "" {
		return false
	}
	cmd := exec.Command(wez, "cli", "--no-auto-start", "list")
	setSysProcAttr(cmd)
	out, err := cmd.Output()
	_ = out
	return err == nil
}

// RespawnPane is a no-op for WezTerm (respawn is tmux-specific).
func (w *WeztermBackend) RespawnPane(paneID string, cmd, cwd, stderrLogPath string, remainOnExit bool) error {
	return fmt.Errorf("respawn not supported on WezTerm")
}

// SaveCrashLog saves the last N lines of pane content to a crash log file.
func (w *WeztermBackend) SaveCrashLog(paneID, logPath string, lines int) error {
	content := w.GetText(paneID, lines)
	if content == "" {
		return nil
	}
	return os.WriteFile(logPath, []byte(content), 0o644)
}

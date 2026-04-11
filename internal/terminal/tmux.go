package terminal

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// TmuxBackend is a pane-oriented tmux backend.
//
// Compatibility note:
//   - New API prefers tmux pane IDs like `%12`.
//   - Legacy CURDX code may still pass a tmux session name as pane_id.
//     Methods accept both: if target starts with % or contains : or . it is
//     treated as a tmux target; otherwise as a session name.
type TmuxBackend struct {
	socketName string

	paneLogInfoMu sync.Mutex
	paneLogInfo   map[string]float64
}

// NewTmuxBackend creates a TmuxBackend with optional socket name for isolation.
func NewTmuxBackend(socketName string) *TmuxBackend {
	sn := strings.TrimSpace(socketName)
	if sn == "" {
		sn = strings.TrimSpace(os.Getenv("CURDX_TMUX_SOCKET"))
	}
	if sn == "" {
		return &TmuxBackend{paneLogInfo: map[string]float64{}}
	}
	return &TmuxBackend{socketName: sn, paneLogInfo: map[string]float64{}}
}

// SocketName returns the configured tmux socket name.
func (t *TmuxBackend) SocketName() string {
	return t.socketName
}

var ansiRE = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)

func (t *TmuxBackend) tmuxBase() []string {
	cmd := []string{"tmux"}
	if t.socketName != "" {
		cmd = append(cmd, "-L", t.socketName)
	}
	return cmd
}

// TmuxRunResult mirrors subprocess.CompletedProcess for tmux calls.
type TmuxRunResult struct {
	Stdout     string
	Stderr     string
	ReturnCode int
}

// tmuxRun executes a tmux command with the configured socket.
func (t *TmuxBackend) tmuxRun(args []string, check bool, capture bool, inputBytes []byte, timeoutSec float64) (*TmuxRunResult, error) {
	// Clone base args to prevent concurrent append from overwriting shared backing array.
	base := t.tmuxBase()
	cmdArgs := make([]string, len(base), len(base)+len(args))
	copy(cmdArgs, base)
	cmdArgs = append(cmdArgs, args...)

	// Use context with timeout if specified.
	var cmd *exec.Cmd
	var cancel context.CancelFunc
	if timeoutSec > 0 {
		var ctx context.Context
		ctx, cancel = context.WithTimeout(context.Background(), time.Duration(timeoutSec*float64(time.Second)))
		cmd = exec.CommandContext(ctx, cmdArgs[0], cmdArgs[1:]...)
	} else {
		cmd = exec.Command(cmdArgs[0], cmdArgs[1:]...)
	}
	if cancel != nil {
		defer cancel()
	}
	setSysProcAttr(cmd)

	if inputBytes != nil {
		cmd.Stdin = strings.NewReader(string(inputBytes))
	}

	var result TmuxRunResult

	if capture {
		var stdout, stderr strings.Builder
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		result.Stdout = stdout.String()
		result.Stderr = stderr.String()
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				result.ReturnCode = exitErr.ExitCode()
			} else {
				result.ReturnCode = 1
			}
			if check {
				return &result, fmt.Errorf("tmux command failed (exit %d): %s", result.ReturnCode, result.Stderr)
			}
			return &result, nil
		}
		result.ReturnCode = 0
		return &result, nil
	}

	err := cmd.Run()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ReturnCode = exitErr.ExitCode()
		} else {
			result.ReturnCode = 1
		}
		if check {
			return &result, fmt.Errorf("tmux command failed (exit %d)", result.ReturnCode)
		}
		return &result, nil
	}
	result.ReturnCode = 0
	return &result, nil
}

// TmuxRun is the exported version used by layout.go. Same as tmuxRun.
func (t *TmuxBackend) TmuxRun(args []string, check bool, capture bool, inputBytes []byte, timeoutSec float64) (*TmuxRunResult, error) {
	return t.tmuxRun(args, check, capture, inputBytes, timeoutSec)
}

// LooksLikePaneID checks if a value looks like a tmux pane ID (%xx).
func LooksLikePaneID(value string) bool {
	return strings.HasPrefix(strings.TrimSpace(value), "%")
}

// looksLikeTmuxTarget checks if a value could be a tmux target (pane/window/session).
func looksLikeTmuxTarget(value string) bool {
	v := strings.TrimSpace(value)
	if v == "" {
		return false
	}
	return strings.HasPrefix(v, "%") || strings.Contains(v, ":") || strings.Contains(v, ".")
}

// PaneExists returns true if the tmux pane target exists.
func (t *TmuxBackend) PaneExists(paneID string) bool {
	if !LooksLikePaneID(paneID) {
		return false
	}
	result, err := t.tmuxRun([]string{"display-message", "-p", "-t", paneID, "#{pane_id}"}, false, true, nil, 0.5)
	if err != nil {
		return false
	}
	return result.ReturnCode == 0 && strings.HasPrefix(strings.TrimSpace(result.Stdout), "%")
}

// GetCurrentPaneID returns the current tmux pane id in %xx format.
func (t *TmuxBackend) GetCurrentPaneID() (string, error) {
	envPane := strings.TrimSpace(os.Getenv("TMUX_PANE"))
	if LooksLikePaneID(envPane) && t.PaneExists(envPane) {
		return envPane, nil
	}

	result, err := t.tmuxRun([]string{"display-message", "-p", "#{pane_id}"}, false, true, nil, 0.5)
	if err == nil {
		out := strings.TrimSpace(result.Stdout)
		if LooksLikePaneID(out) && t.PaneExists(out) {
			return out, nil
		}
	}

	return "", fmt.Errorf("tmux current pane id not available")
}

// SplitPane splits the parent pane and returns the new pane ID.
func (t *TmuxBackend) SplitPane(parentPaneID string, direction string, percent int) (string, error) {
	if parentPaneID == "" {
		return "", fmt.Errorf("parent_pane_id is required")
	}

	// Unzoom if needed.
	if LooksLikePaneID(parentPaneID) {
		zoomCP, _ := t.tmuxRun([]string{"display-message", "-p", "-t", parentPaneID, "#{window_zoomed_flag}"}, false, true, nil, 0.5)
		if zoomCP != nil && zoomCP.ReturnCode == 0 {
			flag := strings.TrimSpace(zoomCP.Stdout)
			if flag == "1" || flag == "on" || flag == "yes" || flag == "true" {
				t.tmuxRun([]string{"resize-pane", "-Z", "-t", parentPaneID}, false, false, nil, 0.5)
			}
		}
	}

	// Check pane exists.
	if LooksLikePaneID(parentPaneID) && !t.PaneExists(parentPaneID) {
		return "", fmt.Errorf("cannot split: pane %s does not exist", parentPaneID)
	}

	sizeCP, _ := t.tmuxRun([]string{"display-message", "-p", "-t", parentPaneID, "#{pane_width}x#{pane_height}"}, false, true, nil, 0)
	paneSize := "unknown"
	if sizeCP != nil && sizeCP.ReturnCode == 0 {
		paneSize = strings.TrimSpace(sizeCP.Stdout)
	}

	dirNorm := strings.ToLower(strings.TrimSpace(direction))
	var flag string
	switch dirNorm {
	case "right", "h", "horizontal":
		flag = "-h"
	case "bottom", "v", "vertical":
		flag = "-v"
	default:
		return "", fmt.Errorf("unsupported direction: %q (use 'right' or 'bottom')", direction)
	}

	// NOTE: Do not pass `-p <percent>` here.
	// tmux 3.4 can error with `size missing` when splitting by percentage in detached sessions.
	result, err := t.tmuxRun([]string{"split-window", flag, "-t", parentPaneID, "-P", "-F", "#{pane_id}"}, true, true, nil, 0)
	if err != nil {
		return "", fmt.Errorf(
			"tmux split-window failed (exit %d): %s\nPane: %s, size: %s, direction: %s\nHint: If the pane is zoomed, press Prefix+z to unzoom; also try enlarging terminal window.",
			result.ReturnCode, strings.TrimSpace(result.Stderr),
			parentPaneID, paneSize, dirNorm,
		)
	}
	paneID := strings.TrimSpace(result.Stdout)
	if !LooksLikePaneID(paneID) {
		return "", fmt.Errorf("tmux split-window did not return pane_id: %q", paneID)
	}
	return paneID, nil
}

// SetPaneTitle sets the tmux pane title.
func (t *TmuxBackend) SetPaneTitle(paneID string, title string) {
	if paneID == "" {
		return
	}
	t.tmuxRun([]string{"select-pane", "-t", paneID, "-T", title}, false, false, nil, 0)
}

// SetPaneUserOption sets a tmux user option at pane scope.
func (t *TmuxBackend) SetPaneUserOption(paneID string, name string, value string) {
	if paneID == "" {
		return
	}
	opt := strings.TrimSpace(name)
	if opt == "" {
		return
	}
	if !strings.HasPrefix(opt, "@") {
		opt = "@" + opt
	}
	t.tmuxRun([]string{"set-option", "-p", "-t", paneID, opt, value}, false, false, nil, 0)
}

// FindPaneByTitleMarker finds a pane whose title starts with marker.
func (t *TmuxBackend) FindPaneByTitleMarker(marker string, cwdHint string) string {
	marker = strings.TrimSpace(marker)
	if marker == "" {
		return ""
	}
	result, _ := t.tmuxRun([]string{"list-panes", "-a", "-F", "#{pane_id}\t#{pane_title}"}, false, true, nil, 0)
	if result == nil || result.ReturnCode != 0 {
		return ""
	}
	for _, line := range strings.Split(result.Stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var pid, title string
		if strings.Contains(line, "\t") {
			parts := strings.SplitN(line, "\t", 2)
			pid = parts[0]
			if len(parts) > 1 {
				title = parts[1]
			}
		} else {
			parts := strings.SplitN(line, " ", 2)
			pid = parts[0]
			if len(parts) > 1 {
				title = parts[1]
			}
		}
		if strings.HasPrefix(title, marker) {
			pid = strings.TrimSpace(pid)
			if LooksLikePaneID(pid) {
				return pid
			}
		}
	}
	return ""
}

// GetPaneContent captures the last N lines of a pane's visible content.
func (t *TmuxBackend) GetPaneContent(paneID string, lines int) string {
	if paneID == "" {
		return ""
	}
	if lines < 1 {
		lines = 1
	}
	result, _ := t.tmuxRun([]string{"capture-pane", "-t", paneID, "-p", "-S", fmt.Sprintf("-%d", lines)}, false, true, nil, 0)
	if result == nil || result.ReturnCode != 0 {
		return ""
	}
	return ansiRE.ReplaceAllString(result.Stdout, "")
}

// GetText is an alias for GetPaneContent (compatibility).
func (t *TmuxBackend) GetText(paneID string, lines int) string {
	return t.GetPaneContent(paneID, lines)
}

// IsPaneAlive returns true if the pane process is alive (not dead).
func (t *TmuxBackend) IsPaneAlive(paneID string) bool {
	if paneID == "" {
		return false
	}
	result, _ := t.tmuxRun([]string{"display-message", "-p", "-t", paneID, "#{pane_dead}"}, false, true, nil, 0)
	if result == nil || result.ReturnCode != 0 {
		return false
	}
	return strings.TrimSpace(result.Stdout) == "0"
}

func (t *TmuxBackend) ensureNotInCopyMode(paneID string) {
	result, err := t.tmuxRun([]string{"display-message", "-p", "-t", paneID, "#{pane_in_mode}"}, false, true, nil, 1.0)
	if err == nil && result != nil && result.ReturnCode == 0 {
		val := strings.TrimSpace(result.Stdout)
		if val == "1" || val == "on" || val == "yes" {
			t.tmuxRun([]string{"send-keys", "-t", paneID, "-X", "cancel"}, false, false, nil, 0)
		}
	}
}

// SendText sends text to a tmux pane or session.
func (t *TmuxBackend) SendText(paneID string, text string) error {
	sanitized := strings.TrimSpace(strings.ReplaceAll(text, "\r", ""))
	if sanitized == "" {
		return nil
	}

	// Legacy: treat pane_id as a tmux session name for pure-tmux mode.
	if !looksLikeTmuxTarget(paneID) {
		session := paneID
		if !strings.Contains(sanitized, "\n") && len(sanitized) <= 200 {
			if _, err := t.tmuxRun([]string{"send-keys", "-t", session, "-l", sanitized}, true, false, nil, 0); err != nil {
				return err
			}
			if _, err := t.tmuxRun([]string{"send-keys", "-t", session, "Enter"}, true, false, nil, 0); err != nil {
				return err
			}
			return nil
		}
		bufferName := fmt.Sprintf("curdx-tb-%d-%d-%d", os.Getpid(), time.Now().UnixMilli(), rand.Intn(9000)+1000)
		if _, err := t.tmuxRun([]string{"load-buffer", "-b", bufferName, "-"}, true, false, []byte(sanitized), 0); err != nil {
			return err
		}
		defer t.tmuxRun([]string{"delete-buffer", "-b", bufferName}, false, false, nil, 0)
		if _, err := t.tmuxRun([]string{"paste-buffer", "-t", session, "-b", bufferName, "-p"}, true, false, nil, 0); err != nil {
			return err
		}
		enterDelay := EnvFloat("CURDX_TMUX_ENTER_DELAY", 0.5)
		if enterDelay > 0 {
			time.Sleep(time.Duration(enterDelay * float64(time.Second)))
		}
		if _, err := t.tmuxRun([]string{"send-keys", "-t", session, "Enter"}, true, false, nil, 0); err != nil {
			return err
		}
		return nil
	}

	// Pane-oriented: bracketed paste + unique tmux buffer + cleanup.
	t.ensureNotInCopyMode(paneID)
	bufferName := fmt.Sprintf("curdx-tb-%d-%d-%d", os.Getpid(), time.Now().UnixMilli(), rand.Intn(9000)+1000)
	if _, err := t.tmuxRun([]string{"load-buffer", "-b", bufferName, "-"}, true, false, []byte(sanitized), 0); err != nil {
		return err
	}
	defer t.tmuxRun([]string{"delete-buffer", "-b", bufferName}, false, false, nil, 0)
	if _, err := t.tmuxRun([]string{"paste-buffer", "-p", "-t", paneID, "-b", bufferName}, true, false, nil, 0); err != nil {
		return err
	}
	enterDelay := EnvFloat("CURDX_TMUX_ENTER_DELAY", 0.5)
	if enterDelay > 0 {
		time.Sleep(time.Duration(enterDelay * float64(time.Second)))
	}
	if _, err := t.tmuxRun([]string{"send-keys", "-t", paneID, "Enter"}, true, false, nil, 0); err != nil {
		return err
	}
	return nil
}

// SendKey sends a single key to a pane.
func (t *TmuxBackend) SendKey(paneID string, key string) bool {
	key = strings.TrimSpace(key)
	if paneID == "" || key == "" {
		return false
	}
	result, err := t.tmuxRun([]string{"send-keys", "-t", paneID, key}, false, true, nil, 2.0)
	if err != nil {
		return false
	}
	return result.ReturnCode == 0
}

// IsAlive returns true if the pane or session is alive.
func (t *TmuxBackend) IsAlive(paneID string) bool {
	if paneID == "" {
		return false
	}
	if looksLikeTmuxTarget(paneID) {
		return t.IsPaneAlive(paneID)
	}
	result, _ := t.tmuxRun([]string{"has-session", "-t", paneID}, false, true, nil, 0)
	return result != nil && result.ReturnCode == 0
}

// KillPane kills a pane or session.
func (t *TmuxBackend) KillPane(paneID string) {
	if paneID == "" {
		return
	}
	if looksLikeTmuxTarget(paneID) {
		t.tmuxRun([]string{"kill-pane", "-t", paneID}, false, false, nil, 0)
	} else {
		// Legacy: treat as session name.
		t.tmuxRun([]string{"kill-session", "-t", paneID}, false, false, nil, 0)
	}
}

// Activate focuses a pane.
func (t *TmuxBackend) Activate(paneID string) {
	if paneID == "" {
		return
	}
	if looksLikeTmuxTarget(paneID) {
		t.tmuxRun([]string{"select-pane", "-t", paneID}, false, false, nil, 0)
		if os.Getenv("TMUX") == "" {
			result, err := t.tmuxRun([]string{"display-message", "-p", "-t", paneID, "#{session_name}"}, false, true, nil, 0)
			if err == nil && result != nil {
				sess := strings.TrimSpace(result.Stdout)
				if sess != "" {
					t.tmuxRun([]string{"attach", "-t", sess}, false, false, nil, 0)
				}
			}
		}
		return
	}
	t.tmuxRun([]string{"attach", "-t", paneID}, false, false, nil, 0)
}

// RespawnPane respawns a pane process.
func (t *TmuxBackend) RespawnPane(paneID string, cmd string, cwd string, stderrLogPath string, remainOnExit bool) error {
	if paneID == "" {
		return fmt.Errorf("pane_id is required")
	}

	// Best-effort log setup.
	t.EnsurePaneLog(paneID)

	cmdBody := strings.TrimSpace(cmd)
	if cmdBody == "" {
		return fmt.Errorf("cmd is required")
	}

	startDir := strings.TrimSpace(cwd)
	if startDir == "." {
		startDir = ""
	}

	if stderrLogPath != "" {
		logPath := stderrLogPath
		dir := filepath.Dir(logPath)
		os.MkdirAll(dir, 0o755)
		cmdBody = fmt.Sprintf("%s 2>> %s", cmdBody, shellQuote(logPath))
	}

	shell := strings.TrimSpace(os.Getenv("CURDX_TMUX_SHELL"))
	if shell == "" {
		result, err := t.tmuxRun([]string{"show-option", "-gqv", "default-shell"}, false, true, nil, 1.0)
		if err == nil && result != nil {
			shell = strings.TrimSpace(result.Stdout)
		}
	}
	if shell == "" {
		shell = strings.TrimSpace(os.Getenv("SHELL"))
	}
	if shell == "" {
		shell, _ = DefaultShell()
	}

	flagsRaw := strings.TrimSpace(os.Getenv("CURDX_TMUX_SHELL_FLAGS"))
	var flags []string
	if flagsRaw != "" {
		flags = splitShellArgs(flagsRaw)
	} else {
		shellName := strings.ToLower(filepath.Base(shell))
		switch shellName {
		case "bash", "zsh", "ksh", "fish":
			flags = []string{"-l", "-i", "-c"}
		default:
			flags = []string{"-c"}
		}
	}

	fullArgv := append([]string{shell}, flags...)
	fullArgv = append(fullArgv, cmdBody)
	full := shellJoin(fullArgv)

	// Prevent a race where a fast-exiting command closes the pane.
	if remainOnExit {
		t.tmuxRun([]string{"set-option", "-p", "-t", paneID, "remain-on-exit", "on"}, false, false, nil, 0)
	}

	tmuxArgs := []string{"respawn-pane", "-k", "-t", paneID}
	if startDir != "" {
		tmuxArgs = append(tmuxArgs, "-c", startDir)
	}
	tmuxArgs = append(tmuxArgs, full)
	if _, err := t.tmuxRun(tmuxArgs, true, false, nil, 0); err != nil {
		return err
	}
	if remainOnExit {
		t.tmuxRun([]string{"set-option", "-p", "-t", paneID, "remain-on-exit", "on"}, false, false, nil, 0)
	}
	return nil
}

// SaveCrashLog saves pane content to a crash log file.
func (t *TmuxBackend) SaveCrashLog(paneID string, crashLogPath string, lines int) error {
	text := t.GetPaneContent(paneID, lines)
	dir := filepath.Dir(crashLogPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(crashLogPath, []byte(text), 0o644)
}

// CreatePane creates a new pane and runs cmd inside it.
func (t *TmuxBackend) CreatePane(cmd string, cwd string, direction string, percent int, parentPane string) (string, error) {
	cmd = strings.TrimSpace(cmd)
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		cwd = "."
	}

	base := strings.TrimSpace(parentPane)
	if base == "" {
		currentPane, err := t.GetCurrentPaneID()
		if err == nil {
			base = currentPane
		}
	}

	if base != "" {
		newPane, err := t.SplitPane(base, direction, percent)
		if err != nil {
			return "", err
		}
		if cmd != "" {
			if err := t.RespawnPane(newPane, cmd, cwd, "", true); err != nil {
				return newPane, err
			}
		}
		return newPane, nil
	}

	// Outside tmux: create a new detached tmux session as root container.
	sessionName := fmt.Sprintf("curdx-%s-%d-%d", filepath.Base(cwd), int(time.Now().Unix())%100000, os.Getpid())
	if _, err := t.tmuxRun([]string{"new-session", "-d", "-s", sessionName, "-c", cwd}, true, false, nil, 0); err != nil {
		return "", err
	}
	result, err := t.tmuxRun([]string{"list-panes", "-t", sessionName, "-F", "#{pane_id}"}, true, true, nil, 0)
	if err != nil {
		return "", err
	}
	stdout := strings.TrimSpace(result.Stdout)
	if stdout == "" {
		return "", fmt.Errorf("tmux failed to resolve root pane_id for session %q", sessionName)
	}
	paneID := strings.TrimSpace(strings.Split(stdout, "\n")[0])
	if !LooksLikePaneID(paneID) {
		return "", fmt.Errorf("tmux failed to resolve root pane_id for session %q", sessionName)
	}
	if cmd != "" {
		if err := t.RespawnPane(paneID, cmd, cwd, "", true); err != nil {
			return paneID, err
		}
	}
	return paneID, nil
}

// shellQuote quotes a string for shell use.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	// Simple check: if no special chars, return as-is
	safe := true
	for _, c := range s {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '/' || c == '.' || c == '_' || c == '-' || c == '=' || c == ':') {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

// shellJoin joins arguments with shell quoting.
func shellJoin(args []string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = shellQuote(a)
	}
	return strings.Join(quoted, " ")
}

// splitShellArgs splits a string into shell arguments, respecting single and double quotes.
func splitShellArgs(s string) []string {
	var args []string
	var current strings.Builder
	inSingle := false
	inDouble := false
	escaped := false

	for _, r := range s {
		if escaped {
			current.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' && !inSingle {
			escaped = true
			continue
		}
		if r == '\'' && !inDouble {
			inSingle = !inSingle
			continue
		}
		if r == '"' && !inSingle {
			inDouble = !inDouble
			continue
		}
		if r == ' ' || r == '\t' {
			if inSingle || inDouble {
				current.WriteRune(r)
			} else if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
			continue
		}
		current.WriteRune(r)
	}
	if current.Len() > 0 {
		args = append(args, current.String())
	}
	return args
}

// PaneBelongsToCWD checks if a tmux pane's working directory matches the given workDir.
func (t *TmuxBackend) PaneBelongsToCWD(paneID, workDir string) bool {
	result, err := t.tmuxRun([]string{"display-message", "-t", paneID, "-p", "#{pane_current_path}"}, false, true, nil, 1.0)
	if err != nil || result.ReturnCode != 0 {
		return true // assume true if we can't check
	}
	paneCwd := strings.TrimSpace(result.Stdout)
	if paneCwd == "" {
		return true
	}
	return paneCwd == workDir || strings.HasPrefix(paneCwd, workDir+"/")
}

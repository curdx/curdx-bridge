// Package terminal provides terminal multiplexer backends (tmux, wezterm)
// for creating and managing panes.
// Source: claude_code_bridge/lib/terminal.py
package terminal

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
)

// TerminalBackend abstracts pane-oriented terminal multiplexers.
type TerminalBackend interface {
	SendText(paneID string, text string) error
	IsAlive(paneID string) bool
	KillPane(paneID string)
	Activate(paneID string)
	CreatePane(cmd string, cwd string, direction string, percent int, parentPane string) (string, error)
}

// IsWindows returns true on Windows.
func IsWindows() bool {
	return runtime.GOOS == "windows"
}

// IsWSL returns true if running inside Windows Subsystem for Linux.
func IsWSL() bool {
	data, err := os.ReadFile("/proc/version")
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(data)), "microsoft")
}

// DefaultShell returns the default shell and its flag for command execution.
func DefaultShell() (string, string) {
	if IsWSL() {
		return "bash", "-c"
	}
	if IsWindows() {
		for _, shell := range []string{"pwsh", "powershell"} {
			if p, _ := exec.LookPath(shell); p != "" {
				return shell, "-Command"
			}
		}
		return "powershell", "-Command"
	}
	return "bash", "-c"
}

// GetShellType returns "bash" or "powershell" for the current environment.
func GetShellType() string {
	if IsWindows() && strings.EqualFold(os.Getenv("CURDX_BACKEND_ENV"), "wsl") {
		return "bash"
	}
	shell, _ := DefaultShell()
	if shell == "pwsh" || shell == "powershell" {
		return "powershell"
	}
	return "bash"
}

// SanitizeFilename replaces non-alphanumeric chars (except . _ -) with underscore.
func SanitizeFilename(value string) string {
	text := strings.TrimSpace(value)
	if text == "" {
		return ""
	}
	re := regexp.MustCompile(`[^A-Za-z0-9._-]+`)
	return strings.Trim(re.ReplaceAllString(text, "_"), "_")
}

// EnvFloat reads a float environment variable, returning default on error.
// Clamps to 0.0 minimum.
func EnvFloat(name string, defaultVal float64) float64 {
	raw, ok := os.LookupEnv(name)
	if !ok {
		return defaultVal
	}
	var value float64
	_, err := fmt.Sscanf(raw, "%f", &value)
	if err != nil {
		return defaultVal
	}
	if value < 0 {
		return 0
	}
	return value
}

// EnvInt reads an int environment variable, returning default on error.
func EnvInt(name string, defaultVal int) int {
	raw, ok := os.LookupEnv(name)
	if !ok || raw == "" {
		return defaultVal
	}
	var value int
	_, err := fmt.Sscanf(raw, "%d", &value)
	if err != nil {
		return defaultVal
	}
	return value
}

// currentTTY returns the tty name for the first available fd (0,1,2), or "".
func currentTTY() string {
	// On non-Unix platforms, this always returns "".
	return currentTTYPlatform()
}

// insideTmux returns true if the current process is running inside a tmux pane.
func insideTmux() bool {
	tmuxEnv := os.Getenv("TMUX")
	tmuxPane := os.Getenv("TMUX_PANE")
	if tmuxEnv == "" && tmuxPane == "" {
		return false
	}
	if p, _ := exec.LookPath("tmux"); p == "" {
		return false
	}

	tty := currentTTY()
	pane := strings.TrimSpace(tmuxPane)

	if pane != "" {
		out, err := runCapture("tmux", "display-message", "-p", "-t", pane, "#{pane_tty}")
		if err == nil {
			paneTTY := strings.TrimSpace(out)
			if tty != "" && paneTTY == tty {
				return true
			}
		}
	}

	if tty != "" {
		out, err := runCapture("tmux", "display-message", "-p", "#{client_tty}")
		if err == nil {
			clientTTY := strings.TrimSpace(out)
			if clientTTY == tty {
				return true
			}
		}
	}

	if tty == "" && pane != "" {
		out, err := runCapture("tmux", "display-message", "-p", "-t", pane, "#{pane_id}")
		if err == nil {
			paneID := strings.TrimSpace(out)
			if strings.HasPrefix(paneID, "%") {
				return true
			}
		}
	}

	return false
}

// insideWezterm returns true if WEZTERM_PANE is set.
func insideWezterm() bool {
	return strings.TrimSpace(os.Getenv("WEZTERM_PANE")) != ""
}

// DetectTerminal returns "tmux", "wezterm", or "" based on the current environment.
func DetectTerminal() string {
	if insideTmux() {
		return "tmux"
	}
	if insideWezterm() {
		return "wezterm"
	}
	return ""
}

var (
	backendCache   TerminalBackend
	backendCacheMu sync.Mutex
)

// GetBackend returns a cached backend based on detected terminal type.
func GetBackend(terminalType string) TerminalBackend {
	backendCacheMu.Lock()
	defer backendCacheMu.Unlock()
	if backendCache != nil {
		return backendCache
	}
	t := terminalType
	if t == "" {
		t = DetectTerminal()
	}
	switch t {
	case "wezterm":
		backendCache = NewWeztermBackend()
	case "tmux":
		backendCache = NewTmuxBackend("")
	}
	return backendCache
}

// ResetBackendCache clears the cached backend (useful for testing).
func ResetBackendCache() {
	backendCacheMu.Lock()
	defer backendCacheMu.Unlock()
	backendCache = nil
}

// defaultBackendKind returns the terminal backend to assume when session data
// doesn't specify one. Windows native has no tmux, so we fall back to wezterm.
func defaultBackendKind() string {
	if IsWindows() && !IsWSL() {
		return "wezterm"
	}
	return "tmux"
}

// GetBackendForSession returns a backend based on session data.
func GetBackendForSession(sessionData map[string]interface{}) TerminalBackend {
	terminal, _ := sessionData["terminal"].(string)
	if terminal == "" {
		terminal = defaultBackendKind()
	}
	if terminal == "wezterm" {
		return NewWeztermBackend()
	}
	return NewTmuxBackend("")
}

// GetPaneIDFromSession extracts pane ID from session data.
func GetPaneIDFromSession(sessionData map[string]interface{}) string {
	terminal, _ := sessionData["terminal"].(string)
	if terminal == "" {
		terminal = defaultBackendKind()
	}
	if terminal == "wezterm" {
		id, _ := sessionData["pane_id"].(string)
		return id
	}
	// tmux legacy: older session files used tmux_session as a pseudo pane_id.
	id, _ := sessionData["pane_id"].(string)
	if id != "" {
		return id
	}
	id, _ = sessionData["tmux_session"].(string)
	return id
}

// runCapture is a helper that runs a command and returns stdout.
func runCapture(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.Output()
	return string(out), err
}

// chooseWeztermCLICWD picks a safe cwd for launching Windows wezterm.exe from WSL.
func chooseWeztermCLICWD() string {
	override := strings.TrimSpace(os.Getenv("CURDX_WEZTERM_CLI_CWD"))
	candidates := []string{}
	if override != "" {
		candidates = append(candidates, override)
	}
	candidates = append(candidates, "/mnt/c", "/mnt/d", "/mnt")
	for _, c := range candidates {
		if c == "" {
			continue
		}
		info, err := os.Stat(c)
		if err == nil && info.IsDir() {
			return c
		}
	}
	return ""
}

// extractWSLPathFromUNCLikePath converts UNC-like WSL paths to WSL-internal absolute paths.
func extractWSLPathFromUNCLikePath(raw string) string {
	if raw == "" {
		return ""
	}
	// Match patterns like:
	//   /wsl.localhost/Ubuntu-24.04/home/user/...
	//   \\wsl.localhost\Ubuntu-24.04\home\user\...
	//   /wsl$/Ubuntu-24.04/home/user/...
	re := regexp.MustCompile(`(?i)^[/\\]{1,2}(?:wsl\.localhost|wsl\$)[/\\]([^/\\]+)(.*)$`)
	m := re.FindStringSubmatch(raw)
	if m == nil {
		return ""
	}
	remainder := strings.ReplaceAll(m[2], "\\", "/")
	if remainder == "" {
		return "/"
	}
	if !strings.HasPrefix(remainder, "/") {
		remainder = "/" + remainder
	}
	return remainder
}

// loadCachedWeztermBin loads a cached WezTerm path from installation config files.
func loadCachedWeztermBin() string {
	var candidates []string

	xdg := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME"))
	if xdg != "" {
		candidates = append(candidates, filepath.Join(xdg, "curdx", "env"))
	}

	if IsWindows() {
		localAppData := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
		if localAppData != "" {
			candidates = append(candidates, filepath.Join(localAppData, "curdx", "env"))
		}
		appData := strings.TrimSpace(os.Getenv("APPDATA"))
		if appData != "" {
			candidates = append(candidates, filepath.Join(appData, "curdx", "env"))
		}
	}

	home, _ := os.UserHomeDir()
	if home != "" {
		candidates = append(candidates, filepath.Join(home, ".config", "curdx", "env"))
	}

	for _, configPath := range candidates {
		data, err := os.ReadFile(configPath)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "CODEX_WEZTERM_BIN=") {
				path := strings.TrimSpace(strings.SplitN(line, "=", 2)[1])
				if path != "" {
					if _, err := os.Stat(path); err == nil {
						return path
					}
				}
			}
		}
	}
	return ""
}

var (
	cachedWeztermBin   string
	cachedWeztermBinMu sync.Mutex
)

// getWeztermBin returns the WezTerm binary path with caching.
func getWeztermBin() string {
	cachedWeztermBinMu.Lock()
	defer cachedWeztermBinMu.Unlock()

	if cachedWeztermBin != "" {
		return cachedWeztermBin
	}

	// Priority: env var > install cache > PATH > hardcoded paths
	override := os.Getenv("CODEX_WEZTERM_BIN")
	if override == "" {
		override = os.Getenv("WEZTERM_BIN")
	}
	if override != "" {
		if _, err := os.Stat(override); err == nil {
			cachedWeztermBin = override
			return override
		}
	}

	cached := loadCachedWeztermBin()
	if cached != "" {
		cachedWeztermBin = cached
		return cached
	}

	if found, _ := exec.LookPath("wezterm"); found != "" {
		cachedWeztermBin = found
		return found
	}
	if found, _ := exec.LookPath("wezterm.exe"); found != "" {
		cachedWeztermBin = found
		return found
	}

	if IsWSL() {
		for _, drive := range "cdefghijklmnopqrstuvwxyz" {
			for _, path := range []string{
				fmt.Sprintf("/mnt/%c/Program Files/WezTerm/wezterm.exe", drive),
				fmt.Sprintf("/mnt/%c/Program Files (x86)/WezTerm/wezterm.exe", drive),
			} {
				if _, err := os.Stat(path); err == nil {
					cachedWeztermBin = path
					return path
				}
			}
		}
	}

	return ""
}

// isWindowsWezterm detects if WezTerm is running on Windows.
func isWindowsWezterm() bool {
	override := os.Getenv("CODEX_WEZTERM_BIN")
	if override == "" {
		override = os.Getenv("WEZTERM_BIN")
	}
	if override != "" {
		lower := strings.ToLower(override)
		if strings.Contains(lower, ".exe") || strings.Contains(override, "/mnt/") {
			return true
		}
	}
	if p, _ := exec.LookPath("wezterm.exe"); p != "" {
		return true
	}
	if IsWSL() {
		for _, drive := range "cdefghijklmnopqrstuvwxyz" {
			for _, path := range []string{
				fmt.Sprintf("/mnt/%c/Program Files/WezTerm/wezterm.exe", drive),
				fmt.Sprintf("/mnt/%c/Program Files (x86)/WezTerm/wezterm.exe", drive),
			} {
				if _, err := os.Stat(path); err == nil {
					return true
				}
			}
		}
	}
	return false
}

// resetWeztermBinCache clears the cached wezterm binary path (for testing).
func resetWeztermBinCache() {
	cachedWeztermBinMu.Lock()
	defer cachedWeztermBinMu.Unlock()
	cachedWeztermBin = ""
}

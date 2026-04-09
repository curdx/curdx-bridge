package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/anthropics/curdx-bridge/internal/envutil"
	"github.com/anthropics/curdx-bridge/internal/paneregistry"
	"github.com/anthropics/curdx-bridge/internal/processlock"
	"github.com/anthropics/curdx-bridge/internal/projectid"
	"github.com/anthropics/curdx-bridge/internal/rpc"
	rtpkg "github.com/anthropics/curdx-bridge/internal/runtime"
	"github.com/anthropics/curdx-bridge/internal/sessionutil"
	"github.com/anthropics/curdx-bridge/internal/startconfig"
	"github.com/anthropics/curdx-bridge/internal/terminal"
)

const (
	Version   = "5.2.9"
	GitCommit = "c539e79"
	GitDate   = "2026-02-25"
)

var allowedProviders = map[string]bool{
	"codex": true, "gemini": true, "opencode": true, "claude": true,
}

// splitProviderTokens splits comma-separated and/or space-separated provider tokens.
func splitProviderTokens(values []string) []string {
	var parts []string
	for _, item := range values {
		for _, part := range strings.Split(item, ",") {
			p := strings.TrimSpace(strings.ToLower(part))
			if p != "" {
				parts = append(parts, p)
			}
		}
	}
	return parts
}

// parseProviders parses and validates provider names from argv.
func parseProviders(values []string, allowUnknown bool) []string {
	rawParts := splitProviderTokens(values)
	if len(rawParts) == 0 {
		return nil
	}

	seen := map[string]bool{}
	var parsed, unknown []string
	for _, p := range rawParts {
		if seen[p] {
			continue
		}
		seen[p] = true
		if allowedProviders[p] || allowUnknown {
			parsed = append(parsed, p)
		} else {
			unknown = append(unknown, p)
		}
	}

	if len(unknown) > 0 && !allowUnknown {
		fmt.Fprintf(os.Stderr, "invalid provider(s): %s\n", strings.Join(unknown, ", "))
		fmt.Fprintln(os.Stderr, "use: ccb codex gemini opencode claude  (spaces)  or  ccb codex,gemini,opencode,claude  (commas)")
		fmt.Fprintln(os.Stderr, "allowed: codex, gemini, opencode, claude")
		return nil
	}

	return parsed
}

// parseProvidersWithCmd parses providers and extracts "cmd" as a special flag.
func parseProvidersWithCmd(values []string) ([]string, bool) {
	rawParts := splitProviderTokens(values)
	if len(rawParts) == 0 {
		return nil, false
	}

	seen := map[string]bool{}
	var parsed, unknown []string
	cmdEnabled := false

	for _, p := range rawParts {
		if p == "cmd" {
			cmdEnabled = true
			continue
		}
		if seen[p] {
			continue
		}
		seen[p] = true
		if allowedProviders[p] {
			parsed = append(parsed, p)
		} else {
			unknown = append(unknown, p)
		}
	}

	if len(unknown) > 0 {
		fmt.Fprintf(os.Stderr, "invalid provider(s): %s\n", strings.Join(unknown, ", "))
		fmt.Fprintln(os.Stderr, "use: ccb codex gemini opencode claude cmd  (spaces)  or  ccb codex,gemini,opencode,claude,cmd  (commas)")
		fmt.Fprintln(os.Stderr, "allowed: codex, gemini, opencode, claude, cmd")
		return nil, cmdEnabled
	}

	return parsed, cmdEnabled
}

// isPIDAlive checks if a process with the given PID is running.
func isPIDAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	if errors.Is(err, syscall.EPERM) {
		return true // process exists but belongs to another user
	}
	return err == nil
}

// shortProjectID returns a truncated project ID for display (up to 8 chars).
func shortProjectID(id string) string {
	if len(id) >= 8 {
		return id[:8]
	}
	return id
}

// getVersionInfo reads version info from an install directory.
func getVersionInfo(dirPath string) map[string]string {
	info := map[string]string{"commit": "", "date": "", "version": ""}
	ccbFile := filepath.Join(dirPath, "ccb")
	data, err := os.ReadFile(ccbFile)
	if err == nil {
		lines := strings.Split(string(data), "\n")
		limit := len(lines)
		if limit > 60 {
			limit = 60
		}
		for _, line := range lines[:limit] {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "VERSION") && strings.Contains(line, "=") {
				parts := strings.SplitN(line, "=", 2)
				info["version"] = strings.Trim(strings.TrimSpace(parts[1]), "\"'")
			} else if strings.HasPrefix(line, "GIT_COMMIT") && strings.Contains(line, "=") {
				parts := strings.SplitN(line, "=", 2)
				val := strings.Trim(strings.TrimSpace(parts[1]), "\"'")
				if val != "" {
					info["commit"] = val
				}
			} else if strings.HasPrefix(line, "GIT_DATE") && strings.Contains(line, "=") {
				parts := strings.SplitN(line, "=", 2)
				val := strings.Trim(strings.TrimSpace(parts[1]), "\"'")
				if val != "" {
					info["date"] = val
				}
			}
		}
	}

	gitPath := filepath.Join(dirPath, ".git")
	if _, err := exec.LookPath("git"); err == nil {
		if _, err := os.Stat(gitPath); err == nil {
			result, err := exec.Command("git", "-C", dirPath, "log", "-1", "--format=%h|%ci").Output()
			if err == nil {
				parts := strings.SplitN(strings.TrimSpace(string(result)), "|", 2)
				if len(parts) >= 2 {
					info["commit"] = parts[0]
					info["date"] = strings.Fields(parts[1])[0]
				}
			}
		}
	}
	return info
}

// formatVersionInfo formats version info for display.
func formatVersionInfo(info map[string]string) string {
	var parts []string
	if info["version"] != "" {
		parts = append(parts, "v"+info["version"])
	}
	if info["commit"] != "" {
		parts = append(parts, info["commit"])
	}
	if info["date"] != "" {
		parts = append(parts, info["date"])
	}
	if len(parts) == 0 {
		return "unknown"
	}
	return strings.Join(parts, " ")
}

// findInstallDir locates the CCB installation directory.
func findInstallDir() string {
	selfPath, _ := os.Executable()
	var scriptRoot string
	if selfPath != "" {
		scriptRoot = filepath.Dir(selfPath)
	}

	candidates := []string{scriptRoot}
	if prefix := os.Getenv("CODEX_INSTALL_PREFIX"); prefix != "" {
		expanded := prefix
		if strings.HasPrefix(expanded, "~/") {
			home, _ := os.UserHomeDir()
			if home != "" {
				expanded = filepath.Join(home, expanded[2:])
			}
		}
		candidates = append(candidates, expanded)
	}
	home, _ := os.UserHomeDir()
	if home != "" {
		candidates = append(candidates, filepath.Join(home, ".local", "share", "codex-dual"))
	}

	if runtime.GOOS == "windows" {
		localAppData := os.Getenv("LOCALAPPDATA")
		if localAppData != "" {
			candidates = append(candidates,
				filepath.Join(localAppData, "codex-dual"),
				filepath.Join(localAppData, "claude-code-bridge"))
		}
	}

	for _, c := range candidates {
		if c == "" {
			continue
		}
		ccb := filepath.Join(c, "ccb")
		if _, err := os.Stat(ccb); err == nil {
			return c
		}
	}
	if scriptRoot != "" {
		return scriptRoot
	}
	return "."
}

// cmdVersion implements "ccb version".
func cmdVersion() int {
	installDir := findInstallDir()
	localInfo := getVersionInfo(installDir)
	localStr := formatVersionInfo(localInfo)

	fmt.Printf("ccb (Claude Code Bridge) %s\n", localStr)
	fmt.Printf("Install path: %s\n", installDir)
	fmt.Println("\nChecking for updates...")

	// Try to get remote version info
	remoteInfo := getRemoteVersionInfo()
	if remoteInfo == nil {
		fmt.Println("Unable to check for updates (network error)")
	} else if localInfo["commit"] != "" && remoteInfo["commit"] != "" {
		if localInfo["commit"] == remoteInfo["commit"] {
			fmt.Println("Up to date")
		} else {
			remoteStr := remoteInfo["commit"]
			if remoteInfo["date"] != "" {
				remoteStr += " " + remoteInfo["date"]
			}
			fmt.Printf("Update available: %s\n", remoteStr)
			fmt.Println("   Run: ccb update")
		}
	} else {
		fmt.Println("Unable to compare versions")
	}
	return 0
}

// getRemoteVersionInfo fetches latest version info from GitHub.
func getRemoteVersionInfo() map[string]string {
	apiURL := "https://api.github.com/repos/bfly123/claude_code_bridge/commits/main"

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(apiURL)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil
	}
	out, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1MB limit
	if err != nil {
		return nil
	}
	var data map[string]interface{}
	if json.Unmarshal(out, &data) != nil {
		return nil
	}
	sha, _ := data["sha"].(string)
	commit := ""
	if len(sha) >= 7 {
		commit = sha[:7]
	}
	date := ""
	if commitObj, ok := data["commit"].(map[string]interface{}); ok {
		if committer, ok := commitObj["committer"].(map[string]interface{}); ok {
			if dateStr, ok := committer["date"].(string); ok && len(dateStr) >= 10 {
				date = dateStr[:10]
			}
		}
	}
	return map[string]string{"commit": commit, "date": date}
}

// cmdUpdate implements "ccb update [version]".
func cmdUpdate(target string) int {
	installDir := findInstallDir()
	oldInfo := getVersionInfo(installDir)

	if target != "" {
		targetSpec := strings.TrimPrefix(target, "v")
		matched, _ := regexp.MatchString(`^\d+(\.\d+)*$`, targetSpec)
		if !matched {
			fmt.Fprintf(os.Stderr, "Invalid version format: %s\n", target)
			fmt.Fprintln(os.Stderr, "   Examples: ccb update 4, ccb update 4.1, ccb update 4.1.3")
			return 1
		}
		fmt.Printf("Looking for version matching: %s\n", targetSpec)
	}

	if target != "" {
		fmt.Printf("Updating to %s...\n", target)
	} else {
		fmt.Println("Checking for updates...")
	}

	// Method 1: git pull
	gitPath := filepath.Join(installDir, ".git")
	if _, err := exec.LookPath("git"); err == nil {
		if _, err := os.Stat(gitPath); err == nil {
			if target != "" {
				targetSpec := strings.TrimPrefix(target, "v")
				fmt.Printf("Switching to v%s via git...\n", targetSpec)
				exec.Command("git", "-C", installDir, "fetch", "--tags", "--force").Run()
				result := exec.Command("git", "-C", installDir, "checkout", "v"+targetSpec)
				out, err := result.CombinedOutput()
				if err == nil {
					fmt.Println(strings.TrimSpace(string(out)))
					runInstaller(installDir)
					showUpgradeInfo(installDir, oldInfo)
					return 0
				}
				fmt.Fprintf(os.Stderr, "Git checkout failed: %s\n", strings.TrimSpace(string(out)))
				fmt.Println("Falling back to tarball download...")
			} else {
				fmt.Println("Updating via git pull...")
				result := exec.Command("git", "-C", installDir, "pull", "--ff-only")
				out, err := result.CombinedOutput()
				if err == nil {
					output := strings.TrimSpace(string(out))
					if output != "" {
						fmt.Println(output)
					} else {
						fmt.Println("Already up to date.")
					}
					runInstaller(installDir)
					showUpgradeInfo(installDir, oldInfo)
					return 0
				}
				fmt.Fprintf(os.Stderr, "Git pull failed: %s\n", strings.TrimSpace(string(out)))
				fmt.Println("Falling back to tarball download...")
			}
		}
	}

	// Method 2: tarball download
	repoURL := "https://github.com/bfly123/claude_code_bridge"
	var tarballURL, extractedName string
	if target != "" {
		targetSpec := strings.TrimPrefix(target, "v")
		tarballURL = fmt.Sprintf("%s/archive/refs/tags/v%s.tar.gz", repoURL, targetSpec)
		extractedName = "claude_code_bridge-" + targetSpec
	} else {
		tarballURL = repoURL + "/archive/refs/heads/main.tar.gz"
		extractedName = "claude_code_bridge-main"
	}

	tmpDir := filepath.Join(os.TempDir(), "ccb_update")
	os.RemoveAll(tmpDir)
	os.MkdirAll(tmpDir, 0o755)
	defer os.RemoveAll(tmpDir)

	tarballPath := filepath.Join(tmpDir, "main.tar.gz")

	if target != "" {
		fmt.Printf("Downloading v%s...\n", strings.TrimPrefix(target, "v"))
	} else {
		fmt.Println("Downloading latest version...")
	}

	downloaded := false
	if _, err := exec.LookPath("curl"); err == nil {
		result := exec.Command("curl", "-fsSL", "-o", tarballPath, tarballURL)
		if err := result.Run(); err == nil {
			downloaded = true
		}
	}
	if !downloaded {
		if _, err := exec.LookPath("wget"); err == nil {
			result := exec.Command("wget", "-q", "-O", tarballPath, tarballURL)
			if err := result.Run(); err == nil {
				downloaded = true
			}
		}
	}
	if !downloaded {
		fmt.Fprintln(os.Stderr, "Download failed (need curl or wget)")
		return 1
	}

	fmt.Println("Extracting...")
	tarCmd := exec.Command("tar", "xzf", tarballPath, "-C", tmpDir, "--no-same-owner")
	if err := tarCmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Extraction failed: %v\n", err)
		return 1
	}

	// Verify extraction stayed within tmpDir (path traversal protection).
	extractedDir := filepath.Join(tmpDir, extractedName)
	realExtracted, err := filepath.EvalSymlinks(extractedDir)
	if err != nil || !strings.HasPrefix(realExtracted, tmpDir) {
		fmt.Fprintln(os.Stderr, "Extraction failed: path traversal detected")
		return 1
	}
	fmt.Println("Installing...")
	env := os.Environ()
	env = append(env, "CODEX_INSTALL_PREFIX="+installDir)
	env = append(env, "CCB_CLEAN_INSTALL=1")

	installerPath := filepath.Join(extractedDir, "install.sh")
	installCmd := exec.Command(installerPath, "install")
	installCmd.Env = env
	installCmd.Stdout = os.Stdout
	installCmd.Stderr = os.Stderr
	if err := installCmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Update failed: %v\n", err)
		return 1
	}

	showUpgradeInfo(installDir, oldInfo)
	return 0
}

func runInstaller(installDir string) {
	env := os.Environ()
	env = append(env, "CCB_CLEAN_INSTALL=1")
	installerPath := filepath.Join(installDir, "install.sh")
	if _, err := os.Stat(installerPath); err != nil {
		return
	}
	fmt.Println("Reinstalling...")
	installCmd := exec.Command(installerPath, "install")
	installCmd.Env = env
	installCmd.Stdout = os.Stdout
	installCmd.Stderr = os.Stderr
	_ = installCmd.Run()
}

func showUpgradeInfo(installDir string, oldInfo map[string]string) {
	newInfo := getVersionInfo(installDir)
	oldStr := formatVersionInfo(oldInfo)
	newStr := formatVersionInfo(newInfo)
	if oldInfo["commit"] != newInfo["commit"] || oldInfo["version"] != newInfo["version"] {
		fmt.Printf("Updated: %s -> %s\n", oldStr, newStr)
	} else {
		fmt.Printf("Already up to date: %s\n", newStr)
	}
}

// findAllZombieSessions finds tmux sessions whose parent process is dead.
func findAllZombieSessions() []map[string]interface{} {
	if runtime.GOOS == "windows" {
		return nil
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		return nil
	}

	pattern := regexp.MustCompile(`^(codex|gemini|opencode|claude)-(\d+)-`)
	var zombies []map[string]interface{}

	out, err := exec.Command("tmux", "list-sessions", "-F", "#{session_name}").Output()
	if err != nil {
		return nil
	}

	for _, session := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		session = strings.TrimSpace(session)
		if session == "" {
			continue
		}
		m := pattern.FindStringSubmatch(session)
		if m == nil {
			continue
		}
		parentPID, err := strconv.Atoi(m[2])
		if err != nil {
			continue
		}
		if isPIDAlive(parentPID) {
			continue
		}
		zombies = append(zombies, map[string]interface{}{
			"session":    session,
			"provider":   m[1],
			"parent_pid": parentPID,
		})
	}
	return zombies
}

// killGlobalZombies cleans up zombie tmux sessions.
func killGlobalZombies(yes bool) int {
	zombies := findAllZombieSessions()
	if len(zombies) == 0 {
		fmt.Println("No zombie sessions found")
		return 0
	}

	fmt.Printf("Found %d zombie session(s):\n", len(zombies))
	for _, z := range zombies {
		fmt.Printf("  - %s (parent PID %v exited)\n", z["session"], z["parent_pid"])
	}

	if !yes {
		fmt.Print("\nClean up these sessions? [y/N] ")
		var reply string
		fmt.Scanln(&reply)
		if strings.ToLower(reply) != "y" {
			fmt.Println("Cancelled")
			return 1
		}
	}

	killed := 0
	failed := 0
	for _, z := range zombies {
		sessionName, _ := z["session"].(string)
		cmd := exec.Command("tmux", "kill-session", "-t", sessionName)
		if err := cmd.Run(); err != nil {
			failed++
		} else {
			killed++
		}
	}

	if failed > 0 {
		fmt.Printf("Cleaned up %d zombie session(s), %d failed\n", killed, failed)
	} else {
		fmt.Printf("Cleaned up %d zombie session(s)\n", killed)
	}
	return 0
}

// findDaemonPIDsByName finds PIDs of daemon processes.
func findDaemonPIDsByName(daemonName string) []int {
	var pids []int
	if runtime.GOOS == "windows" {
		return pids
	}
	out, err := exec.Command("pgrep", "-f", "bin/"+daemonName+"$").Output()
	if err != nil {
		return pids
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			if pid, err := strconv.Atoi(line); err == nil {
				pids = append(pids, pid)
			}
		}
	}
	return pids
}

// killPID kills a process by PID.
func killPID(pid int, force bool) bool {
	if pid <= 0 {
		return false
	}
	sig := syscall.SIGTERM
	if force {
		sig = syscall.SIGKILL
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(sig) == nil
}

// cmdKill implements "ccb kill".
func cmdKill(providerArgs []string, force, yes bool) int {
	if force {
		return killGlobalZombies(yes)
	}

	providers := parseProviders(providerArgs, true)
	if providers == nil {
		providers = []string{"codex", "gemini", "opencode", "claude"}
	}

	daemonSpecs := map[string]struct {
		protocolPrefix string
		daemonBinName  string
	}{
		"codex":    {"cask", "askd"},
		"gemini":   {"gask", "askd"},
		"opencode": {"oask", "askd"},
		"claude":   {"lask", "askd"},
	}

	cwd, _ := os.Getwd()
	if cwd == "" {
		cwd = "."
	}

	for _, provider := range providers {
		// 1. Kill UI sessions
		sessionFilename := fmt.Sprintf(".%s-session", provider)
		sessionFile := sessionutil.FindProjectSessionFile(cwd, sessionFilename)
		if sessionFile != "" {
			data, err := os.ReadFile(sessionFile)
			if err == nil {
				// Strip BOM
				if len(data) >= 3 && data[0] == 0xef && data[1] == 0xbb && data[2] == 0xbf {
					data = data[3:]
				}
				var sessionData map[string]interface{}
				if json.Unmarshal(data, &sessionData) == nil {
					terminalType, _ := sessionData["terminal"].(string)
					if terminalType == "" {
						terminalType = "tmux"
					}

					paneID := ""
					if terminalType == "wezterm" {
						paneID, _ = sessionData["pane_id"].(string)
					} else {
						paneID, _ = sessionData["pane_id"].(string)
						if paneID == "" {
							paneID, _ = sessionData["tmux_session"].(string)
						}
					}

					if terminalType == "wezterm" && paneID != "" {
						backend := terminal.NewWeztermBackend()
						backend.KillPane(paneID)
					} else if paneID != "" {
						if _, err := exec.LookPath("tmux"); err == nil {
							backend := terminal.NewTmuxBackend("")
							if strings.HasPrefix(paneID, "%") {
								backend.KillPane(paneID)
							} else {
								tmuxSession, _ := sessionData["tmux_session"].(string)
								tmuxSession = strings.TrimSpace(tmuxSession)
								if tmuxSession != "" && !strings.HasPrefix(tmuxSession, "%") {
									exec.Command("tmux", "kill-session", "-t", tmuxSession).Run()
									exec.Command("tmux", "kill-session", "-t", "launcher-"+tmuxSession).Run()
								} else {
									backend.KillPane(paneID)
								}
							}
						}
					}

					// Update session file
					sessionData["active"] = false
					sessionData["ended_at"] = time.Now().Format("2006-01-02 15:04:05")
					updatedJSON, err := json.MarshalIndent(sessionData, "", "  ")
					if err == nil {
						sessionutil.SafeWriteSession(sessionFile, string(updatedJSON))
					}
					fmt.Printf("%s session terminated\n", capitalizeFirst(provider))
				}
			}
		} else {
			fmt.Printf("%s: No active session file found\n", provider)
		}

		// 2. Kill background daemon
		if spec, ok := daemonSpecs[provider]; ok {
			stateFile := rtpkg.StateFilePath(spec.daemonBinName + ".json")
			if rpc.ShutdownDaemon(spec.protocolPrefix, 1.0, stateFile) {
				fmt.Printf("%s daemon shutdown requested\n", spec.daemonBinName)
			} else {
				st := rpc.ReadState(stateFile)
				if st != nil {
					if pidRaw, ok := st["pid"]; ok {
						pid := 0
						switch v := pidRaw.(type) {
						case float64:
							pid = int(v)
						case string:
							pid, _ = strconv.Atoi(v)
						}
						if pid > 0 {
							if killPID(pid, true) {
								fmt.Printf("%s daemon force killed (pid=%d)\n", spec.daemonBinName, pid)
							} else {
								fmt.Printf("%s daemon could not be killed (pid=%d)\n", spec.daemonBinName, pid)
							}
						}
					}
				}
			}
		}
	}

	return 0
}

// cmdUninstall implements "ccb uninstall".
func cmdUninstall() int {
	installDir := findInstallDir()
	installerPath := filepath.Join(installDir, "install.sh")
	if _, err := os.Stat(installerPath); err != nil {
		fmt.Fprintln(os.Stderr, "install.sh not found; cannot uninstall")
		return 1
	}
	cmd := exec.Command(installerPath, "uninstall")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Uninstall failed: %v\n", err)
		return 1
	}
	return 0
}

// cmdReinstall implements "ccb reinstall".
func cmdReinstall() int {
	installDir := findInstallDir()
	installerPath := filepath.Join(installDir, "install.sh")
	if _, err := os.Stat(installerPath); err != nil {
		fmt.Fprintln(os.Stderr, "install.sh not found; cannot reinstall")
		return 1
	}
	cmd := exec.Command(installerPath, "install")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Reinstall failed: %v\n", err)
		return 1
	}
	return 0
}

// isDangerousRoot checks if a path is $HOME or filesystem root.
func isDangerousRoot(cwd string) (bool, string) {
	resolved, err := filepath.Abs(cwd)
	if err != nil {
		resolved = cwd
	}
	if resolvedReal, err := filepath.EvalSymlinks(resolved); err == nil {
		resolved = resolvedReal
	}

	home, _ := os.UserHomeDir()
	if home != "" {
		homeResolved, err := filepath.EvalSymlinks(home)
		if err == nil {
			home = homeResolved
		}
		if resolved == home {
			return true, "$HOME"
		}
	}

	// Check filesystem root
	if resolved == "/" || resolved == filepath.VolumeName(resolved)+string(filepath.Separator) {
		return true, "filesystem root"
	}

	return false, ""
}

// findParentAnchorDir finds the nearest ancestor .ccb/ directory.
func findParentAnchorDir(cwd string) string {
	resolved, err := filepath.Abs(cwd)
	if err != nil {
		resolved = cwd
	}
	if resolvedReal, err := filepath.EvalSymlinks(resolved); err == nil {
		resolved = resolvedReal
	}

	current := resolved
	for {
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent

		var candidate string
		primary := filepath.Join(current, ".ccb")
		if info, err := os.Stat(primary); err == nil && info.IsDir() {
			candidate = primary
		} else {
			legacy := filepath.Join(current, ".ccb_config")
			if info, err := os.Stat(legacy); err == nil && info.IsDir() {
				candidate = legacy
			}
		}
		if candidate == "" {
			continue
		}
		// Ignore dangerous root anchors
		isDangerous, _ := isDangerousRoot(current)
		if isDangerous {
			continue
		}
		return candidate
	}
	return ""
}

// envTruthy checks if an env var is truthy.
func envTruthy(name string) bool {
	raw := os.Getenv(name)
	if raw == "" {
		return false
	}
	v := strings.ToLower(strings.TrimSpace(raw))
	return v == "1" || v == "true" || v == "yes" || v == "y" || v == "on"
}

// capitalizeFirst returns the string with the first character uppercased.
func capitalizeFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// -----------------------------------------------------------------------
// AILauncher — mirrors Python AILauncher
// -----------------------------------------------------------------------

// aiLauncher holds all state for a CCB launch session.
type aiLauncher struct {
	providers     []string
	resume        bool
	auto          bool
	cwd           string
	projectRoot   string
	sessionID     string
	ccbPID        int
	projectID     string
	projectRunDir string
	runtimeDir    string
	terminalType  string
	anchorProv    string
	anchorPaneID  string

	tmuxPanes   map[string]string
	weztermPanes map[string]string
	extraPanes  map[string]string

	cleaned    bool
	cleanedMu  sync.Mutex

	launchArgs map[string]interface{}
	launchEnv  map[string]interface{}
	cmdConfig  map[string]interface{}
}

func newAILauncher(providers []string, resume, auto bool, cmdConfig, launchArgs, launchEnv map[string]interface{}) *aiLauncher {
	cwd, _ := os.Getwd()
	if cwd == "" {
		cwd = "."
	}
	projectRoot, err := filepath.Abs(cwd)
	if err != nil {
		projectRoot = cwd
	}
	if resolved, err := filepath.EvalSymlinks(projectRoot); err == nil {
		projectRoot = resolved
	}
	pid := os.Getpid()
	sessionID := fmt.Sprintf("ai-%d-%d", time.Now().Unix(), pid)
	ccbProjectID := projectid.ComputeCCBProjectID(projectRoot)
	projectHash := ccbProjectID
	if len(projectHash) > 16 {
		projectHash = projectHash[:16]
	}
	if projectHash == "" {
		projectHash = "unknown"
	}
	home, _ := os.UserHomeDir()
	projectRunDir := filepath.Join(home, ".cache", "ccb", "projects", projectHash)
	_ = os.MkdirAll(projectRunDir, 0o755)

	runtimeDir := filepath.Join(os.TempDir(), fmt.Sprintf("claude-ai-%s", currentUser()), sessionID)
	_ = os.MkdirAll(runtimeDir, 0o755)

	termType := terminal.DetectTerminal()
	// Respect CCB_TERMINAL override
	forced := strings.TrimSpace(strings.ToLower(os.Getenv("CCB_TERMINAL")))
	if forced == "" {
		forced = strings.TrimSpace(strings.ToLower(os.Getenv("CODEX_TERMINAL")))
	}
	if forced == "wezterm" || forced == "tmux" {
		termType = forced
	}

	if launchArgs == nil {
		launchArgs = map[string]interface{}{}
	}
	if launchEnv == nil {
		launchEnv = map[string]interface{}{}
	}

	return &aiLauncher{
		providers:     providers,
		resume:        resume,
		auto:          auto,
		cwd:           cwd,
		projectRoot:   projectRoot,
		sessionID:     sessionID,
		ccbPID:        pid,
		projectID:     ccbProjectID,
		projectRunDir: projectRunDir,
		runtimeDir:    runtimeDir,
		terminalType:  termType,
		tmuxPanes:     make(map[string]string),
		weztermPanes:  make(map[string]string),
		extraPanes:    make(map[string]string),
		launchArgs:    launchArgs,
		launchEnv:     launchEnv,
		cmdConfig:     cmdConfig,
	}
}

func currentUser() string {
	user := os.Getenv("USER")
	if user == "" {
		user = os.Getenv("USERNAME")
	}
	if user == "" {
		user = "unknown"
	}
	return user
}

// providerEnvOverrides returns managed environment overrides for a provider pane.
func (l *aiLauncher) providerEnvOverrides(provider string) map[string]string {
	env := map[string]string{
		"CCB_MANAGED":    "1",
		"CCB_PARENT_PID": strconv.Itoa(l.ccbPID),
	}
	if v := os.Getenv("CCB_RUN_DIR"); v != "" {
		env["CCB_RUN_DIR"] = v
	}
	prov := strings.TrimSpace(strings.ToLower(provider))
	if prov != "" {
		env["CCB_CALLER"] = prov
	}
	// Merge per-provider launch_env
	if extra, ok := l.launchEnv[prov]; ok {
		if m, ok := extra.(map[string]interface{}); ok {
			for k, v := range m {
				env[strings.TrimSpace(k)] = fmt.Sprintf("%v", v)
			}
		}
	}
	return env
}

// validEnvKeyRE matches valid POSIX environment variable names.
var validEnvKeyRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// buildEnvPrefix builds shell export statements for environment overrides.
func (l *aiLauncher) buildEnvPrefix(env map[string]string) string {
	if len(env) == 0 {
		return ""
	}
	var parts []string
	for key, val := range env {
		if !validEnvKeyRE.MatchString(key) {
			continue // skip invalid env key to prevent shell injection
		}
		parts = append(parts, fmt.Sprintf("export %s=%s; ", key, shellQuote(val)))
	}
	sort.Strings(parts)
	return strings.Join(parts, "")
}

// buildExportPathCmd ensures CCB bin/ is on PATH inside the pane.
func (l *aiLauncher) buildExportPathCmd() string {
	selfPath, _ := os.Executable()
	if selfPath == "" {
		return ""
	}
	binDir := filepath.Dir(selfPath)
	currentPath := os.Getenv("PATH")
	if currentPath != "" {
		return fmt.Sprintf("export PATH=%s:%s; ", shellQuote(binDir), shellQuote(currentPath))
	}
	return fmt.Sprintf("export PATH=%s:$PATH; ", shellQuote(binDir))
}

// buildCdCmd builds a cd command.
func buildCdCmd(workDir string) string {
	return fmt.Sprintf("cd %s; ", shellQuote(workDir))
}

// buildPaneTitleCmd builds a pane title command.
func buildPaneTitleCmd(marker string) string {
	return fmt.Sprintf("printf '\\033]0;%s\\007'; ", marker)
}

// buildKeepOpenCmd wraps a command so the pane stays open on exit.
func buildKeepOpenCmd(provider, startCmd string) string {
	return fmt.Sprintf("%s; code=$?; echo; echo \"[%s] exited with code $code. Press Enter to close...\"; read -r _; exit $code",
		startCmd, provider)
}

// shellQuote quotes a string for shell use.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
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

// currentPaneID returns the current terminal pane ID.
func (l *aiLauncher) currentPaneID() string {
	if l.terminalType == "wezterm" {
		return strings.TrimSpace(os.Getenv("WEZTERM_PANE"))
	}
	backend := terminal.NewTmuxBackend("")
	id, err := backend.GetCurrentPaneID()
	if err != nil {
		return strings.TrimSpace(os.Getenv("TMUX_PANE"))
	}
	return id
}

// providerPaneID gets the pane ID for a provider.
func (l *aiLauncher) providerPaneID(provider string) string {
	prov := strings.TrimSpace(strings.ToLower(provider))
	if prov == l.anchorProv && l.anchorPaneID != "" {
		return l.anchorPaneID
	}
	if l.terminalType == "wezterm" {
		return l.weztermPanes[prov]
	}
	return l.tmuxPanes[prov]
}

// getStartCmd builds the start command for a provider.
func (l *aiLauncher) getStartCmd(provider string) string {
	var cmd string
	switch provider {
	case "codex":
		cmd = l.buildCodexStartCmd()
	case "gemini":
		cmd = l.buildGeminiStartCmd()
	case "opencode":
		cmd = l.buildOpenCodeStartCmd()
	case "claude":
		cmd = l.buildClaudeStartCmd()
	default:
		return ""
	}
	// Append per-provider launch_args
	if extra, ok := l.launchArgs[provider]; ok {
		if s, ok := extra.(string); ok && strings.TrimSpace(s) != "" {
			cmd = cmd + " " + strings.TrimSpace(s)
		}
	}
	return cmd
}

func (l *aiLauncher) buildCodexStartCmd() string {
	cmd := "codex -c disable_paste_burst=true"
	if l.auto {
		cmd += " -c trust_level=\"trusted\" -c approval_policy=\"never\" -c sandbox_mode=\"danger-full-access\""
	}
	if l.resume {
		fmt.Fprintf(os.Stderr, "No Codex session scan in Go build, starting fresh\n")
	}
	return cmd
}

func (l *aiLauncher) buildGeminiStartCmd() string {
	cmd := "gemini"
	if l.auto {
		cmd = "gemini --yolo"
	}
	if l.resume {
		cmd += " --resume latest"
		fmt.Fprintf(os.Stderr, "Resuming Gemini session\n")
	}
	return cmd
}

func (l *aiLauncher) buildOpenCodeStartCmd() string {
	cmd := strings.TrimSpace(os.Getenv("OPENCODE_START_CMD"))
	if cmd == "" {
		cmd = "opencode"
	}
	if l.resume {
		cmd += " --continue"
		fmt.Fprintf(os.Stderr, "Resuming OpenCode session\n")
	}
	return cmd
}

func (l *aiLauncher) buildClaudeStartCmd() string {
	claudeCmd := l.findClaudeCmd()
	if claudeCmd == "" {
		return ""
	}
	cmd := claudeCmd
	if l.auto {
		cmd += " --dangerously-skip-permissions"
	}
	if l.resume {
		cmd += " --continue"
		fmt.Fprintf(os.Stderr, "Resuming Claude session\n")
	}
	return cmd
}

func (l *aiLauncher) findClaudeCmd() string {
	if path, _ := exec.LookPath("claude"); path != "" {
		return path
	}
	return ""
}

// claudeEnvOverrides returns environment overrides for the Claude pane.
func (l *aiLauncher) claudeEnvOverrides() map[string]string {
	env := l.providerEnvOverrides("claude")
	runtimeBase := l.runtimeDir

	if contains(l.providers, "codex") {
		rt := filepath.Join(runtimeBase, "codex")
		env["CODEX_SESSION_ID"] = l.sessionID
		env["CODEX_RUNTIME_DIR"] = rt
		env["CODEX_INPUT_FIFO"] = filepath.Join(rt, "input.fifo")
		env["CODEX_OUTPUT_FIFO"] = filepath.Join(rt, "output.fifo")
		env["CODEX_TERMINAL"] = l.terminalType
		paneID := l.providerPaneID("codex")
		if l.terminalType == "wezterm" {
			env["CODEX_WEZTERM_PANE"] = paneID
		} else {
			env["CODEX_TMUX_SESSION"] = paneID
		}
	}
	if contains(l.providers, "gemini") {
		rt := filepath.Join(runtimeBase, "gemini")
		env["GEMINI_SESSION_ID"] = l.sessionID
		env["GEMINI_RUNTIME_DIR"] = rt
		env["GEMINI_TERMINAL"] = l.terminalType
		paneID := l.providerPaneID("gemini")
		if l.terminalType == "wezterm" {
			env["GEMINI_WEZTERM_PANE"] = paneID
		} else {
			env["GEMINI_TMUX_SESSION"] = paneID
		}
	}
	if contains(l.providers, "opencode") {
		rt := filepath.Join(runtimeBase, "opencode")
		env["OPENCODE_SESSION_ID"] = l.sessionID
		env["OPENCODE_RUNTIME_DIR"] = rt
		env["OPENCODE_TERMINAL"] = l.terminalType
		paneID := l.providerPaneID("opencode")
		if l.terminalType == "wezterm" {
			env["OPENCODE_WEZTERM_PANE"] = paneID
		} else {
			env["OPENCODE_TMUX_SESSION"] = paneID
		}
	}
	return env
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

// projectConfigDir returns the resolved project config dir.
func (l *aiLauncher) projectConfigDir() string {
	return sessionutil.ResolveProjectConfigDir(l.projectRoot)
}

// projectSessionFile returns the path for a project session file.
func (l *aiLauncher) projectSessionFile(filename string) string {
	return filepath.Join(l.projectConfigDir(), filename)
}

// writeProviderSession writes a session file for any provider.
func (l *aiLauncher) writeProviderSession(provider, paneID, paneTitleMarker, startCmd string) {
	sessionFilename := fmt.Sprintf(".%s-session", provider)
	sessionFile := l.projectSessionFile(sessionFilename)

	data := map[string]interface{}{
		"session_id":        l.sessionID,
		"ccb_session_id":    l.sessionID,
		"ccb_project_id":    l.projectID,
		"runtime_dir":       filepath.Join(l.runtimeDir, provider),
		"terminal":          l.terminalType,
		"pane_id":           paneID,
		"pane_title_marker": paneTitleMarker,
		"work_dir":          l.projectRoot,
		"work_dir_norm":     projectid.NormalizeWorkDir(l.projectRoot),
		"start_dir":         l.cwd,
		"active":            true,
		"started_at":        time.Now().Format("2006-01-02 15:04:05"),
	}
	if startCmd != "" {
		data["start_cmd"] = startCmd
	}
	if provider == "codex" && startCmd != "" {
		data["codex_start_cmd"] = startCmd
		data["input_fifo"] = filepath.Join(l.runtimeDir, "codex", "input.fifo")
		data["output_fifo"] = filepath.Join(l.runtimeDir, "codex", "output.fifo")
		data["tmux_log"] = filepath.Join(l.runtimeDir, "codex", "bridge_output.log")
	}

	payload, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return
	}
	sessionutil.SafeWriteSession(sessionFile, string(payload))

	// Update pane registry
	provEntry := map[string]interface{}{
		"pane_id":           paneID,
		"pane_title_marker": paneTitleMarker,
		"session_file":      sessionFile,
	}
	paneregistry.UpsertRegistry(map[string]interface{}{
		"ccb_session_id": l.sessionID,
		"ccb_project_id": l.projectID,
		"work_dir":       l.projectRoot,
		"terminal":       l.terminalType,
		"providers": map[string]interface{}{
			provider: provEntry,
		},
	})
}

// writeClaudeSession writes the .claude-session file.
func (l *aiLauncher) writeClaudeSession(paneID, paneTitleMarker string) {
	sessionFile := l.projectSessionFile(".claude-session")

	data := map[string]interface{}{
		"session_id":        l.sessionID,
		"ccb_project_id":    l.projectID,
		"work_dir":          l.projectRoot,
		"work_dir_norm":     projectid.NormalizeWorkDir(l.projectRoot),
		"start_dir":         l.cwd,
		"terminal":          l.terminalType,
		"active":            true,
		"started_at":        time.Now().Format("2006-01-02 15:04:05"),
		"updated_at":        time.Now().Format("2006-01-02 15:04:05"),
	}
	if paneID != "" {
		data["pane_id"] = paneID
	}
	if paneTitleMarker != "" {
		data["pane_title_marker"] = paneTitleMarker
	}

	payload, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return
	}
	sessionutil.SafeWriteSession(sessionFile, string(payload))

	if paneID != "" {
		paneregistry.UpsertRegistry(map[string]interface{}{
			"ccb_session_id": l.sessionID,
			"ccb_project_id": l.projectID,
			"work_dir":       l.projectRoot,
			"terminal":       l.terminalType,
			"providers": map[string]interface{}{
				"claude": map[string]interface{}{
					"pane_id":           paneID,
					"pane_title_marker": paneTitleMarker,
					"session_file":      sessionFile,
				},
			},
		})
	}
}

// startProviderInPane starts a provider in a new split pane (non-anchor provider).
func (l *aiLauncher) startProviderInPane(provider string, parentPane string, direction string) (string, error) {
	if l.terminalType == "" {
		return "", fmt.Errorf("no terminal backend detected")
	}

	paneTitleMarker := fmt.Sprintf("CCB-%s-%s", capitalizeFirst(provider), shortProjectID(l.projectID))
	startCmd := l.getStartCmd(provider)
	envOverrides := l.providerEnvOverrides(provider)
	if provider == "claude" {
		envOverrides = l.claudeEnvOverrides()
	}

	fullCmd := buildPaneTitleCmd(paneTitleMarker) +
		l.buildEnvPrefix(envOverrides) +
		l.buildExportPathCmd() +
		startCmd

	// For non-claude providers in wezterm, wrap with keep-open
	if provider != "claude" && l.terminalType == "wezterm" {
		keepOpen := envutil.EnvBool("CODEX_WEZTERM_KEEP_OPEN", true)
		if keepOpen {
			startCmdWrapped := l.buildEnvPrefix(envOverrides) +
				l.buildExportPathCmd() +
				l.getStartCmd(provider)
			fullCmd = buildPaneTitleCmd(paneTitleMarker) +
				l.buildEnvPrefix(envOverrides) +
				l.buildExportPathCmd() +
				buildKeepOpenCmd(provider, l.getStartCmd(provider))
			_ = startCmdWrapped
		}
	}

	var paneID string
	var err error

	if l.terminalType == "wezterm" {
		backend := terminal.NewWeztermBackend()
		useDirection := direction
		if useDirection == "" {
			if len(l.weztermPanes) == 0 {
				useDirection = "right"
			} else {
				useDirection = "bottom"
			}
		}
		paneID, err = backend.CreatePane(fullCmd, l.cwd, useDirection, 50, parentPane)
		if err != nil {
			return "", err
		}
		l.weztermPanes[provider] = paneID
	} else {
		// tmux mode
		backend := terminal.NewTmuxBackend("")
		useDirection := direction
		if useDirection == "" {
			if len(l.tmuxPanes) == 0 {
				useDirection = "right"
			} else {
				useDirection = "bottom"
			}
		}
		useParent := parentPane
		if useParent == "" {
			if p, err := backend.GetCurrentPaneID(); err == nil {
				useParent = p
			}
		}
		// If the preferred parent pane is dead, fall back to current pane
		if useParent != "" && strings.HasPrefix(useParent, "%") && !backend.PaneExists(useParent) {
			if p, err := backend.GetCurrentPaneID(); err == nil {
				useParent = p
			}
		}

		paneID, err = backend.CreatePane("", l.cwd, useDirection, 50, useParent)
		if err != nil {
			return "", err
		}
		backend.RespawnPane(paneID, fullCmd, l.cwd, "", true)
		backend.SetPaneTitle(paneID, paneTitleMarker)
		backend.SetPaneUserOption(paneID, "@ccb_agent", capitalizeFirst(provider))
		l.tmuxPanes[provider] = paneID
	}

	// Write session file
	if provider == "claude" {
		l.writeClaudeSession(paneID, paneTitleMarker)
	} else {
		l.writeProviderSession(provider, paneID, paneTitleMarker, startCmd)
	}

	fmt.Fprintf(os.Stderr, "%s started (%s pane: %s)\n",
		capitalizeFirst(provider), l.terminalType, paneID)
	return paneID, nil
}

// startCmdPane starts a shell pane for the "cmd" extra.
func (l *aiLauncher) startCmdPane(parentPane, direction string) (string, error) {
	title := "CCB-Cmd"
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "bash"
	}
	if _, err := exec.LookPath(shell); err != nil {
		shell = "bash"
	}

	fullCmd := buildPaneTitleCmd(title) +
		l.buildEnvPrefix(l.providerEnvOverrides("codex")) +
		l.buildExportPathCmd() +
		buildCdCmd(l.cwd) +
		shell

	useDirection := direction
	if useDirection == "" {
		useDirection = "right"
	}

	var paneID string
	var err error

	if l.terminalType == "wezterm" {
		backend := terminal.NewWeztermBackend()
		paneID, err = backend.CreatePane(fullCmd, l.cwd, useDirection, 50, parentPane)
		if err != nil {
			return "", err
		}
		l.extraPanes["cmd"] = paneID
	} else {
		backend := terminal.NewTmuxBackend("")
		paneID, err = backend.CreatePane("", l.cwd, useDirection, 50, parentPane)
		if err != nil {
			return "", err
		}
		backend.RespawnPane(paneID, fullCmd, l.cwd, "", true)
		backend.SetPaneTitle(paneID, title)
		backend.SetPaneUserOption(paneID, "@ccb_agent", "Cmd")
		l.extraPanes["cmd"] = paneID
	}

	fmt.Fprintf(os.Stderr, "Started cmd pane (%s)\n", paneID)
	return paneID, nil
}

// startProviderInCurrentPane runs the anchor provider in the current pane (blocking).
func (l *aiLauncher) startProviderInCurrentPane(provider string) int {
	if provider == "claude" {
		return l.startClaudeInCurrentPane()
	}
	return l.startGenericInCurrentPane(provider)
}

// startClaudeInCurrentPane starts Claude in the current pane.
func (l *aiLauncher) startClaudeInCurrentPane() int {
	fmt.Fprintf(os.Stderr, "Starting Claude...\n")
	env := l.buildClaudeEnv()

	claudeCmd := l.findClaudeCmd()
	if claudeCmd == "" {
		fmt.Fprintln(os.Stderr, "Claude CLI not found. Install: npm install -g @anthropic-ai/claude-code")
		return 1
	}

	cmdParts := []string{claudeCmd}
	if l.auto {
		cmdParts = append(cmdParts, "--dangerously-skip-permissions")
	}
	if l.resume {
		cmdParts = append(cmdParts, "--continue")
		fmt.Fprintf(os.Stderr, "Resuming Claude session\n")
	}
	if extra, ok := l.launchArgs["claude"]; ok {
		if s, ok := extra.(string); ok && strings.TrimSpace(s) != "" {
			cmdParts = append(cmdParts, strings.Fields(strings.TrimSpace(s))...)
		}
	}

	runCwd := l.projectRoot

	fmt.Fprintf(os.Stderr, "Session ID: %s\n", l.sessionID)
	fmt.Fprintf(os.Stderr, "Runtime dir: %s\n", l.runtimeDir)
	fmt.Fprintf(os.Stderr, "Active backends: %s\n", strings.Join(l.providers, ", "))
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Available commands:")
	if contains(l.providers, "codex") {
		fmt.Fprintln(os.Stderr, "   cask/cping/cpend - Codex communication")
	}
	if contains(l.providers, "gemini") {
		fmt.Fprintln(os.Stderr, "   gask/gping/gpend - Gemini communication")
	}
	if contains(l.providers, "opencode") {
		fmt.Fprintln(os.Stderr, "   oask/oping/opend - OpenCode communication")
	}
	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "Executing: %s\n", strings.Join(cmdParts, " "))

	cmd := exec.Command(cmdParts[0], cmdParts[1:]...)
	cmd.Env = mapToEnv(env)
	cmd.Dir = runCwd
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err := cmd.Run()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		}
		return 1
	}
	return 0
}

// startGenericInCurrentPane runs a non-Claude provider in the current pane.
func (l *aiLauncher) startGenericInCurrentPane(provider string) int {
	paneID := l.currentPaneID()
	paneTitleMarker := fmt.Sprintf("CCB-%s-%s", capitalizeFirst(provider), shortProjectID(l.projectID))

	if l.terminalType == "tmux" && paneID != "" {
		backend := terminal.NewTmuxBackend("")
		backend.SetPaneTitle(paneID, paneTitleMarker)
		backend.SetPaneUserOption(paneID, "@ccb_agent", capitalizeFirst(provider))
	}

	startCmd := l.getStartCmd(provider)
	envOverrides := l.providerEnvOverrides(provider)

	// Write session file
	l.writeProviderSession(provider, paneID, paneTitleMarker, startCmd)

	// Build full command with env prefix and PATH
	fullCmd := l.buildEnvPrefix(envOverrides) +
		l.buildExportPathCmd() +
		startCmd

	// Run via shell
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "bash"
	}
	if _, err := exec.LookPath(shell); err != nil {
		shell = "bash"
	}

	envMap := l.mergeEnv(envOverrides)

	// For opencode in tmux, try direct exec for single provider
	if provider == "opencode" && l.terminalType == "tmux" && len(l.providers) == 1 {
		cmdParts := strings.Fields(l.getStartCmd(provider))
		if len(cmdParts) > 0 {
			cmd := exec.Command(cmdParts[0], cmdParts[1:]...)
			cmd.Env = mapToEnv(envMap)
			cmd.Dir = l.cwd
			cmd.Stdin = os.Stdin
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				if exitErr, ok := err.(*exec.ExitError); ok {
					return exitErr.ExitCode()
				}
				return 1
			}
			return 0
		}
	}

	cmd := exec.Command(shell, "-lc", fullCmd)
	cmd.Dir = l.cwd
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		}
		return 1
	}
	return 0
}

func (l *aiLauncher) buildClaudeEnv() map[string]string {
	env := make(map[string]string)
	for _, kv := range os.Environ() {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) == 2 {
			env[parts[0]] = parts[1]
		}
	}
	// Add bin to PATH
	selfPath, _ := os.Executable()
	if selfPath != "" {
		binDir := filepath.Dir(selfPath)
		currentPath := env["PATH"]
		if !strings.Contains(currentPath, binDir) {
			env["PATH"] = binDir + ":" + currentPath
		}
	}
	// Merge claude overrides
	for k, v := range l.claudeEnvOverrides() {
		env[k] = v
	}
	return env
}

func (l *aiLauncher) mergeEnv(overrides map[string]string) map[string]string {
	env := make(map[string]string)
	for _, kv := range os.Environ() {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) == 2 {
			env[parts[0]] = parts[1]
		}
	}
	selfPath, _ := os.Executable()
	if selfPath != "" {
		binDir := filepath.Dir(selfPath)
		currentPath := env["PATH"]
		if !strings.Contains(currentPath, binDir) {
			env["PATH"] = binDir + ":" + currentPath
		}
	}
	for k, v := range overrides {
		env[k] = v
	}
	return env
}

func mapToEnv(m map[string]string) []string {
	var result []string
	for k, v := range m {
		result = append(result, k+"="+v)
	}
	return result
}

// setCurrentPaneLabel labels the anchor pane in tmux.
func (l *aiLauncher) setCurrentPaneLabel(provider string) {
	if l.terminalType != "tmux" {
		return
	}
	if os.Getenv("TMUX") == "" {
		return
	}
	backend := terminal.NewTmuxBackend("")
	paneID, err := backend.GetCurrentPaneID()
	if err != nil {
		return
	}
	title := fmt.Sprintf("CCB-%s-%s", capitalizeFirst(provider), shortProjectID(l.projectID))
	backend.SetPaneTitle(paneID, title)
	backend.SetPaneUserOption(paneID, "@ccb_agent", capitalizeFirst(provider))
}

// cleanup performs graceful shutdown.
func (l *aiLauncher) cleanup() {
	l.cleanedMu.Lock()
	if l.cleaned {
		l.cleanedMu.Unlock()
		return
	}
	l.cleaned = true
	l.cleanedMu.Unlock()

	fmt.Fprintf(os.Stderr, "\nCleaning up session resources...\n")

	// Kill spawned panes
	if l.terminalType == "wezterm" {
		backend := terminal.NewWeztermBackend()
		for _, paneID := range l.weztermPanes {
			if paneID != "" {
				backend.KillPane(paneID)
			}
		}
		for _, paneID := range l.extraPanes {
			if paneID != "" {
				backend.KillPane(paneID)
			}
		}
	} else {
		backend := terminal.NewTmuxBackend("")
		for _, paneID := range l.tmuxPanes {
			if paneID != "" {
				backend.KillPane(paneID)
			}
		}
		for _, paneID := range l.extraPanes {
			if paneID != "" {
				backend.KillPane(paneID)
			}
		}
	}

	// Mark session files inactive
	for _, prov := range []string{"codex", "gemini", "opencode", "claude"} {
		sessionFile := l.projectSessionFile(fmt.Sprintf(".%s-session", prov))
		if _, err := os.Stat(sessionFile); err != nil {
			continue
		}
		raw, err := os.ReadFile(sessionFile)
		if err != nil {
			continue
		}
		if len(raw) >= 3 && raw[0] == 0xef && raw[1] == 0xbb && raw[2] == 0xbf {
			raw = raw[3:]
		}
		var data map[string]interface{}
		if json.Unmarshal(raw, &data) != nil {
			continue
		}
		data["active"] = false
		data["ended_at"] = time.Now().Format("2006-01-02 15:04:05")
		payload, err := json.MarshalIndent(data, "", "  ")
		if err != nil {
			continue
		}
		sessionutil.SafeWriteSession(sessionFile, string(payload))
	}

	// Shutdown askd daemon
	stateFile := rtpkg.StateFilePath("askd.json")
	rpc.ShutdownDaemon("ask", 1.0, stateFile)

	// Remove runtime dir
	os.RemoveAll(l.runtimeDir)

	fmt.Fprintf(os.Stderr, "Cleanup complete\n")
}

// startAskdDaemon starts the unified askd daemon.
func (l *aiLauncher) startAskdDaemon() {
	stateFile := rtpkg.StateFilePath("askd.json")
	if rpc.PingDaemon("ask", 0.5, stateFile) {
		st := rpc.ReadState(stateFile)
		if st != nil {
			h, _ := st["host"].(string)
			p := 0
			if v, ok := st["port"].(float64); ok {
				p = int(v)
			}
			if h != "" && p > 0 {
				fmt.Fprintf(os.Stderr, "askd already running at %s:%d\n", h, p)
			} else {
				fmt.Fprintf(os.Stderr, "askd already running\n")
			}
		}
		return
	}

	selfPath, _ := os.Executable()
	if selfPath == "" {
		return
	}
	askdPath := filepath.Join(filepath.Dir(selfPath), "askd")
	if _, err := os.Stat(askdPath); err != nil {
		return
	}

	fmt.Fprintf(os.Stderr, "Starting askd daemon...\n")
	cmd := exec.Command(askdPath)
	cmd.Env = append(os.Environ(), "CCB_PARENT_PID="+strconv.Itoa(os.Getpid()))
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start askd: %v\n", err)
		return
	}
	go func() { _ = cmd.Wait() }()

	// Wait for daemon to become reachable
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if rpc.PingDaemon("ask", 0.2, stateFile) {
			st := rpc.ReadState(stateFile)
			if st != nil {
				h, _ := st["host"].(string)
				p := 0
				if v, ok := st["port"].(float64); ok {
					p = int(v)
				}
				if h != "" && p > 0 {
					fmt.Fprintf(os.Stderr, "askd started at %s:%d\n", h, p)
				} else {
					fmt.Fprintf(os.Stderr, "askd started\n")
				}
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	fmt.Fprintf(os.Stderr, "askd start requested, but daemon not reachable yet\n")
}

// cmdSettings returns the resolved cmd pane settings.
func (l *aiLauncher) cmdSettings() (bool, string, string) {
	cfg := l.cmdConfig
	if cfg == nil {
		return false, "", ""
	}
	enabled, _ := cfg["enabled"].(bool)
	if !enabled {
		return false, "", ""
	}
	title, _ := cfg["title"].(string)
	if title == "" {
		title = "CCB-Cmd"
	}
	startCmd, _ := cfg["start_cmd"].(string)
	return true, title, startCmd
}

// runUp implements the full start flow (Python's AILauncher.run_up).
func (l *aiLauncher) runUp() int {
	versionStr := fmt.Sprintf("v%s", Version)
	if GitCommit != "" {
		versionStr += fmt.Sprintf(" (%s %s)", GitCommit, GitDate)
	}
	fmt.Fprintf(os.Stderr, "Claude Code Bridge %s\n", versionStr)
	fmt.Fprintf(os.Stderr, "%s\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Fprintf(os.Stderr, "Backends: %s\n", strings.Join(l.providers, ", "))
	fmt.Fprintf(os.Stderr, "%s\n", strings.Repeat("=", 50))

	// Require existing terminal session
	insideTmux := os.Getenv("TMUX") != "" || os.Getenv("TMUX_PANE") != ""
	insideWezterm := os.Getenv("WEZTERM_PANE") != ""

	if l.terminalType == "wezterm" && !insideWezterm {
		l.terminalType = ""
	}
	if l.terminalType == "tmux" && !insideTmux {
		l.terminalType = ""
	}

	if l.terminalType == "" {
		fmt.Fprintln(os.Stderr, "No terminal backend detected (WezTerm or tmux)")
		fmt.Fprintln(os.Stderr, "   Solutions:")
		fmt.Fprintln(os.Stderr, "   - Install WezTerm (recommended): https://wezfurlong.org/wezterm/")
		fmt.Fprintln(os.Stderr, "   - Or install tmux")
		if _, err := exec.LookPath("tmux"); err == nil {
			fmt.Fprintln(os.Stderr, "   - tmux is installed, but you're not inside a tmux session (run `tmux` first)")
		}
		fmt.Fprintln(os.Stderr, "   - Or set CCB_TERMINAL=wezterm and configure CODEX_WEZTERM_BIN")
		return 2
	}

	// Verify project config dir
	cfgDir := l.projectConfigDir()
	if info, err := os.Stat(cfgDir); err != nil || !info.IsDir() {
		fmt.Fprintln(os.Stderr, "Missing required project config directory: .ccb")
		fmt.Fprintf(os.Stderr, "   project_root: %s\n", l.projectRoot)
		fmt.Fprintf(os.Stderr, "Fix: mkdir -p %s\n", cfgDir)
		return 2
	}

	if len(l.providers) == 0 {
		fmt.Fprintln(os.Stderr, "No providers configured.")
		return 2
	}

	// Set environment
	os.Setenv("CCB_MANAGED", "1")
	os.Setenv("CCB_PARENT_PID", strconv.Itoa(l.ccbPID))
	if os.Getenv("CCB_RUN_DIR") == "" {
		os.Setenv("CCB_RUN_DIR", l.projectRunDir)
	}

	// Determine anchor and spawn items
	l.anchorProv = l.providers[len(l.providers)-1]
	l.anchorPaneID = l.currentPaneID()
	if l.anchorPaneID == "" {
		fmt.Fprintln(os.Stderr, "Unable to determine current pane id. Run inside tmux or WezTerm.")
		return 2
	}

	cmdEnabled, _, _ := l.cmdSettings()

	// Build spawn list: items that need NEW panes (everything except anchor)
	var spawnItems []string
	if cmdEnabled {
		spawnItems = append(spawnItems, "cmd")
	}
	// reversed non-anchor providers
	for i := len(l.providers) - 2; i >= 0; i-- {
		spawnItems = append(spawnItems, l.providers[i])
	}

	totalPanes := 1 + len(spawnItems)
	leftCount := 1
	if totalPanes > 1 {
		leftCount = max(1, totalPanes/2)
	}
	rightCount := totalPanes - leftCount

	extras := make([]string, len(spawnItems))
	copy(extras, spawnItems)

	var rightTopItem string
	var remaining []string
	if rightCount > 0 && len(extras) > 0 {
		rightTopItem = extras[0]
		remaining = extras[1:]
	} else {
		remaining = extras
	}

	leftSlots := max(0, leftCount-1)
	rightSlots := max(0, rightCount)
	if rightTopItem != "" {
		rightSlots = max(0, rightCount-1)
	}

	leftItems := []string{l.anchorProv}
	if leftSlots > 0 && len(remaining) >= leftSlots {
		leftItems = append(leftItems, remaining[:leftSlots]...)
	} else {
		leftItems = append(leftItems, remaining...)
	}

	var rightItems []string
	if rightTopItem != "" {
		rightItems = append(rightItems, rightTopItem)
	}
	if rightSlots > 0 {
		startIdx := leftSlots
		endIdx := leftSlots + rightSlots
		if startIdx < len(remaining) {
			if endIdx > len(remaining) {
				endIdx = len(remaining)
			}
			rightItems = append(rightItems, remaining[startIdx:endIdx]...)
		}
	}

	// Register cleanup
	defer l.cleanup()

	// Set up signal handling — notify main goroutine instead of os.Exit
	// so that defers (lock release, cleanup) execute properly.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	sigReceived := make(chan struct{})
	go func() {
		<-sigCh
		close(sigReceived)
	}()

	// Label anchor pane
	l.setCurrentPaneLabel(l.anchorProv)

	// Mark claude session if anchor
	if l.anchorProv == "claude" {
		paneTitleMarker := fmt.Sprintf("CCB-Claude-%s", shortProjectID(l.projectID))
		l.writeClaudeSession(l.anchorPaneID, paneTitleMarker)
	}

	// Start askd daemon
	l.startAskdDaemon()

	// Helper to start an item
	startItem := func(item string, parent string, direction string) (string, error) {
		if item == "cmd" {
			return l.startCmdPane(parent, direction)
		}
		return l.startProviderInPane(item, parent, direction)
	}

	// Start right-top item
	var rightTop string
	if len(rightItems) > 0 {
		var err error
		rightTop, err = startItem(rightItems[0], l.anchorPaneID, "right")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to start %s: %v\n", rightItems[0], err)
			return 1
		}
	}

	// Start left-bottom items (below the anchor)
	lastLeft := l.anchorPaneID
	for _, item := range leftItems[1:] {
		paneID, err := startItem(item, lastLeft, "bottom")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to start %s: %v\n", item, err)
			return 1
		}
		lastLeft = paneID
	}

	// Start right-bottom items
	lastRight := rightTop
	for _, item := range rightItems[1:] {
		paneID, err := startItem(item, lastRight, "bottom")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to start %s: %v\n", item, err)
			return 1
		}
		lastRight = paneID
	}

	// Run anchor provider in current pane (blocking)
	rc := l.startProviderInCurrentPane(l.anchorProv)

	// If interrupted by signal, return 130
	select {
	case <-sigReceived:
		return 130
	default:
		return rc
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// -----------------------------------------------------------------------
// cmdStart implements the default "ccb [providers...]" start command.
// -----------------------------------------------------------------------

func cmdStart(providerArgs []string, resume, auto bool) int {
	// Enforce terminal environment
	termType := terminal.DetectTerminal()
	if termType == "" {
		fmt.Fprintln(os.Stderr, "[ERROR] CCB must run inside tmux or WezTerm.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Please start tmux first:")
		fmt.Fprintln(os.Stderr, "  tmux")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Or use WezTerm terminal emulator.")
		return 1
	}

	cwd, _ := os.Getwd()
	if cwd == "" {
		cwd = "."
	}

	primaryCfg := sessionutil.ProjectConfigDir(cwd)
	legacyCfg := sessionutil.LegacyProjectConfigDir(cwd)

	// Validate config directories
	if info, err := os.Stat(primaryCfg); err == nil && !info.IsDir() {
		fmt.Fprintln(os.Stderr, "Invalid .ccb: exists but is not a directory")
		fmt.Fprintf(os.Stderr, "   path: %s\n", primaryCfg)
		fmt.Fprintln(os.Stderr, "Fix: remove it or rename it, then retry.")
		return 2
	}

	if info, err := os.Stat(legacyCfg); err == nil && !info.IsDir() {
		if _, err := os.Stat(primaryCfg); err != nil {
			fmt.Fprintln(os.Stderr, "Invalid .ccb_config: exists but is not a directory")
			fmt.Fprintf(os.Stderr, "   path: %s\n", legacyCfg)
			fmt.Fprintln(os.Stderr, "Fix: remove it or rename it, then retry.")
			return 2
		}
	}

	// Migrate legacy config dir
	if _, err := os.Stat(primaryCfg); err != nil {
		if info, err := os.Stat(legacyCfg); err == nil && info.IsDir() {
			if err := os.Rename(legacyCfg, primaryCfg); err != nil {
				fmt.Fprintf(os.Stderr, "Failed to migrate %s -> %s: %v\n", legacyCfg, primaryCfg, err)
				fmt.Fprintln(os.Stderr, "   Continuing with legacy .ccb_config directory.")
			} else {
				fmt.Fprintf(os.Stderr, "Migrated legacy config dir: %s -> %s\n", legacyCfg, primaryCfg)
			}
		}
	}

	cfg := sessionutil.ResolveProjectConfigDir(cwd)
	if info, err := os.Stat(cfg); err != nil || !info.IsDir() {
		isDangerous, dangerReason := isDangerousRoot(cwd)
		parentAnchor := findParentAnchorDir(cwd)

		if parentAnchor != "" {
			projectRoot := filepath.Dir(parentAnchor)
			fmt.Fprintln(os.Stderr, "Project config directory not found in current directory, but an existing project anchor was found in a parent directory.")
			fmt.Fprintf(os.Stderr, "   cwd:         %s\n", cwd)
			fmt.Fprintf(os.Stderr, "   project_root:%s\n", projectRoot)
			fmt.Fprintln(os.Stderr, "Auto-create blocked to avoid accidental nesting.")
			fmt.Fprintln(os.Stderr, "If you want this directory to be a separate project, create .ccb here:")
			fmt.Fprintln(os.Stderr, "   mkdir .ccb")
			return 2
		}

		if isDangerous && !envTruthy("CCB_INIT_PROJECT_DANGEROUS") {
			fmt.Fprintf(os.Stderr, "Refusing to auto-create .ccb in %s.\n", dangerReason)
			fmt.Fprintln(os.Stderr, "If you really intend to do this, set CCB_INIT_PROJECT_DANGEROUS=1 and retry.")
			return 2
		}

		if err := os.Mkdir(cfg, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to create %s: %v\n", cfg, err)
			return 2
		}
		fmt.Fprintf(os.Stderr, "Created: %s\n", cfg)
	}

	// Parse provider args
	requestedProviders, requestedCmdEnabled := parseProvidersWithCmd(providerArgs)

	// Enforce single instance per directory
	resolved, _ := filepath.Abs(cwd)
	if resolvedReal, err := filepath.EvalSymlinks(resolved); err == nil {
		resolved = resolvedReal
	}
	ccbLock := processlock.NewProviderLock("ccb", 0.1, resolved)
	if !ccbLock.TryAcquire() {
		pid := ""
		if data, err := os.ReadFile(ccbLock.LockFile); err == nil {
			pid = strings.TrimSpace(string(data))
		}
		pidMsg := ""
		if pid != "" {
			pidMsg = fmt.Sprintf(" (pid %s)", pid)
		}

		// Try activating existing provider pane
		focusProvider := ""
		if len(requestedProviders) == 1 {
			focusProvider = requestedProviders[0]
		}
		if focusProvider != "" {
			ccbProjectID := projectid.ComputeCCBProjectID(cwd)
			record := paneregistry.LoadRegistryByProjectID(ccbProjectID, focusProvider)
			if record != nil {
				backend := terminal.GetBackendForSession(record)
				paneID := terminal.GetPaneIDFromSession(record)
				if backend != nil && paneID != "" && backend.IsAlive(paneID) {
					backend.Activate(paneID)
					fmt.Fprintf(os.Stderr, "Reusing existing ccb instance for this directory%s; activated %s pane %s.\n",
						pidMsg, focusProvider, paneID)
					return 0
				}
			}
		}

		fmt.Fprintf(os.Stderr, "Another ccb instance is already running for this directory%s.\n", pidMsg)
		fmt.Fprintln(os.Stderr, "Only one ccb instance is allowed per directory.")
		if focusProvider != "" {
			fmt.Fprintf(os.Stderr, "Try switching to the existing %s pane in that ccb session.\n", focusProvider)
		}
		return 2
	}
	defer ccbLock.Release()

	providers := requestedProviders
	config := startconfig.LoadStartConfig(cwd)
	configData := config.Data

	if len(providers) == 0 {
		// Use config file providers or defaults
		if rawProviders, ok := configData["providers"]; ok {
			switch rp := rawProviders.(type) {
			case []interface{}:
				for _, p := range rp {
					if s, ok := p.(string); ok {
						providers = append(providers, s)
					}
				}
			case string:
				providers = parseProviders([]string{rp}, false)
			}
		}
		if len(providers) == 0 {
			providers = startconfig.DefaultProviders
			if config.Path == "" {
				createdPath, created := startconfig.EnsureDefaultStartConfig(cwd)
				if created && createdPath != "" {
					fmt.Fprintf(os.Stderr, "Created default config: %s\n", createdPath)
				}
			}
		}
	}

	if len(providers) == 0 {
		fmt.Fprintln(os.Stderr, "No providers configured. Define providers in ccb.config or pass them on the command line.")
		return 2
	}

	// Read flags from config
	flags, _ := configData["flags"].(map[string]interface{})
	if flags != nil {
		if !resume {
			if v, ok := flags["resume"]; ok && isTruthyVal(v) {
				resume = true
			}
			if v, ok := flags["restore"]; ok && isTruthyVal(v) {
				resume = true
			}
			if v, ok := flags["auto_resume"]; ok && isTruthyVal(v) {
				resume = true
			}
		}
		if !auto {
			if v, ok := flags["auto"]; ok && isTruthyVal(v) {
				auto = true
			}
			if v, ok := flags["auto_mode"]; ok && isTruthyVal(v) {
				auto = true
			}
		}
	}

	// Resolve cmd config
	var cmdConfig map[string]interface{}
	if rawCmd, ok := configData["cmd"]; ok {
		switch v := rawCmd.(type) {
		case bool:
			cmdConfig = map[string]interface{}{"enabled": v}
		case map[string]interface{}:
			cmdConfig = v
		}
	}
	if requestedCmdEnabled {
		if cmdConfig == nil {
			cmdConfig = map[string]interface{}{}
		}
		cmdConfig["enabled"] = true
	}

	// Launch args and env from config
	var launchArgs map[string]interface{}
	if raw, ok := configData["launch_args"]; ok {
		if m, ok := raw.(map[string]interface{}); ok {
			launchArgs = m
		}
	}
	var launchEnv map[string]interface{}
	if raw, ok := configData["launch_env"]; ok {
		if m, ok := raw.(map[string]interface{}); ok {
			launchEnv = m
		}
	}

	launcher := newAILauncher(providers, resume, auto, cmdConfig, launchArgs, launchEnv)
	return launcher.runUp()
}

func isTruthyVal(v interface{}) bool {
	switch val := v.(type) {
	case bool:
		return val
	case string:
		lower := strings.ToLower(strings.TrimSpace(val))
		return lower == "true" || lower == "1" || lower == "yes" || lower == "on"
	case float64:
		return val != 0
	}
	return false
}

func main() {
	os.Exit(run(os.Args))
}

func run(argv []string) int {
	args := argv[1:]

	if containsFlag(args, "--print-version") {
		fmt.Printf("v%s\n", Version)
		return 0
	}

	if len(args) > 0 && (args[0] == "-v" || args[0] == "--version") {
		args = []string{"version"}
	}

	if len(args) > 0 && args[0] == "up" {
		fmt.Fprintln(os.Stderr, "`ccb up` is no longer supported.")
		fmt.Fprintln(os.Stderr, "Use: ccb [providers...]  (or configure ccb.config)")
		return 2
	}

	// Handle subcommands: kill, update, version, uninstall, reinstall
	if len(args) > 0 {
		switch args[0] {
		case "kill":
			force := containsFlag(args[1:], "-f") || containsFlag(args[1:], "--force")
			yes := containsFlag(args[1:], "-y") || containsFlag(args[1:], "--yes")
			providerArgs := filterFlags(args[1:])
			return cmdKill(providerArgs, force, yes)

		case "update":
			target := ""
			remaining := filterFlags(args[1:])
			if len(remaining) > 0 {
				target = remaining[0]
			}
			return cmdUpdate(target)

		case "version":
			return cmdVersion()

		case "uninstall":
			return cmdUninstall()

		case "reinstall":
			return cmdReinstall()

		case "mail":
			fmt.Fprintln(os.Stderr, "ccb mail is not yet implemented in Go")
			return 1
		}
	}

	// Default: start command
	resume := containsFlag(args, "-r") || containsFlag(args, "--resume") || containsFlag(args, "--restore")
	auto := containsFlag(args, "-a") || containsFlag(args, "--auto")
	help := containsFlag(args, "-h") || containsFlag(args, "--help")

	providerArgs := filterFlags(args)

	if help {
		fmt.Println("Usage: ccb [providers...] [options]")
		fmt.Println("")
		fmt.Println("Providers: codex, gemini, opencode, claude (space or comma separated)")
		fmt.Println("")
		fmt.Println("Options:")
		fmt.Println("  -r, --resume, --restore    Resume context")
		fmt.Println("  -a, --auto                 Full auto permission mode")
		fmt.Println("  -h, --help                 Show this help message")
		fmt.Println("")
		fmt.Println("Subcommands:")
		fmt.Println("  ccb kill [providers...] [-f] [-y]    Kill sessions or clean up zombies")
		fmt.Println("  ccb update [version]                 Update to latest or specified version")
		fmt.Println("  ccb version                          Show version and check for updates")
		fmt.Println("  ccb uninstall                        Uninstall ccb")
		fmt.Println("  ccb reinstall                        Reinstall ccb")
		return 0
	}

	return cmdStart(providerArgs, resume, auto)
}

// containsFlag checks if any arg matches the given flag.
func containsFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

// filterFlags removes flags (starting with -) from args.
func filterFlags(args []string) []string {
	var result []string
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			result = append(result, a)
		}
	}
	return result
}

// Ensure packages are used.
var _ = envutil.EnvBool
var _ = sort.Strings

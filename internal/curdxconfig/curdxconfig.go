// Package curdxconfig provides CURDX configuration for Windows/WSL backend environment.
// Source: claude_code_bridge/lib/curdx_config.py
package curdxconfig

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// GetBackendEnv returns the BackendEnv from env var or .curdx-config.json.
// Returns "wsl", "windows", or "".
func GetBackendEnv() string {
	v := strings.TrimSpace(strings.ToLower(os.Getenv("CURDX_BACKEND_ENV")))
	if v == "wsl" || v == "windows" {
		return v
	}

	cwd, err := os.Getwd()
	if err != nil {
		if runtime.GOOS == "windows" {
			return "windows"
		}
		return ""
	}

	path := filepath.Join(cwd, ".curdx-config.json")
	data, err := os.ReadFile(path)
	if err == nil {
		var obj map[string]interface{}
		if json.Unmarshal(data, &obj) == nil {
			if raw, ok := obj["BackendEnv"]; ok {
				if s, ok := raw.(string); ok {
					v = strings.TrimSpace(strings.ToLower(s))
					if v == "wsl" || v == "windows" {
						return v
					}
				}
			}
		}
	}

	if runtime.GOOS == "windows" {
		return "windows"
	}
	return ""
}

// wslProbeDistroAndHome probes the default WSL distro and home directory.
func wslProbeDistroAndHome() (string, string) {
	// Try probing distro and home in one shot
	cmd := exec.Command("wsl.exe", "-e", "sh", "-lc", "echo $WSL_DISTRO_NAME; echo $HOME")
	cmd.Env = os.Environ()
	output, err := runWithTimeout(cmd, 10*time.Second)
	if err == nil {
		lines := strings.Split(strings.TrimSpace(output), "\n")
		if len(lines) >= 2 {
			return strings.TrimSpace(lines[0]), strings.TrimSpace(lines[1])
		}
	}

	// Fall back: try listing distros
	distro := "Ubuntu"
	cmd2 := exec.Command("wsl.exe", "-l", "-q")
	cmd2.Env = os.Environ()
	output2, err := runWithTimeout(cmd2, 5*time.Second)
	if err == nil {
		for _, line := range strings.Split(strings.TrimSpace(output2), "\n") {
			d := strings.TrimSpace(strings.Trim(line, "\x00"))
			if d != "" {
				distro = d
				break
			}
		}
	}

	// Probe home for the distro
	home := "/root"
	cmd3 := exec.Command("wsl.exe", "-d", distro, "-e", "sh", "-lc", "echo $HOME")
	cmd3.Env = os.Environ()
	output3, err := runWithTimeout(cmd3, 5*time.Second)
	if err == nil {
		h := strings.TrimSpace(output3)
		if h != "" {
			home = h
		}
	}

	return distro, home
}

// runWithTimeout runs a command with a timeout and returns stdout as string.
func runWithTimeout(cmd *exec.Cmd, timeout time.Duration) (string, error) {
	type result struct {
		output []byte
		err    error
	}
	ch := make(chan result, 1)
	go func() {
		out, err := cmd.Output()
		ch <- result{out, err}
	}()
	select {
	case r := <-ch:
		return string(r.output), r.err
	case <-time.After(timeout):
		if cmd.Process != nil {
			cmd.Process.Kill()
			// Wait to release resources and avoid zombie processes.
			go func() { <-ch }()
		}
		return "", os.ErrDeadlineExceeded
	}
}

// ApplyBackendEnv applies BackendEnv=wsl settings
// (sets session root paths for Windows to access WSL).
func ApplyBackendEnv() {
	if runtime.GOOS != "windows" || GetBackendEnv() != "wsl" {
		return
	}
	if os.Getenv("CODEX_SESSION_ROOT") != "" {
		return
	}

	distro, home := wslProbeDistroAndHome()
	homeWin := strings.ReplaceAll(home, "/", "\\")

	bases := []string{
		`\\wsl.localhost\` + distro,
		`\\wsl$\` + distro,
	}

	for _, base := range bases {
		prefix := base + homeWin
		codexPath := prefix + `\.codex\sessions`

		if info, err := os.Stat(codexPath); err == nil && info != nil {
			setDefault("CODEX_SESSION_ROOT", codexPath)
			return
		}
	}

	prefix := `\\wsl.localhost\` + distro + homeWin
	setDefault("CODEX_SESSION_ROOT", prefix+`\.codex\sessions`)
}

// setDefault sets an env var only if it is not already set.
func setDefault(key, value string) {
	if os.Getenv(key) == "" {
		os.Setenv(key, value)
	}
}

// Package client provides the daemon RPC client.
// Source: claude_code_bridge/lib/askd_client.py
package client

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/anthropics/curdx-bridge/internal/envutil"
	"github.com/anthropics/curdx-bridge/internal/paneregistry"
	"github.com/anthropics/curdx-bridge/internal/projectid"
	"github.com/anthropics/curdx-bridge/internal/providers"
	"github.com/anthropics/curdx-bridge/internal/rpc"
	"github.com/anthropics/curdx-bridge/internal/runtime"
	"github.com/anthropics/curdx-bridge/internal/sessionutil"
)

// ResolveWorkDir resolves the work directory for a provider.
func ResolveWorkDir(spec providers.ProviderClientSpec, cliSessionFile, envSessionFile string) (string, string, error) {
	raw := strings.TrimSpace(cliSessionFile)
	if raw == "" {
		raw = strings.TrimSpace(envSessionFile)
	}
	if raw == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return ".", "", nil
		}
		return cwd, "", nil
	}

	expanded := os.ExpandEnv(raw)
	if strings.HasPrefix(expanded, "~") {
		home, _ := os.UserHomeDir()
		expanded = home + expanded[1:]
	}

	absPath, err := filepath.Abs(expanded)
	if err != nil {
		return "", "", fmt.Errorf("resolve path %s: %w", raw, err)
	}

	if filepath.Base(absPath) != spec.SessionFilename {
		return "", "", fmt.Errorf("invalid session file for %s: expected %s, got %s",
			spec.ProtocolPrefix, spec.SessionFilename, filepath.Base(absPath))
	}

	if _, err := os.Stat(absPath); err != nil {
		return "", "", fmt.Errorf("session file not found: %s", absPath)
	}

	parent := filepath.Base(filepath.Dir(absPath))
	if parent == sessionutil.CCBProjectConfigDirname || parent == sessionutil.CCBProjectConfigLegacyDirname {
		return filepath.Dir(filepath.Dir(absPath)), absPath, nil
	}
	return filepath.Dir(absPath), absPath, nil
}

// ResolveWorkDirWithRegistry resolves work_dir using registry fallback.
func ResolveWorkDirWithRegistry(spec providers.ProviderClientSpec, provider, cliSessionFile string) (string, string, error) {
	workDir, sessionFile, err := ResolveWorkDir(spec, cliSessionFile, os.Getenv("CCB_SESSION_FILE"))
	if err != nil {
		return "", "", err
	}
	if sessionFile != "" {
		return workDir, sessionFile, nil
	}

	// Try to find session file
	found := sessionutil.FindProjectSessionFile(workDir, spec.SessionFilename)
	if found != "" {
		return workDir, found, nil
	}

	// Try registry
	pid := projectid.ComputeCCBProjectID(workDir)
	if pid != "" {
		record := paneregistry.LoadRegistryByProjectID(pid, provider)
		if record != nil {
			if provs, _ := record["providers"].(map[string]any); provs != nil {
				if prov, _ := provs[provider].(map[string]any); prov != nil {
					if sf, _ := prov["session_file"].(string); sf != "" {
						if _, err := os.Stat(sf); err == nil {
							return workDir, sf, nil
						}
					}
				}
			}
		}
	}

	return workDir, "", nil
}

// StateFileFromEnv returns the state file path from environment or default.
func StateFileFromEnv(spec providers.ProviderClientSpec) string {
	if env := os.Getenv(spec.StateFileEnv); env != "" {
		return env
	}
	// All providers now use unified askd state file
	return runtime.StateFilePath("askd.json")
}

// TryDaemonRequest sends a request to the daemon and returns the response.
func TryDaemonRequest(stateFile string, req map[string]any, timeoutS float64) (map[string]any, error) {
	state := rpc.ReadState(stateFile)
	if state == nil {
		return nil, fmt.Errorf("read state: file missing or invalid")
	}

	host, _ := state["connect_host"].(string)
	if host == "" {
		host, _ = state["host"].(string)
	}
	portF, _ := state["port"].(float64)
	port := int(portF)
	token, _ := state["token"].(string)

	if host == "" || port == 0 || token == "" {
		return nil, fmt.Errorf("invalid state file")
	}

	req["token"] = token

	// Send request via TCP
	timeout := time.Duration(timeoutS * float64(time.Second))
	if timeout < time.Second {
		timeout = 30 * time.Second
	}
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", host, port), timeout)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(timeout + 5*time.Second))
	data, _ := json.Marshal(req)
	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		return nil, fmt.Errorf("write: %w", err)
	}

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	if !scanner.Scan() {
		return nil, fmt.Errorf("no response")
	}

	var resp map[string]any
	if err := json.Unmarshal([]byte(scanner.Text()), &resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return resp, nil
}

// MaybeStartDaemon starts the daemon if not running.
func MaybeStartDaemon(spec providers.ProviderClientSpec) error {
	stateFile := StateFileFromEnv(spec)
	if rpc.PingDaemon("ask", 0.5, stateFile) {
		return nil // Already running
	}

	binName := spec.DaemonBinName
	binPath, err := exec.LookPath(binName)
	if err != nil {
		return fmt.Errorf("daemon binary not found: %s", binName)
	}

	cmd := exec.Command(binPath)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start daemon: %w", err)
	}
	go cmd.Wait()

	return nil
}

// WaitForDaemonReady waits for the daemon to become ready.
func WaitForDaemonReady(spec providers.ProviderClientSpec, timeoutS float64) bool {
	stateFile := StateFileFromEnv(spec)
	deadline := time.Now().Add(time.Duration(timeoutS * float64(time.Second)))
	for time.Now().Before(deadline) {
		if rpc.PingDaemon("ask", 0.2, stateFile) {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

// CheckBackgroundMode determines if the command should run in background.
func CheckBackgroundMode(forceBackground, forceForeground bool) bool {
	if forceForeground {
		return false
	}
	if forceBackground {
		return true
	}
	// Default: background if CCB_ALLOW_FOREGROUND is not set
	return !envutil.EnvBool("CCB_ALLOW_FOREGROUND", false)
}

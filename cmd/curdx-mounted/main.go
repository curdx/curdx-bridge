// curdx-mounted - Check which CURDX providers are mounted.
//
// Usage:
//
//	curdx-mounted [--json|--simple] [--autostart] [path]
//
// Source: claude_code_bridge/bin/curdx-mounted
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/curdx/curdx-bridge/internal/rpc"
	"github.com/curdx/curdx-bridge/internal/runtime"
)

var providerNames = []string{"codex", "gemini", "opencode", "claude"}

func sessionFileExists(cwd, provider string) bool {
	candidates := []string{
		filepath.Join(cwd, ".curdx", "."+provider+"-session"),
		filepath.Join(cwd, ".curdx_config", "."+provider+"-session"),
		filepath.Join(cwd, "."+provider+"-session"),
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return true
		}
	}
	return false
}

// isDaemonOnline checks if the unified askd daemon is reachable.
func isDaemonOnline() bool {
	stateFile := runtime.StateFilePath("askd.json")
	return rpc.PingDaemon("ask", 0.5, stateFile)
}

func main() {
	cwd, _ := os.Getwd()
	format := "--json"
	autostart := false

	args := os.Args[1:]
	for len(args) > 0 {
		switch args[0] {
		case "--simple":
			format = "--simple"
			args = args[1:]
		case "--json":
			format = "--json"
			args = args[1:]
		case "--autostart":
			autostart = true
			args = args[1:]
		default:
			if strings.HasPrefix(args[0], "-") {
				fmt.Fprintf(os.Stderr, "Unknown option: %s\n", args[0])
				os.Exit(1)
			}
			cwd = args[0]
			args = args[1:]
		}
	}

	// Get script directory for calling curdx-ping.
	self, _ := os.Executable()
	scriptDir := filepath.Dir(self)

	daemonOnline := isDaemonOnline()

	var mounted []string
	for _, provider := range providerNames {
		if !sessionFileExists(cwd, provider) {
			continue
		}

		isOnline := daemonOnline

		if !isOnline && autostart {
			// Try autostart via curdx-ping.
			curdxPing := filepath.Join(scriptDir, "curdx-ping")
			cmd := exec.Command(curdxPing, provider, "--autostart")
			cmd.Dir = cwd
			cmd.Stdout = nil
			cmd.Stderr = nil
			if err := cmd.Run(); err == nil {
				isOnline = true
			}
		}

		if isOnline {
			mounted = append(mounted, provider)
		}
	}

	mountedStr := strings.Join(mounted, " ")

	switch format {
	case "--simple":
		fmt.Println(strings.TrimSpace(mountedStr))
	default: // --json
		if len(mounted) == 0 {
			fmt.Printf("{\"cwd\":%s,\"mounted\":[]}\n", jsonString(cwd))
		} else {
			jsonArr := make([]string, len(mounted))
			for i, m := range mounted {
				jsonArr[i] = jsonString(m)
			}
			fmt.Printf("{\"cwd\":%s,\"mounted\":[%s]}\n", jsonString(cwd), strings.Join(jsonArr, ","))
		}
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

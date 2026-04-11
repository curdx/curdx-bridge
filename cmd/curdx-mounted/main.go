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
)

type providerDaemon struct {
	provider string
	daemon   string
}

var providerDaemons = []providerDaemon{
	{provider: "codex", daemon: "cask"},
	{provider: "gemini", daemon: "gask"},
	{provider: "opencode", daemon: "oask"},
	{provider: "claude", daemon: "lask"},
}

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

func getOnlineDaemons() map[string]bool {
	online := make(map[string]bool)
	out, err := exec.Command("pgrep", "-af", "bin/[cglod]askd$").Output()
	if err != nil {
		return online
	}
	for _, daemon := range []string{"caskd", "gaskd", "oaskd", "laskd"} {
		if strings.Contains(string(out), daemon) {
			online[daemon] = true
		}
	}
	return online
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

	online := getOnlineDaemons()

	var mounted []string
	for _, pd := range providerDaemons {
		if !sessionFileExists(cwd, pd.provider) {
			continue
		}

		isOnline := online[pd.daemon+"d"]

		if !isOnline && autostart {
			// Try autostart via curdx-ping.
			curdxPing := filepath.Join(scriptDir, "curdx-ping")
			cmd := exec.Command(curdxPing, pd.provider, "--autostart")
			cmd.Dir = cwd
			cmd.Stdout = nil
			cmd.Stderr = nil
			if err := cmd.Run(); err == nil {
				isOnline = true
			}
		}

		if isOnline {
			mounted = append(mounted, pd.provider)
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

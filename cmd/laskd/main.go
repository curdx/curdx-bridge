// laskd - Claude ask daemon.
// Source: claude_code_bridge/bin/laskd
//
// Claude-specific daemon that manages session bindings and
// routes messages to Claude via terminal panes.
package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/anthropics/curdx-bridge/internal/rpc"
	"github.com/anthropics/curdx-bridge/internal/runtime"
	"github.com/anthropics/curdx-bridge/internal/session"
	"github.com/anthropics/curdx-bridge/internal/terminal"
)

func parseListen(value string) (string, int) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "127.0.0.1", 0
	}
	if !strings.Contains(value, ":") {
		return value, 0
	}
	idx := strings.LastIndex(value, ":")
	host := value[:idx]
	portS := value[idx+1:]
	if host == "" {
		host = "127.0.0.1"
	}
	port, err := strconv.Atoi(portS)
	if err != nil {
		port = 0
	}
	return host, port
}

func usage() {
	fmt.Fprintln(os.Stderr, "Usage: laskd [OPTIONS]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "lask daemon (Claude)")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Options:")
	fmt.Fprintln(os.Stderr, "  --listen HOST:PORT    Listen address (default 127.0.0.1:0)")
	fmt.Fprintln(os.Stderr, "  --state-file PATH     Override state file path")
	fmt.Fprintln(os.Stderr, "  --shutdown            Shutdown running daemon")
	fmt.Fprintln(os.Stderr, "  -h, --help            Show this help message")
}

func main() {
	session.GetBackendFunc = func(data map[string]interface{}) session.TerminalBackend {
		t, _ := data["terminal"].(string)
		if t == "wezterm" {
			return terminal.NewWeztermBackend()
		}
		return terminal.NewTmuxBackend("")
	}

	listen := envOrDefault("CCB_LASKD_LISTEN", "127.0.0.1:0")
	stateFile := envOrDefault("CCB_LASKD_STATE_FILE", "")
	shutdown := false

	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-h", "--help":
			usage()
			os.Exit(0)
		case "--listen":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "[ERROR] --listen requires a value")
				os.Exit(1)
			}
			listen = args[i]
		case "--state-file":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "[ERROR] --state-file requires a value")
				os.Exit(1)
			}
			stateFile = args[i]
		case "--shutdown":
			shutdown = true
		default:
			fmt.Fprintf(os.Stderr, "[ERROR] Unknown argument: %s\n", args[i])
			os.Exit(1)
		}
	}

	if shutdown {
		sf := stateFile
		if sf == "" {
			sf = runtime.StateFilePath("laskd.json")
		}
		ok := rpc.ShutdownDaemon("lask", 5.0, sf)
		if ok {
			os.Exit(0)
		}
		os.Exit(1)
	}

	host, port := parseListen(listen)

	fmt.Fprintf(os.Stderr, "laskd: starting on %s:%d\n", host, port)
	if stateFile != "" {
		fmt.Fprintf(os.Stderr, "laskd: state file: %s\n", stateFile)
	}

	_ = host
	_ = port
	_ = stateFile

	// Placeholder: the full LaskdServer implementation will be wired here once
	// internal/laskddaemon is fully ported.
	fmt.Fprintln(os.Stderr, "laskd: daemon implementation pending (use Python laskd for now)")
	os.Exit(1)
}

func envOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

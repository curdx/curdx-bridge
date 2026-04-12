// askd - Unified Ask Daemon for all AI providers.
// Source: claude_code_bridge/bin/askd
//
// This is the daemon entry point. It starts a unified daemon that handles
// codex, opencode, and claude.
package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	adapterPkg "github.com/curdx/curdx-bridge/internal/adapter"
	"github.com/curdx/curdx-bridge/internal/daemon"
	"github.com/curdx/curdx-bridge/internal/registry"
	"github.com/curdx/curdx-bridge/internal/rpc"
	"github.com/curdx/curdx-bridge/internal/runtime"
	"github.com/curdx/curdx-bridge/internal/session"
	"github.com/curdx/curdx-bridge/internal/terminal"
)

var allProviders = []string{"codex", "opencode", "claude"}

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
	fmt.Fprintln(os.Stderr, "Usage: askd [OPTIONS]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Unified ask daemon (all providers)")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Options:")
	fmt.Fprintln(os.Stderr, "  --listen HOST:PORT    Listen address (default 127.0.0.1:0)")
	fmt.Fprintln(os.Stderr, "  --state-file PATH     Override state file path")
	fmt.Fprintln(os.Stderr, "  --shutdown            Shutdown running daemon")
	fmt.Fprintln(os.Stderr, "  --providers LIST      Comma-separated list of providers to enable (default: all)")
	fmt.Fprintln(os.Stderr, "  --work-dir PATH       Override work_dir written to state file")
	fmt.Fprintln(os.Stderr, "  -h, --help            Show this help message")
}

func main() {
	// Wire up the session package's backend resolver so EnsurePane works.
	session.GetBackendFunc = func(data map[string]interface{}) session.TerminalBackend {
		t, _ := data["terminal"].(string)
		if t == "wezterm" {
			return terminal.NewWeztermBackend()
		}
		return terminal.NewTmuxBackend("")
	}

	listen := envOrDefault("CURDX_ASKD_LISTEN", "127.0.0.1:0")
	stateFile := envOrDefault("CURDX_ASKD_STATE_FILE", "")
	shutdown := false
	providersList := envOrDefault("CURDX_ASKD_PROVIDERS", "")
	workDir := envOrDefault("CURDX_WORK_DIR", "")

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
		case "--providers":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "[ERROR] --providers requires a value")
				os.Exit(1)
			}
			providersList = args[i]
		case "--work-dir":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "[ERROR] --work-dir requires a value")
				os.Exit(1)
			}
			workDir = args[i]
		default:
			fmt.Fprintf(os.Stderr, "[ERROR] Unknown argument: %s\n", args[i])
			os.Exit(1)
		}
	}

	if shutdown {
		sf := stateFile
		if sf == "" {
			sf = runtime.StateFilePath("askd.json")
		}
		ok := rpc.ShutdownDaemon("askd", 5.0, sf)
		if ok {
			os.Exit(0)
		}
		os.Exit(1)
	}

	host, port := parseListen(listen)

	providers := allProviders
	if providersList != "" {
		providers = nil
		for _, p := range strings.Split(providersList, ",") {
			p = strings.TrimSpace(strings.ToLower(p))
			if p == "" {
				continue
			}
			valid := false
			for _, ap := range allProviders {
				if p == ap {
					valid = true
					break
				}
			}
			if !valid {
				fmt.Fprintf(os.Stderr, "Unknown provider: %s\n", p)
				fmt.Fprintf(os.Stderr, "Available: %s\n", strings.Join(allProviders, ", "))
				os.Exit(1)
			}
			providers = append(providers, p)
		}
	}

	fmt.Fprintf(os.Stderr, "Enabled providers: %s\n", strings.Join(providers, ", "))

	// Create provider registry and register adapters
	reg := registry.New()
	// NOTE: Concrete adapter implementations are registered here.
	// Each adapter is already ported in internal/adapter/*.go
	for _, name := range providers {
		a := adapterForProvider(name)
		if a != nil {
			reg.Register(a)
		}
	}

	d := daemon.NewUnifiedAskDaemon(host, port, stateFile, reg, workDir)
	os.Exit(d.ServeForever())
}

func adapterForProvider(name string) adapterPkg.BaseProviderAdapter {
	switch name {
	case "codex":
		return &adapterPkg.CodexAdapter{}
	case "opencode":
		return &adapterPkg.OpenCodeAdapter{}
	case "claude":
		return &adapterPkg.ClaudeAdapter{}
	default:
		return nil
	}
}

func envOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

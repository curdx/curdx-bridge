// curdx-ping - Unified ping for AI providers.
//
// Usage:
//
//	curdx-ping <provider> [--session-file FILE] [--autostart]
//
// Source: claude_code_bridge/bin/curdx-ping
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/curdx/curdx-bridge/internal/askcli"
	"github.com/curdx/curdx-bridge/internal/providers"
	"github.com/curdx/curdx-bridge/internal/rpc"
	"github.com/curdx/curdx-bridge/internal/runtime"
)

// providerComm maps provider name to (label, sessionFilename).
var providerComm = map[string]struct {
	label       string
	sessionFile string
}{
	"gemini":   {label: "Gemini", sessionFile: ".gemini-session"},
	"codex":    {label: "Codex", sessionFile: ".codex-session"},
	"opencode": {label: "OpenCode", sessionFile: ".opencode-session"},
	"claude":   {label: "Claude", sessionFile: ".claude-session"},
}

// providerSpecs maps provider name to its client spec for autostart.
var providerSpecs = map[string]providers.ProviderClientSpec{
	"codex":    providers.CaskClientSpec,
	"gemini":   providers.GaskClientSpec,
	"opencode": providers.OaskClientSpec,
	"claude":   providers.LaskClientSpec,
}

func usage() {
	fmt.Fprintln(os.Stderr, "Usage: curdx-ping <provider> [--session-file FILE] [--autostart]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Providers:")
	fmt.Fprintln(os.Stderr, "  gemini, codex, opencode, claude")
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	provider := strings.ToLower(os.Args[1])

	if provider == "-h" || provider == "--help" {
		usage()
		os.Exit(0)
	}

	info, ok := providerComm[provider]
	if !ok {
		fmt.Fprintf(os.Stderr, "[ERROR] Unknown provider: %s\n", provider)
		keys := make([]string, 0, len(providerComm))
		for k := range providerComm {
			keys = append(keys, k)
		}
		fmt.Fprintf(os.Stderr, "[ERROR] Available: %s\n", strings.Join(keys, ", "))
		os.Exit(1)
	}

	// Parse remaining arguments (after the provider positional arg).
	fs := flag.NewFlagSet("curdx-ping "+provider, flag.ContinueOnError)
	sessionFile := fs.String("session-file", "", "Path to session file")
	autostart := fs.Bool("autostart", false, "Start daemon if needed")
	fs.Parse(os.Args[2:])

	if *sessionFile != "" {
		os.Setenv("CURDX_SESSION_FILE", *sessionFile)
	}
	workDir := askcli.ResolveWorkDir(*sessionFile)

	// Pre-emptive autostart if requested.
	if *autostart {
		if spec, hasSpec := providerSpecs[provider]; hasSpec {
			maybeAutostartDaemon(spec, workDir)
		}
	}

	code := askcli.RunPing(askcli.ProviderPingConfig{
		ProgName:        "curdx-ping " + provider,
		ProviderLabel:   info.label,
		SessionFilename: info.sessionFile,
	})
	os.Exit(code)
}

// maybeAutostartDaemon attempts to ping the daemon and is a best-effort
// pre-check. Full autostart logic requires the daemon launcher which may
// not be fully wired in Go yet; for now we check if the daemon is already
// responsive via RPC.
func maybeAutostartDaemon(spec providers.ProviderClientSpec, workDir string) {
	_ = workDir // workDir reserved for future daemon start logic
	stateFile := runtime.StateFilePath(spec.ProtocolPrefix + "d")
	if rpc.PingDaemon(spec.ProtocolPrefix, 0.5, stateFile) {
		return
	}
	// Daemon not responsive -- a full autostart would launch the daemon
	// process here. For now, this is a best-effort check.
	fmt.Fprintf(os.Stderr, "[WARN] Autostart pre-check: daemon not responsive\n")
}

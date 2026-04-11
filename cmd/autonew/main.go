// autonew - Send /new (or /clear) command to a provider's terminal pane.
//
// Usage:
//
//	autonew <provider>
//
// Providers: gemini, codex, opencode, claude
//
// Source: claude_code_bridge/bin/autonew
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/curdx/curdx-bridge/internal/paneregistry"
	"github.com/curdx/curdx-bridge/internal/projectid"
	"github.com/curdx/curdx-bridge/internal/terminal"
)

// providerCommands maps provider to its reset command.
var providerCommands = map[string]string{
	"gemini":   "/clear",
	"codex":    "/new",
	"opencode": "/new",
	"claude":   "/new",
}

func usage() {
	fmt.Fprintln(os.Stderr, "Usage: autonew <provider>")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Providers:")
	fmt.Fprintln(os.Stderr, "  gemini, codex, opencode, claude")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Sends /new to the provider's pane to start a new session.")
}

func main() {
	os.Exit(run())
}

func run() int {
	if len(os.Args) <= 1 {
		usage()
		return 1
	}

	provider := strings.ToLower(os.Args[1])

	if provider == "-h" || provider == "--help" {
		usage()
		return 0
	}

	if _, ok := providerCommands[provider]; !ok {
		fmt.Fprintf(os.Stderr, "[ERROR] Unknown provider: %s\n", provider)
		keys := sortedKeys(providerCommands)
		fmt.Fprintf(os.Stderr, "[ERROR] Available: %s\n", strings.Join(keys, ", "))
		return 1
	}

	// Get current project ID.
	workDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] Failed to compute project ID: %s\n", err)
		return 1
	}
	pid := projectid.ComputeCURDXProjectID(workDir)

	// Wire up the pane registry backend so LoadRegistryByProjectID can check pane liveness.
	paneregistry.GetBackendFunc = func(record map[string]interface{}) paneregistry.TerminalBackend {
		b := terminal.GetBackendForSession(record)
		if b == nil {
			return nil
		}
		return tmuxBackendAdapter{b}
	}

	// Load registry for this project and provider.
	record := paneregistry.LoadRegistryByProjectID(pid, provider)
	if record == nil {
		fmt.Fprintf(os.Stderr, "[ERROR] No active %s session found for this project.\n", provider)
		return 1
	}

	// Get provider's pane_id from the providers map.
	paneID := extractProviderPaneID(record, provider)
	if paneID == "" {
		fmt.Fprintf(os.Stderr, "[ERROR] No pane_id found for %s.\n", provider)
		return 1
	}

	// Get terminal backend.
	backend := terminal.GetBackendForSession(record)
	if backend == nil {
		fmt.Fprintln(os.Stderr, "[ERROR] Terminal backend not available.")
		return 1
	}

	// Check if pane is alive.
	if !backend.IsAlive(paneID) {
		fmt.Fprintf(os.Stderr, "[ERROR] %s pane %s is not alive.\n", provider, paneID)
		return 1
	}

	// Send reset command.
	resetCmd := providerCommands[provider]
	if err := backend.SendText(paneID, resetCmd); err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] Failed to send %s: %s\n", resetCmd, err)
		return 1
	}

	fmt.Printf("Sent %s to %s (pane: %s)\n", resetCmd, provider, paneID)
	return 0
}

// extractProviderPaneID extracts the pane_id for a provider from a registry record.
func extractProviderPaneID(record map[string]interface{}, provider string) string {
	if provRaw, ok := record["providers"]; ok {
		if provMap, ok := provRaw.(map[string]interface{}); ok {
			if entry, ok := provMap[provider]; ok {
				if entryMap, ok := entry.(map[string]interface{}); ok {
					if paneID, ok := entryMap["pane_id"]; ok {
						s := strings.TrimSpace(fmt.Sprintf("%v", paneID))
						if s != "" && s != "<nil>" {
							return s
						}
					}
				}
			}
		}
	}
	return ""
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// Simple sort for small sets.
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	return keys
}

// tmuxBackendAdapter adapts terminal.TerminalBackend to paneregistry.TerminalBackend.
type tmuxBackendAdapter struct {
	b terminal.TerminalBackend
}

func (a tmuxBackendAdapter) IsAlive(paneID string) bool {
	return a.b.IsAlive(paneID)
}

func (a tmuxBackendAdapter) FindPaneByTitleMarker(marker string, cwdHint ...string) string {
	// The terminal.TerminalBackend interface doesn't include FindPaneByTitleMarker,
	// so we return "" here. This is a best-effort adapter.
	return ""
}

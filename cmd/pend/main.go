// pend - View latest reply from AI providers.
//
// Usage:
//
//	pend <provider> [N] [--session-file FILE]
//
// Source: claude_code_bridge/bin/pend
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var providerPends = map[string]string{
	"gemini":   "gpend",
	"codex":    "cpend",
	"opencode": "opend",
	"claude":   "lpend",
}

func usage() {
	fmt.Fprintln(os.Stderr, "Usage: pend <provider> [N] [--session-file FILE]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Providers:")
	fmt.Fprintln(os.Stderr, "  gemini, codex, opencode, claude")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Arguments:")
	fmt.Fprintln(os.Stderr, "  N    Show the latest N conversations (default: 1)")
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

	pendCmd, ok := providerPends[provider]
	if !ok {
		fmt.Fprintf(os.Stderr, "[ERROR] Unknown provider: %s\n", provider)
		fmt.Fprintf(os.Stderr, "[ERROR] Available: %s\n", joinKeys(providerPends))
		os.Exit(1)
	}

	// Resolve path to the sibling command.
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] %s\n", err)
		os.Exit(1)
	}
	pendPath := filepath.Join(filepath.Dir(self), pendCmd)

	if _, err := os.Stat(pendPath); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "[ERROR] Command not found: %s\n", pendPath)
		os.Exit(1)
	}

	cmd := exec.Command(pendPath, os.Args[2:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		fmt.Fprintf(os.Stderr, "[ERROR] %s\n", err)
		os.Exit(1)
	}
}

func joinKeys(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return strings.Join(keys, ", ")
}

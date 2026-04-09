// Package cliutil provides CLI output utilities and exit codes.
// Source: claude_code_bridge/lib/cli_output.py
package cliutil

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	ExitOK      = 0
	ExitError   = 1
	ExitNoReply = 2
)

// AtomicWriteText writes content to path atomically using temp+rename.
// Creates parent directories if needed.
// Matches Python's atomic_write_text: mkstemp in same dir, then os.replace.
func AtomicWriteText(path string, content string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}

	base := filepath.Base(path)
	prefix := "." + base + "."

	f, err := os.CreateTemp(dir, prefix+"*.tmp")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpPath := f.Name()

	success := false
	defer func() {
		if !success {
			f.Close()
			os.Remove(tmpPath)
		}
	}()

	if _, err := f.WriteString(content); err != nil {
		return fmt.Errorf("write temp: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close temp: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename %s -> %s: %w", tmpPath, path, err)
	}
	success = true
	return nil
}

// NormalizeMessageParts joins parts with space and trims whitespace.
func NormalizeMessageParts(parts []string) string {
	return strings.TrimSpace(strings.Join(parts, " "))
}

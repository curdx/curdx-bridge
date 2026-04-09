// Package envutil provides environment variable parsing utilities.
// Source: claude_code_bridge/lib/env_utils.py
package envutil

import (
	"os"
	"strconv"
	"strings"
)

// EnvBool reads a boolean environment variable.
// Returns default if unset, empty, or not a recognized value.
// Recognized true values: "1", "true", "yes", "on"
// Recognized false values: "0", "false", "no", "off"
func EnvBool(name string, defaultVal bool) bool {
	raw, ok := os.LookupEnv(name)
	if !ok || raw == "" {
		return defaultVal
	}
	v := strings.ToLower(strings.TrimSpace(raw))
	switch v {
	case "0", "false", "no", "off":
		return false
	case "1", "true", "yes", "on":
		return true
	default:
		return defaultVal
	}
}

// EnvInt reads an integer environment variable.
// Returns default if unset, empty, or not a valid integer.
func EnvInt(name string, defaultVal int) int {
	raw, ok := os.LookupEnv(name)
	if !ok || raw == "" {
		return defaultVal
	}
	v, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return defaultVal
	}
	return v
}

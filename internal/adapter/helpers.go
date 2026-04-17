// Shared helper functions for adapter implementations.
package adapter

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// nowMs returns the current time in milliseconds since epoch.
func nowMs() int64 {
	return time.Now().UnixMilli()
}

// envFloatDefault reads a float64 from an environment variable, returning defaultVal on error.
func envFloatDefault(name string, defaultVal float64) float64 {
	raw := os.Getenv(name)
	if raw == "" {
		return defaultVal
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return defaultVal
	}
	return v
}

// envIntDefault reads an int from an environment variable, returning defaultVal on error.
func envIntDefault(name string, defaultVal int) int {
	raw := os.Getenv(name)
	if raw == "" {
		return defaultVal
	}
	v, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return defaultVal
	}
	return v
}

// envBoolDefault reads a bool from an environment variable.
func envBoolDefault(name string, defaultVal bool) bool {
	raw := strings.TrimSpace(strings.ToLower(os.Getenv(name)))
	if raw == "" {
		return defaultVal
	}
	return raw == "1" || raw == "true" || raw == "yes" || raw == "on"
}

// intPtr returns a pointer to an int value.
//
//go:fix inline
func intPtr(v int) *int {
	return new(v)
}

// expandHome expands a leading "~/" to the user's home directory.
func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

// tailStateForJSONL returns an initial state for tail-reading a JSONL log file.
func tailStateForJSONL(logPath string, tailBytes int64) (sessionPath string, offset int64) {
	if logPath == "" {
		return logPath, 0
	}
	fi, err := os.Stat(logPath)
	if err != nil {
		return logPath, 0
	}
	size := fi.Size()
	offset = max(size-tailBytes, 0)
	return logPath, offset
}

// tailStateForPaneLog returns an initial PaneLogState for tail-reading a pane log file.
func tailStateForPaneLog(logPath string, tailBytes int64) (paneLogPath string, offset int64) {
	if logPath == "" {
		return logPath, 0
	}
	fi, err := os.Stat(logPath)
	if err != nil {
		return logPath, 0
	}
	size := fi.Size()
	offset = max(size-tailBytes, 0)
	return logPath, offset
}

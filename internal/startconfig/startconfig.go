// Package startconfig provides CURDX start configuration loading and parsing.
// Source: claude_code_bridge/lib/curdx_start_config.py
package startconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/anthropics/curdx-bridge/internal/sessionutil"
)

const (
	ConfigFilename = "curdx.config"
)

// DefaultProviders is the default set of providers.
var DefaultProviders = []string{"claude", "codex", "gemini"}

// StartConfig holds parsed start configuration data.
type StartConfig struct {
	Data map[string]interface{}
	Path string // empty string means no path (None equivalent)
}

var allowedProviders = map[string]bool{
	"codex":  true,
	"gemini": true,
	"claude": true,
}

// parseTokens extracts tokens from raw config text, stripping comments and delimiters.
func parseTokens(raw string) []string {
	if raw == "" {
		return nil
	}
	var lines []string
	for _, line := range strings.Split(raw, "\n") {
		stripped := line
		if idx := strings.Index(stripped, "//"); idx >= 0 {
			stripped = stripped[:idx]
		}
		if idx := strings.Index(stripped, "#"); idx >= 0 {
			stripped = stripped[:idx]
		}
		lines = append(lines, stripped)
	}
	cleaned := strings.Join(lines, " ")
	// Replace []{}\"' with spaces
	re := regexp.MustCompile(`[\[\]\{\}"']`)
	cleaned = re.ReplaceAllString(cleaned, " ")
	// Split on commas and whitespace
	splitRe := regexp.MustCompile(`[,\s]+`)
	parts := splitRe.Split(cleaned, -1)
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

// normalizeProviders filters and deduplicates provider tokens.
// Returns the provider list and whether "cmd" was found.
func normalizeProviders(tokens []string) ([]string, bool) {
	var providers []string
	seen := map[string]bool{}
	cmdEnabled := false
	for _, raw := range tokens {
		token := strings.TrimSpace(strings.ToLower(raw))
		if token == "" {
			continue
		}
		if token == "cmd" {
			cmdEnabled = true
			continue
		}
		if !allowedProviders[token] {
			continue
		}
		if seen[token] {
			continue
		}
		seen[token] = true
		providers = append(providers, token)
	}
	return providers, cmdEnabled
}

// parseConfigObj parses a decoded JSON value into a config map.
func parseConfigObj(obj interface{}) map[string]interface{} {
	switch v := obj.(type) {
	case map[string]interface{}:
		data := make(map[string]interface{})
		for k, val := range v {
			data[k] = val
		}

		rawProviders, hasProviders := data["providers"]
		var tokens []string

		if hasProviders {
			switch rp := rawProviders.(type) {
			case string:
				tokens = parseTokens(rp)
			case []interface{}:
				for _, p := range rp {
					if p != nil {
						tokens = append(tokens, stringify(p))
					}
				}
			default:
				if rp != nil {
					tokens = []string{stringify(rp)}
				}
			}
		}

		if len(tokens) > 0 {
			providers, cmdEnabled := normalizeProviders(tokens)
			data["providers"] = toInterfaceSlice(providers)
			if cmdEnabled {
				if _, hasCMD := data["cmd"]; !hasCMD {
					data["cmd"] = true
				}
			}
		}
		return data

	case []interface{}:
		var tokens []string
		for _, p := range v {
			if p != nil {
				tokens = append(tokens, stringify(p))
			}
		}
		providers, cmdEnabled := normalizeProviders(tokens)
		data := map[string]interface{}{
			"providers": toInterfaceSlice(providers),
		}
		if cmdEnabled {
			data["cmd"] = true
		}
		return data

	case string:
		tokens := parseTokens(v)
		providers, cmdEnabled := normalizeProviders(tokens)
		data := map[string]interface{}{
			"providers": toInterfaceSlice(providers),
		}
		if cmdEnabled {
			data["cmd"] = true
		}
		return data
	}

	return map[string]interface{}{}
}

// readConfig reads and parses a config file.
func readConfig(path string) map[string]interface{} {
	data, err := os.ReadFile(path)
	if err != nil {
		return map[string]interface{}{}
	}
	// Strip UTF-8 BOM if present (utf-8-sig)
	raw := string(data)
	raw = strings.TrimPrefix(raw, "\xef\xbb\xbf")

	if strings.TrimSpace(raw) == "" {
		return map[string]interface{}{}
	}

	var obj interface{}
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		// Not valid JSON; parse as plain text tokens
		tokens := parseTokens(raw)
		providers, cmdEnabled := normalizeProviders(tokens)
		result := map[string]interface{}{
			"providers": toInterfaceSlice(providers),
		}
		if cmdEnabled {
			result["cmd"] = true
		}
		return result
	}

	return parseConfigObj(obj)
}

// configPaths returns the primary, legacy, and global config file paths.
func configPaths(workDir string) (string, string, string) {
	primary := filepath.Join(sessionutil.ProjectConfigDir(workDir), ConfigFilename)
	legacy := filepath.Join(sessionutil.LegacyProjectConfigDir(workDir), ConfigFilename)
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	globalPath := filepath.Join(home, ".curdx", ConfigFilename)
	return primary, legacy, globalPath
}

// LoadStartConfig loads the start configuration for a work directory.
// Checks primary, legacy, then global config paths.
func LoadStartConfig(workDir string) StartConfig {
	primary, legacy, globalPath := configPaths(workDir)

	if _, err := os.Stat(primary); err == nil {
		return StartConfig{Data: readConfig(primary), Path: primary}
	}
	if _, err := os.Stat(legacy); err == nil {
		return StartConfig{Data: readConfig(legacy), Path: legacy}
	}
	if _, err := os.Stat(globalPath); err == nil {
		return StartConfig{Data: readConfig(globalPath), Path: globalPath}
	}
	return StartConfig{Data: map[string]interface{}{}, Path: ""}
}

// EnsureDefaultStartConfig ensures a default start config file exists.
// Returns (path, created). Path is "" if creation failed.
func EnsureDefaultStartConfig(workDir string) (string, bool) {
	primary, legacy, _ := configPaths(workDir)

	if _, err := os.Stat(primary); err == nil {
		return primary, false
	}
	if _, err := os.Stat(legacy); err == nil {
		return legacy, false
	}

	target := primary
	primaryParent := filepath.Dir(primary)
	legacyParent := filepath.Dir(legacy)
	if _, err := os.Stat(primaryParent); err != nil {
		// primary parent doesn't exist
		if info, err := os.Stat(legacyParent); err == nil && info.IsDir() {
			target = legacy
		}
	}

	targetDir := filepath.Dir(target)
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return "", false
	}

	payload := strings.Join(DefaultProviders, ",") + "\n"
	if err := os.WriteFile(target, []byte(payload), 0o644); err != nil {
		return "", false
	}

	return target, true
}

// stringify converts an interface value to a string.
func stringify(v interface{}) string {
	switch s := v.(type) {
	case string:
		return s
	case float64:
		return fmt.Sprintf("%v", s)
	case bool:
		return fmt.Sprintf("%v", s)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// toInterfaceSlice converts a string slice to an interface slice.
func toInterfaceSlice(ss []string) []interface{} {
	result := make([]interface{}, len(ss))
	for i, s := range ss {
		result[i] = s
	}
	return result
}

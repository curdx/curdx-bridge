// Package paneregistry manages JSON-based session registry files in ~/.curdx/run/.
// Source: claude_code_bridge/lib/pane_registry.py
package paneregistry

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/curdx/curdx-bridge/internal/cliutil"
	"github.com/curdx/curdx-bridge/internal/projectid"
	"github.com/curdx/curdx-bridge/internal/providers"
)

const (
	RegistryPrefix     = "curdx-session-"
	RegistrySuffix     = ".json"
	RegistryTTLSeconds = 7 * 24 * 60 * 60 // 7 days
)

func debugEnabled() bool {
	v := strings.TrimSpace(os.Getenv("CURDX_DEBUG"))
	return v == "1" || v == "true" || v == "yes"
}

func debug(msg string) {
	if !debugEnabled() {
		return
	}
	fmt.Fprintf(os.Stderr, "[DEBUG] %s\n", msg)
}

func registryDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".curdx", "run")
}

// RegistryPathForSession returns the path for a registry file given a session ID.
func RegistryPathForSession(sessionID string) string {
	return filepath.Join(registryDir(), RegistryPrefix+sessionID+RegistrySuffix)
}

func iterRegistryFiles() []string {
	dir := registryDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var paths []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, RegistryPrefix) && strings.HasSuffix(name, RegistrySuffix) {
			paths = append(paths, filepath.Join(dir, name))
		}
	}
	sort.Strings(paths)
	return paths
}

func coerceUpdatedAt(value interface{}, fallbackPath string) int64 {
	switch v := value.(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return i
		}
		if f, err := v.Float64(); err == nil {
			return int64(f)
		}
	case string:
		trimmed := strings.TrimSpace(v)
		if i, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
			return i
		}
	}
	if fallbackPath != "" {
		info, err := os.Stat(fallbackPath)
		if err == nil {
			return info.ModTime().Unix()
		}
	}
	return 0
}

func isStale(updatedAt int64, nowOpt ...int64) bool {
	if updatedAt <= 0 {
		return true
	}
	var now int64
	if len(nowOpt) > 0 {
		now = nowOpt[0]
	} else {
		now = time.Now().Unix()
	}
	return (now - updatedAt) > int64(RegistryTTLSeconds)
}

func loadRegistryFile(path string) map[string]interface{} {
	data, err := os.ReadFile(path)
	if err != nil {
		debug(fmt.Sprintf("Failed to read registry %s: %v", path, err))
		return nil
	}
	var obj map[string]interface{}
	if err := json.Unmarshal(data, &obj); err != nil {
		debug(fmt.Sprintf("Failed to parse registry %s: %v", path, err))
		return nil
	}
	return obj
}

func providerEntryFromLegacy(data map[string]interface{}, provider string) map[string]interface{} {
	provider = strings.TrimSpace(strings.ToLower(provider))
	out := map[string]interface{}{}

	copyIfPresent := func(srcKey, dstKey string) {
		v, ok := data[srcKey]
		if !ok {
			return
		}
		s, ok := v.(string)
		if ok && strings.TrimSpace(s) != "" {
			out[dstKey] = s
		}
	}

	switch provider {
	case "codex":
		copyIfPresent("codex_pane_id", "pane_id")
		copyIfPresent("pane_title_marker", "pane_title_marker")
		copyIfPresent("codex_session_id", "codex_session_id")
		copyIfPresent("codex_session_path", "codex_session_path")
	case "opencode":
		copyIfPresent("opencode_pane_id", "pane_id")
		copyIfPresent("pane_title_marker", "pane_title_marker")
	case "claude":
		copyIfPresent("claude_pane_id", "pane_id")
	}
	return out
}

func getProvidersMap(data map[string]interface{}) map[string]map[string]interface{} {
	pRaw, ok := data["providers"]
	if ok {
		if pMap, ok := pRaw.(map[string]interface{}); ok {
			out := map[string]map[string]interface{}{}
			for k, v := range pMap {
				if entry, ok := v.(map[string]interface{}); ok {
					key := strings.TrimSpace(strings.ToLower(k))
					cp := make(map[string]interface{}, len(entry))
					for ek, ev := range entry {
						cp[ek] = ev
					}
					out[key] = cp
				}
			}
			return out
		}
	}

	// Legacy flat format.
	out := map[string]map[string]interface{}{}
	for _, p := range []string{"codex", "opencode", "claude"} {
		entry := providerEntryFromLegacy(data, p)
		if len(entry) > 0 {
			out[p] = entry
		}
	}
	return out
}

// TerminalBackend is the interface needed by the pane registry for liveness checks.
type TerminalBackend interface {
	IsAlive(paneID string) bool
	FindPaneByTitleMarker(marker string, cwdHint ...string) string
}

// GetBackendFunc is a function type that returns a TerminalBackend for a record's terminal type.
// This is set externally to avoid circular dependencies with the terminal package.
var GetBackendFunc func(record map[string]interface{}) TerminalBackend

func providerPaneAlive(record map[string]interface{}, provider string) bool {
	baseProv, _ := providers.ParseQualifiedProvider(provider)
	provMap := getProvidersMap(record)

	// Try qualified key first, fall back to base provider.
	key := strings.TrimSpace(strings.ToLower(provider))
	entry, ok := provMap[key]
	if !ok {
		entry, ok = provMap[baseProv]
	}
	if !ok {
		return false
	}

	paneID := ""
	if v := entry["pane_id"]; v != nil {
		paneID = strings.TrimSpace(fmt.Sprintf("%v", v))
	}
	marker := ""
	if m, ok := entry["pane_title_marker"]; ok && m != nil {
		marker = strings.TrimSpace(fmt.Sprintf("%v", m))
	}

	if GetBackendFunc == nil {
		return false
	}
	backend := GetBackendFunc(record)
	if backend == nil {
		return false
	}

	// Best-effort marker resolution if pane_id is missing/stale.
	if paneID == "" || paneID == "<nil>" {
		if marker != "" {
			resolved := backend.FindPaneByTitleMarker(marker)
			paneID = strings.TrimSpace(resolved)
		}
	}

	if paneID == "" || paneID == "<nil>" {
		return false
	}

	return backend.IsAlive(paneID)
}

// LoadRegistryBySessionID loads a registry record by session ID.
func LoadRegistryBySessionID(sessionID string) map[string]interface{} {
	if sessionID == "" {
		return nil
	}
	path := RegistryPathForSession(sessionID)
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	data := loadRegistryFile(path)
	if data == nil {
		return nil
	}
	updatedAt := coerceUpdatedAt(data["updated_at"], path)
	if isStale(updatedAt) {
		debug(fmt.Sprintf("Registry stale for session %s: %s", sessionID, path))
		return nil
	}
	return data
}

// LoadRegistryByClaudePane finds a registry record matching the given claude pane_id.
func LoadRegistryByClaudePane(paneID string) map[string]interface{} {
	if paneID == "" {
		return nil
	}
	var best map[string]interface{}
	var bestTS int64 = -1

	for _, path := range iterRegistryFiles() {
		data := loadRegistryFile(path)
		if data == nil {
			continue
		}
		provMap := getProvidersMap(data)
		claudeEntry, _ := provMap["claude"]
		claudePaneID := ""
		if claudeEntry != nil {
			if v, ok := claudeEntry["pane_id"]; ok {
				claudePaneID = fmt.Sprintf("%v", v)
			}
		}
		// Also check legacy field.
		if claudePaneID == "" {
			if v, ok := data["claude_pane_id"]; ok {
				claudePaneID = fmt.Sprintf("%v", v)
			}
		}
		if claudePaneID != paneID {
			continue
		}
		updatedAt := coerceUpdatedAt(data["updated_at"], path)
		if isStale(updatedAt) {
			debug(fmt.Sprintf("Registry stale for pane %s: %s", paneID, path))
			continue
		}
		if updatedAt > bestTS {
			best = data
			bestTS = updatedAt
		}
	}
	return best
}

// LoadRegistryByProjectID loads the newest alive registry record matching project+provider.
func LoadRegistryByProjectID(curdxProjectID, provider string) map[string]interface{} {
	proj := strings.TrimSpace(curdxProjectID)
	prov := strings.TrimSpace(strings.ToLower(provider))
	if proj == "" || prov == "" {
		return nil
	}

	baseProv, _ := providers.ParseQualifiedProvider(prov)

	var best map[string]interface{}
	var bestTS int64 = -1
	bestNeedsMigration := false

	for _, path := range iterRegistryFiles() {
		data := loadRegistryFile(path)
		if data == nil {
			continue
		}
		updatedAt := coerceUpdatedAt(data["updated_at"], path)
		if isStale(updatedAt) {
			continue
		}

		existing := ""
		if v, ok := data["curdx_project_id"]; ok {
			existing = strings.TrimSpace(fmt.Sprintf("%v", v))
		}
		inferred := ""
		if existing == "" {
			if wd, ok := data["work_dir"]; ok {
				wdStr := strings.TrimSpace(fmt.Sprintf("%v", wd))
				if wdStr != "" {
					inferred = projectid.ComputeCURDXProjectID(wdStr)
				}
			}
		}
		effective := existing
		if effective == "" {
			effective = inferred
		}
		if effective != proj {
			continue
		}

		// Use base provider for pane alive check.
		if !providerPaneAlive(data, baseProv) {
			continue
		}

		if updatedAt > bestTS {
			best = data
			bestTS = updatedAt
			bestNeedsMigration = existing == "" && inferred != ""
		}
	}

	if best != nil && bestNeedsMigration {
		// Best-effort persistence: add curdx_project_id to the winning record.
		existingPID := ""
		if v, ok := best["curdx_project_id"]; ok {
			existingPID = strings.TrimSpace(fmt.Sprintf("%v", v))
		}
		if existingPID == "" {
			if wd, ok := best["work_dir"]; ok {
				wdStr := strings.TrimSpace(fmt.Sprintf("%v", wd))
				if wdStr != "" {
					best["curdx_project_id"] = projectid.ComputeCURDXProjectID(wdStr)
					_ = UpsertRegistry(best)
				}
			}
		}
	}

	return best
}

// UpsertRegistry writes (or merges) a registry record.
func UpsertRegistry(record map[string]interface{}) bool {
	sessionIDRaw, ok := record["curdx_session_id"]
	if !ok {
		debug("Registry update skipped: missing curdx_session_id")
		return false
	}
	sessionID := fmt.Sprintf("%v", sessionIDRaw)
	if strings.TrimSpace(sessionID) == "" {
		debug("Registry update skipped: empty curdx_session_id")
		return false
	}

	path := RegistryPathForSession(sessionID)
	dir := filepath.Dir(path)
	os.MkdirAll(dir, 0o755)

	data := map[string]interface{}{}
	if existing := loadRegistryFile(path); existing != nil {
		for k, v := range existing {
			data[k] = v
		}
	}

	// Normalize to the new schema.
	provs := getProvidersMap(data)

	// Accept incoming nested providers.
	if incomingProvs, ok := record["providers"]; ok {
		if ipMap, ok := incomingProvs.(map[string]interface{}); ok {
			for p, entry := range ipMap {
				eMap, ok := entry.(map[string]interface{})
				if !ok {
					continue
				}
				key := strings.TrimSpace(strings.ToLower(p))
				if _, exists := provs[key]; !exists {
					provs[key] = map[string]interface{}{}
				}
				for k, v := range eMap {
					if v == nil {
						continue
					}
					provs[key][k] = v
				}
			}
		}
	}

	// Accept explicit provider field.
	if provRaw, ok := record["provider"]; ok {
		if provStr, ok := provRaw.(string); ok && strings.TrimSpace(provStr) != "" {
			p := strings.TrimSpace(strings.ToLower(provStr))
			if _, exists := provs[p]; !exists {
				provs[p] = map[string]interface{}{}
			}
			for k, v := range record {
				if v == nil {
					continue
				}
				if k == "provider" || k == "providers" {
					continue
				}
				if k == "pane_id" || k == "pane_title_marker" ||
					strings.HasSuffix(k, "_session_id") ||
					strings.HasSuffix(k, "_session_path") ||
					strings.HasSuffix(k, "_project_id") {
					provs[p][k] = v
				}
			}
		}
	}

	// Migrate legacy flat fields.
	for _, p := range []string{"codex", "opencode", "claude"} {
		legacyEntry := providerEntryFromLegacy(record, p)
		if len(legacyEntry) > 0 {
			if _, exists := provs[p]; !exists {
				provs[p] = map[string]interface{}{}
			}
			for k, v := range legacyEntry {
				if v != nil {
					provs[p][k] = v
				}
			}
		}
	}

	// Top-level fields.
	for k, v := range record {
		if v == nil {
			continue
		}
		if k == "providers" || k == "provider" {
			continue
		}
		data[k] = v
	}

	// Convert providers map back to interface{} for JSON.
	provsIface := make(map[string]interface{}, len(provs))
	for k, v := range provs {
		provsIface[k] = v
	}
	data["providers"] = provsIface

	// Ensure curdx_project_id exists.
	pid := ""
	if v, ok := data["curdx_project_id"]; ok {
		pid = strings.TrimSpace(fmt.Sprintf("%v", v))
	}
	if pid == "" {
		if wd, ok := data["work_dir"]; ok {
			wdStr := strings.TrimSpace(fmt.Sprintf("%v", wd))
			if wdStr != "" {
				data["curdx_project_id"] = projectid.ComputeCURDXProjectID(wdStr)
			}
		}
	}

	data["updated_at"] = time.Now().Unix()

	payload, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		debug(fmt.Sprintf("Failed to marshal registry %s: %v", path, err))
		return false
	}

	if err := cliutil.AtomicWriteText(path, string(payload)); err != nil {
		debug(fmt.Sprintf("Failed to write registry %s: %v", path, err))
		return false
	}
	return true
}

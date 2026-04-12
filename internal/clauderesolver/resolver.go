// Package clauderesolver resolves Claude session information from registry and session files.
// Source: claude_code_bridge/lib/claude_session_resolver.py
package clauderesolver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/curdx/curdx-bridge/internal/paneregistry"
	"github.com/curdx/curdx-bridge/internal/projectid"
	"github.com/curdx/curdx-bridge/internal/sessionutil"
)

// SessionEnvKeys are environment variables checked for session IDs.
var SessionEnvKeys = []string{
	"CURDX_SESSION_ID",
	"CODEX_SESSION_ID",
}

// ClaudeProjectsRoot returns the root directory for Claude projects.
func ClaudeProjectsRoot() string {
	root := os.Getenv("CLAUDE_PROJECTS_ROOT")
	if root == "" {
		root = os.Getenv("CLAUDE_PROJECT_ROOT")
	}
	if root == "" {
		home, _ := os.UserHomeDir()
		root = filepath.Join(home, ".claude", "projects")
	}
	return root
}

// ClaudeSessionResolution holds the resolved session information.
type ClaudeSessionResolution struct {
	Data        map[string]any
	SessionFile string // path or empty
	Registry    map[string]any
	Source      string
}

func readJSON(path string) map[string]any {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	// Strip UTF-8 BOM
	if len(data) >= 3 && data[0] == 0xef && data[1] == 0xbb && data[2] == 0xbf {
		data = data[3:]
	}
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		return nil
	}
	return obj
}

func paneFromData(data map[string]any) string {
	if data == nil {
		return ""
	}
	if v, _ := data["pane_id"].(string); strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	if v, _ := data["claude_pane_id"].(string); strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	terminal, _ := data["terminal"].(string)
	if strings.ToLower(strings.TrimSpace(terminal)) == "tmux" {
		if v, _ := data["tmux_session"].(string); strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func sessionFileFromRecord(record map[string]any) string {
	if record == nil {
		return ""
	}
	providers, _ := record["providers"].(map[string]any)
	var pathStr string
	if providers != nil {
		if claude, _ := providers["claude"].(map[string]any); claude != nil {
			pathStr, _ = claude["session_file"].(string)
		}
	}
	if pathStr == "" {
		pathStr, _ = record["claude_session_file"].(string)
	}
	if pathStr == "" {
		pathStr, _ = record["session_file"].(string)
	}
	return pathStr
}

func dataFromRegistry(record map[string]any, fallbackWorkDir string) map[string]any {
	data := map[string]any{}
	if record == nil {
		return data
	}

	data["curdx_project_id"] = record["curdx_project_id"]
	wd, _ := record["work_dir"].(string)
	if wd == "" {
		wd = fallbackWorkDir
	}
	data["work_dir"] = wd
	data["terminal"] = record["terminal"]

	providers, _ := record["providers"].(map[string]any)
	if providers != nil {
		if claude, _ := providers["claude"].(map[string]any); claude != nil {
			if v, _ := claude["pane_id"].(string); v != "" {
				data["pane_id"] = v
			}
			if v, _ := claude["pane_title_marker"].(string); v != "" {
				data["pane_title_marker"] = v
			}
			if v, _ := claude["claude_session_id"].(string); v != "" {
				data["claude_session_id"] = v
			}
			if v, _ := claude["claude_session_path"].(string); v != "" {
				data["claude_session_path"] = v
			}
		}
	}

	// Legacy flat fields
	if v, _ := record["claude_pane_id"].(string); v != "" {
		if _, ok := data["pane_id"]; !ok {
			data["pane_id"] = v
		}
	}
	if v, _ := record["claude_session_id"].(string); v != "" {
		if _, ok := data["claude_session_id"]; !ok {
			data["claude_session_id"] = v
		}
	}
	if v, _ := record["claude_session_path"].(string); v != "" {
		if _, ok := data["claude_session_path"]; !ok {
			data["claude_session_path"] = v
		}
	}

	return data
}

var projectKeyRe = regexp.MustCompile(`[^A-Za-z0-9]`)

func projectKeyForPath(p string) string {
	return projectKeyRe.ReplaceAllString(p, "-")
}

func candidateProjectDirs(root, workDir string) []string {
	var candidates []string
	if envPWD := os.Getenv("PWD"); envPWD != "" {
		candidates = append(candidates, envPWD)
	}
	candidates = append(candidates, workDir)
	if abs, err := filepath.Abs(workDir); err == nil && abs != workDir {
		candidates = append(candidates, abs)
	}

	var out []string
	seen := map[string]bool{}
	for _, c := range candidates {
		key := projectKeyForPath(c)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, filepath.Join(root, key))
	}
	return out
}

func sessionPathFromID(sessionID, workDir string) string {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return ""
	}
	for _, projDir := range candidateProjectDirs(ClaudeProjectsRoot(), workDir) {
		candidate := filepath.Join(projDir, sid+".jsonl")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

func normalizeSessionBinding(data map[string]any, workDir string) {
	if data == nil {
		return
	}
	sid := ""
	if v, _ := data["claude_session_id"].(string); strings.TrimSpace(v) != "" {
		sid = strings.TrimSpace(v)
	} else if v, _ := data["session_id"].(string); strings.TrimSpace(v) != "" {
		sid = strings.TrimSpace(v)
	}

	pathValue, _ := data["claude_session_path"].(string)
	pathValue = strings.TrimSpace(pathValue)

	if pathValue != "" {
		if _, err := os.Stat(pathValue); err == nil {
			base := strings.TrimSuffix(filepath.Base(pathValue), filepath.Ext(pathValue))
			if sid != "" && base != sid {
				candidate := sessionPathFromID(sid, workDir)
				if candidate != "" {
					data["claude_session_path"] = candidate
				} else {
					data["claude_session_id"] = base
				}
			} else if sid == "" {
				data["claude_session_id"] = base
			}
			return
		}
	}
	if sid != "" {
		candidate := sessionPathFromID(sid, workDir)
		if candidate != "" {
			data["claude_session_path"] = candidate
		}
	}
}

func registryRunDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".curdx", "run")
}

func registryUpdatedAt(data map[string]any, path string) int {
	if v, ok := data["updated_at"].(float64); ok {
		return int(v)
	}
	if v, ok := data["updated_at"].(string); ok {
		v = strings.TrimSpace(v)
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
			return n
		}
	}
	if info, err := os.Stat(path); err == nil {
		return int(info.ModTime().Unix())
	}
	return 0
}

func loadRegistryByProjectIDUnfiltered(curdxProjectID, workDir string) map[string]any {
	if curdxProjectID == "" {
		return nil
	}
	runDir := registryRunDir()
	entries, err := os.ReadDir(runDir)
	if err != nil {
		return nil
	}

	var best map[string]any
	bestTS := -1

	var paths []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "curdx-session-") && strings.HasSuffix(e.Name(), ".json") {
			paths = append(paths, filepath.Join(runDir, e.Name()))
		}
	}
	sort.Strings(paths)

	for _, p := range paths {
		data := readJSON(p)
		if data == nil {
			continue
		}
		pid, _ := data["curdx_project_id"].(string)
		pid = strings.TrimSpace(pid)
		if pid == "" {
			if wd, _ := data["work_dir"].(string); strings.TrimSpace(wd) != "" {
				pid = projectid.ComputeCURDXProjectID(wd)
			}
		}
		if pid != curdxProjectID {
			continue
		}
		ts := registryUpdatedAt(data, p)
		if ts > bestTS {
			bestTS = ts
			best = data
		}
	}
	return best
}

// ResolveClaudeSession resolves the Claude session for the given work directory.
func ResolveClaudeSession(workDir string) *ClaudeSessionResolution {
	var bestFallback *ClaudeSessionResolution

	currentPID := projectid.ComputeCURDXProjectID(workDir)

	cfgDir := sessionutil.ResolveProjectConfigDir(workDir)
	strictProject := false
	if info, err := os.Stat(cfgDir); err == nil && info.IsDir() {
		strictProject = true
	}
	allowCross := false
	if v := os.Getenv("CURDX_ALLOW_CROSS_PROJECT_SESSION"); v == "1" || v == "true" || v == "yes" {
		allowCross = true
	}
	if !strictProject && !allowCross {
		return nil
	}

	recordProjectID := func(record map[string]any) string {
		if record == nil {
			return ""
		}
		if pid, _ := record["curdx_project_id"].(string); strings.TrimSpace(pid) != "" {
			return strings.TrimSpace(pid)
		}
		if wd, _ := record["work_dir"].(string); strings.TrimSpace(wd) != "" {
			return projectid.ComputeCURDXProjectID(wd)
		}
		return ""
	}

	consider := func(candidate *ClaudeSessionResolution) *ClaudeSessionResolution {
		if candidate == nil {
			return nil
		}
		normalizeSessionBinding(candidate.Data, workDir)
		if paneFromData(candidate.Data) != "" {
			return candidate
		}
		if bestFallback == nil {
			bestFallback = candidate
		}
		return nil
	}

	// 1) Registry via session id envs
	for _, key := range SessionEnvKeys {
		sessionID := strings.TrimSpace(os.Getenv(key))
		if sessionID == "" {
			continue
		}
		record := paneregistry.LoadRegistryBySessionID(sessionID)
		if record == nil {
			continue
		}
		if !allowCross && strictProject {
			recPID := recordProjectID(record)
			if recPID == "" || (currentPID != "" && recPID != currentPID) {
				continue
			}
		}
		data := dataFromRegistry(record, workDir)
		sf := sessionFileFromRecord(record)
		if sf == "" {
			if found := sessionutil.FindProjectSessionFile(workDir, ".claude-session"); found != "" {
				sf = found
			}
		}
		resolved := consider(&ClaudeSessionResolution{Data: data, SessionFile: sf, Registry: record, Source: "registry:" + key})
		if resolved != nil {
			return resolved
		}
		break
	}

	// 2) Registry via curdx_project_id
	pid := projectid.ComputeCURDXProjectID(workDir)
	if pid != "" {
		record := paneregistry.LoadRegistryByProjectID(pid, "claude")
		if record != nil {
			data := dataFromRegistry(record, workDir)
			sf := sessionFileFromRecord(record)
			if sf == "" {
				if found := sessionutil.FindProjectSessionFile(workDir, ".claude-session"); found != "" {
					sf = found
				}
			}
			resolved := consider(&ClaudeSessionResolution{Data: data, SessionFile: sf, Registry: record, Source: "registry:project"})
			if resolved != nil {
				return resolved
			}
		}

		unfiltered := loadRegistryByProjectIDUnfiltered(pid, workDir)
		if unfiltered != nil {
			data := dataFromRegistry(unfiltered, workDir)
			sf := sessionFileFromRecord(unfiltered)
			if sf == "" {
				if found := sessionutil.FindProjectSessionFile(workDir, ".claude-session"); found != "" {
					sf = found
				}
			}
			resolved := consider(&ClaudeSessionResolution{Data: data, SessionFile: sf, Registry: unfiltered, Source: "registry:project_unfiltered"})
			if resolved != nil {
				return resolved
			}
		}
	}

	// 3) .claude-session file
	sfPath := sessionutil.FindProjectSessionFile(workDir, ".claude-session")
	if sfPath != "" {
		data := readJSON(sfPath)
		if data != nil {
			if _, ok := data["work_dir"]; !ok {
				data["work_dir"] = workDir
			}
			normalizeSessionBinding(data, workDir)
			resolved := consider(&ClaudeSessionResolution{Data: data, SessionFile: sfPath, Source: "session_file"})
			if resolved != nil {
				return resolved
			}
		}
	}

	// 4) Registry via current pane id
	paneID := os.Getenv("WEZTERM_PANE")
	if paneID == "" {
		paneID = os.Getenv("TMUX_PANE")
	}
	paneID = strings.TrimSpace(paneID)
	if paneID != "" {
		record := paneregistry.LoadRegistryByClaudePane(paneID)
		if record != nil {
			if !allowCross && strictProject {
				recPID := recordProjectID(record)
				if recPID == "" || (currentPID != "" && recPID != currentPID) {
					record = nil
				}
			}
			if record != nil {
				data := dataFromRegistry(record, workDir)
				sf := sessionFileFromRecord(record)
				if sf == "" {
					if found := sessionutil.FindProjectSessionFile(workDir, ".claude-session"); found != "" {
						sf = found
					}
				}
				resolved := consider(&ClaudeSessionResolution{Data: data, SessionFile: sf, Registry: record, Source: "registry:pane"})
				if resolved != nil {
					return resolved
				}
			}
		}
	}

	if bestFallback != nil {
		if bestFallback.SessionFile == "" {
			cfg := sessionutil.ResolveProjectConfigDir(workDir)
			bestFallback.SessionFile = filepath.Join(cfg, ".claude-session")
		}
		return bestFallback
	}

	return nil
}

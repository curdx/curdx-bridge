// Package sessionutil provides session file permission checking and safe writing.
// Source: claude_code_bridge/lib/session_utils.py
package sessionutil

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const (
	CURDXProjectConfigDirname       = ".curdx"
	CURDXProjectConfigLegacyDirname = ".curdx_config"
)

// ProjectConfigDir returns the primary config dir for the given work directory.
func ProjectConfigDir(workDir string) string {
	abs, err := filepath.Abs(workDir)
	if err != nil {
		abs = workDir
	}
	return filepath.Join(abs, CURDXProjectConfigDirname)
}

// LegacyProjectConfigDir returns the legacy config dir for the given work directory.
func LegacyProjectConfigDir(workDir string) string {
	abs, err := filepath.Abs(workDir)
	if err != nil {
		abs = workDir
	}
	return filepath.Join(abs, CURDXProjectConfigLegacyDirname)
}

// ResolveProjectConfigDir returns the primary config dir if present;
// otherwise the legacy dir if it exists.
func ResolveProjectConfigDir(workDir string) string {
	primary := ProjectConfigDir(workDir)
	legacy := LegacyProjectConfigDir(workDir)
	primaryInfo, err := os.Stat(primary)
	if err == nil && primaryInfo.IsDir() {
		return primary
	}
	legacyInfo, err := os.Stat(legacy)
	if err == nil && legacyInfo.IsDir() {
		return legacy
	}
	return primary
}

// CheckResult holds the result of a session writable check.
type CheckResult struct {
	Writable      bool
	ErrorReason   string
	FixSuggestion string
}

// CheckSessionWritable checks if a session file is writable.
func CheckSessionWritable(sessionFile string) CheckResult {
	parent := filepath.Dir(sessionFile)

	// 1. Check if parent directory exists and is accessible
	parentInfo, err := os.Stat(parent)
	if err != nil || !parentInfo.IsDir() {
		return CheckResult{
			Writable:      false,
			ErrorReason:   fmt.Sprintf("Directory not found: %s", parent),
			FixSuggestion: fmt.Sprintf("mkdir -p %s", parent),
		}
	}

	// Check execute permission on parent
	if !checkAccess(parent, accessExecute) {
		return CheckResult{
			Writable:      false,
			ErrorReason:   fmt.Sprintf("Directory not accessible (missing x permission): %s", parent),
			FixSuggestion: fmt.Sprintf("chmod +x %s", parent),
		}
	}

	// 2. Check if parent directory is writable
	if !checkAccess(parent, accessWrite) {
		return CheckResult{
			Writable:      false,
			ErrorReason:   fmt.Sprintf("Directory not writable: %s", parent),
			FixSuggestion: fmt.Sprintf("chmod u+w %s", parent),
		}
	}

	// 3. If file doesn't exist, directory writable is enough
	fileInfo, err := os.Lstat(sessionFile)
	if os.IsNotExist(err) {
		return CheckResult{Writable: true}
	}

	// 4. Check if it's a regular file
	if fileInfo.Mode()&os.ModeSymlink != 0 {
		target, _ := filepath.EvalSymlinks(sessionFile)
		return CheckResult{
			Writable:      false,
			ErrorReason:   fmt.Sprintf("Is symlink pointing to %s", target),
			FixSuggestion: fmt.Sprintf("rm -f %s", sessionFile),
		}
	}

	if fileInfo.IsDir() {
		return CheckResult{
			Writable:      false,
			ErrorReason:   "Is directory, not file",
			FixSuggestion: fmt.Sprintf("rmdir %s or rm -rf %s", sessionFile, sessionFile),
		}
	}

	if !fileInfo.Mode().IsRegular() {
		return CheckResult{
			Writable:      false,
			ErrorReason:   "Not a regular file",
			FixSuggestion: fmt.Sprintf("rm -f %s", sessionFile),
		}
	}

	// 5. Check file ownership (platform-specific)
	if result, checked := checkFileOwnership(sessionFile, fileInfo); checked && !result.Writable {
		return result
	}

	// 6. Check if file is writable
	if !checkAccess(sessionFile, accessWrite) {
		mode := fileInfo.Mode().String()
		return CheckResult{
			Writable:      false,
			ErrorReason:   fmt.Sprintf("File not writable (mode: %s)", mode),
			FixSuggestion: fmt.Sprintf("chmod u+w %s", sessionFile),
		}
	}

	return CheckResult{Writable: true}
}

// SafeWriteSession safely writes a session file, returning a friendly error on failure.
func SafeWriteSession(sessionFile string, content string) (bool, string) {
	result := CheckSessionWritable(sessionFile)
	if !result.Writable {
		base := filepath.Base(sessionFile)
		return false, fmt.Sprintf("Cannot write %s: %s\nFix: %s", base, result.ErrorReason, result.FixSuggestion)
	}

	tmpFile := sessionFile + ".tmp"
	err := os.WriteFile(tmpFile, []byte(content), 0o644)
	if err != nil {
		os.Remove(tmpFile)
		if os.IsPermission(err) {
			base := filepath.Base(sessionFile)
			return false, fmt.Sprintf("Cannot write %s: %s\nTry: rm -f %s then retry", base, err, sessionFile)
		}
		return false, fmt.Sprintf("Write failed: %s", err)
	}

	if err := os.Rename(tmpFile, sessionFile); err != nil {
		os.Remove(tmpFile)
		if os.IsPermission(err) {
			base := filepath.Base(sessionFile)
			return false, fmt.Sprintf("Cannot write %s: %s\nTry: rm -f %s then retry", base, err, sessionFile)
		}
		return false, fmt.Sprintf("Write failed: %s", err)
	}

	return true, ""
}

// PrintSessionError outputs a session-related error to stderr (or stdout).
func PrintSessionError(msg string, toStderr bool) {
	if toStderr {
		fmt.Fprintln(os.Stderr, msg)
	} else {
		fmt.Println(msg)
	}
}

// FindProjectSessionFile finds a session file for the given work_dir.
func FindProjectSessionFile(workDir string, sessionFilename string) string {
	return findSessionFile(workDir, sessionFilename, false)
}

// FindActiveProjectSessionFile finds a session file, skipping those marked active:false.
func FindActiveProjectSessionFile(workDir string, sessionFilename string) string {
	return findSessionFile(workDir, sessionFilename, true)
}

func findSessionFile(workDir string, sessionFilename string, skipInactive bool) string {
	current, err := filepath.Abs(workDir)
	if err != nil {
		current = workDir
	}
	if resolved, err := filepath.EvalSymlinks(current); err == nil {
		current = resolved
	}

	for {
		candidates := []string{
			filepath.Join(current, CURDXProjectConfigDirname, sessionFilename),
			filepath.Join(current, CURDXProjectConfigLegacyDirname, sessionFilename),
			filepath.Join(current, sessionFilename),
		}
		for _, candidate := range candidates {
			if _, err := os.Stat(candidate); err == nil {
				if skipInactive && isSessionInactive(candidate) {
					continue
				}
				return candidate
			}
		}

		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}

	return ""
}

// isSessionInactive returns true only when the file explicitly has "active": false.
func isSessionInactive(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var obj map[string]interface{}
	if json.Unmarshal(data, &obj) != nil {
		return false
	}
	if v, ok := obj["active"]; ok {
		if b, ok := v.(bool); ok && !b {
			return true
		}
	}
	return false
}

// Package sessionutil provides session file permission checking and safe writing.
// Source: claude_code_bridge/lib/session_utils.py
package sessionutil

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
)

const (
	CCBProjectConfigDirname       = ".ccb"
	CCBProjectConfigLegacyDirname = ".ccb_config"
)

// ProjectConfigDir returns the primary config dir for the given work directory.
func ProjectConfigDir(workDir string) string {
	abs, err := filepath.Abs(workDir)
	if err != nil {
		abs = workDir
	}
	return filepath.Join(abs, CCBProjectConfigDirname)
}

// LegacyProjectConfigDir returns the legacy config dir for the given work directory.
func LegacyProjectConfigDir(workDir string) string {
	abs, err := filepath.Abs(workDir)
	if err != nil {
		abs = workDir
	}
	return filepath.Join(abs, CCBProjectConfigLegacyDirname)
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
// Returns (writable, error_reason, fix_suggestion).
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
	if err := syscall.Access(parent, 0x1); err != nil { // X_OK = 1
		return CheckResult{
			Writable:      false,
			ErrorReason:   fmt.Sprintf("Directory not accessible (missing x permission): %s", parent),
			FixSuggestion: fmt.Sprintf("chmod +x %s", parent),
		}
	}

	// 2. Check if parent directory is writable
	if err := syscall.Access(parent, 0x2); err != nil { // W_OK = 2
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

	// 5. Check file ownership (POSIX only)
	if stat, ok := fileInfo.Sys().(*syscall.Stat_t); ok {
		fileUID := stat.Uid
		currentUID := uint32(os.Getuid())
		if fileUID != currentUID {
			ownerName := strconv.FormatUint(uint64(fileUID), 10)
			if u, err := user.LookupId(ownerName); err == nil {
				ownerName = u.Username
			}
			currentName := strconv.FormatUint(uint64(currentUID), 10)
			if u, err := user.LookupId(currentName); err == nil {
				currentName = u.Username
			}
			return CheckResult{
				Writable:      false,
				ErrorReason:   fmt.Sprintf("File owned by %s (current user: %s)", ownerName, currentName),
				FixSuggestion: fmt.Sprintf("sudo chown %s:%s %s", currentName, currentName, sessionFile),
			}
		}
	}

	// 6. Check if file is writable
	if err := syscall.Access(sessionFile, 0x2); err != nil { // W_OK = 2
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
// Returns (success, error_message).
func SafeWriteSession(sessionFile string, content string) (bool, string) {
	// Pre-check
	result := CheckSessionWritable(sessionFile)
	if !result.Writable {
		base := filepath.Base(sessionFile)
		return false, fmt.Sprintf("❌ Cannot write %s: %s\n💡 Fix: %s", base, result.ErrorReason, result.FixSuggestion)
	}

	// Attempt atomic write
	tmpFile := sessionFile + ".tmp"
	err := os.WriteFile(tmpFile, []byte(content), 0o644)
	if err != nil {
		os.Remove(tmpFile)
		if os.IsPermission(err) {
			base := filepath.Base(sessionFile)
			return false, fmt.Sprintf("❌ Cannot write %s: %s\n💡 Try: rm -f %s then retry", base, err, sessionFile)
		}
		return false, fmt.Sprintf("❌ Write failed: %s", err)
	}

	if err := os.Rename(tmpFile, sessionFile); err != nil {
		os.Remove(tmpFile)
		if os.IsPermission(err) {
			base := filepath.Base(sessionFile)
			return false, fmt.Sprintf("❌ Cannot write %s: %s\n💡 Try: rm -f %s then retry", base, err, sessionFile)
		}
		return false, fmt.Sprintf("❌ Write failed: %s", err)
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
//
// Lookup walks upward from workDir to support calls from subdirectories:
//  1. <dir>/.ccb/<sessionFilename>
//  2. <dir>/.ccb_config/<sessionFilename>  (legacy)
//  3. <dir>/<sessionFilename>  (legacy)
//
// The nearest match wins. Returns "" if not found.
func FindProjectSessionFile(workDir string, sessionFilename string) string {
	current, err := filepath.Abs(workDir)
	if err != nil {
		current, err = filepath.Abs(workDir)
		if err != nil {
			current = workDir
		}
	}
	// Resolve symlinks like Python's Path.resolve()
	if resolved, err := filepath.EvalSymlinks(current); err == nil {
		current = resolved
	}

	for {
		// Check .ccb/<sessionFilename>
		candidate := filepath.Join(current, CCBProjectConfigDirname, sessionFilename)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}

		// Check .ccb_config/<sessionFilename> (legacy)
		legacyCandidate := filepath.Join(current, CCBProjectConfigLegacyDirname, sessionFilename)
		if _, err := os.Stat(legacyCandidate); err == nil {
			return legacyCandidate
		}

		// Check <dir>/<sessionFilename> (legacy)
		legacy := filepath.Join(current, sessionFilename)
		if _, err := os.Stat(legacy); err == nil {
			return legacy
		}

		// Move to parent
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}

	return ""
}

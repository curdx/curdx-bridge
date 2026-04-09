// Package projectid provides project ID computation and path normalization.
// Source: claude_code_bridge/lib/project_id.py
package projectid

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path"
	"regexp"
	"runtime"
	"strings"
)

var (
	winDriveRe  = regexp.MustCompile(`^[A-Za-z]:([/\\]|$)`)
	mntDriveRe  = regexp.MustCompile(`^/mnt/([A-Za-z])/(.*)$`)
	msysDriveRe = regexp.MustCompile(`^/([A-Za-z])/(.*)$`)
)

// NormalizeWorkDir normalizes a work_dir into a stable string for hashing and matching.
//
// Goals:
//   - Be stable within a single environment (Linux/WSL/Windows/MSYS).
//   - Reduce trivial path-format mismatches (slashes, drive letter casing, /mnt/<drive> mapping).
//   - Avoid resolve() by default to reduce symlink/interop surprises.
func NormalizeWorkDir(value string) string {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return ""
	}

	// Expand "~" early.
	if strings.HasPrefix(raw, "~") {
		home, err := os.UserHomeDir()
		if err == nil {
			if raw == "~" {
				raw = home
			} else if strings.HasPrefix(raw, "~/") || strings.HasPrefix(raw, "~\\") {
				raw = home + raw[1:]
			}
		}
	}

	// Absolutize when relative (best-effort).
	preview := strings.ReplaceAll(raw, "\\", "/")
	isAbs := strings.HasPrefix(preview, "/") ||
		strings.HasPrefix(preview, "//") ||
		strings.HasPrefix(raw, "\\\\") ||
		winDriveRe.MatchString(preview)

	if !isAbs {
		cwd, err := os.Getwd()
		if err == nil {
			raw = cwd + "/" + raw
		}
	}

	s := strings.ReplaceAll(raw, "\\", "/")

	// Map WSL mount paths to a Windows-like drive form for stable matching.
	if m := mntDriveRe.FindStringSubmatch(s); m != nil {
		drive := strings.ToLower(m[1])
		rest := m[2]
		s = drive + ":/" + rest
	} else if m := msysDriveRe.FindStringSubmatch(s); m != nil {
		// Map MSYS /c/... to c:/...
		if _, hasMSYS := os.LookupEnv("MSYSTEM"); hasMSYS || runtime.GOOS == "windows" {
			drive := strings.ToLower(m[1])
			rest := m[2]
			s = drive + ":/" + rest
		}
	}

	// Collapse redundant separators and dot segments using POSIX semantics.
	// DEVIATION: Python's posixpath.normpath preserves // prefix; Go's path.Clean does not.
	// We replicate the Python behavior here.
	if strings.HasPrefix(s, "//") {
		prefix := "//"
		rest := path.Clean(s[2:])
		rest = strings.TrimLeft(rest, "/")
		s = prefix + rest
	} else {
		s = path.Clean(s)
	}

	// Normalize Windows drive letter casing.
	if winDriveRe.MatchString(s) {
		s = strings.ToLower(s[:1]) + s[1:]
	}

	return s
}

// FindCCBConfigRoot finds a .ccb/ (or legacy .ccb_config/) directory
// in the given directory only (no ancestor traversal).
func FindCCBConfigRoot(startDir string) string {
	current := startDir
	if current == "" {
		var err error
		current, err = os.Getwd()
		if err != nil {
			return ""
		}
	}

	// Expand ~ and absolutize
	if strings.HasPrefix(current, "~") {
		home, err := os.UserHomeDir()
		if err == nil {
			if current == "~" {
				current = home
			} else if strings.HasPrefix(current, "~/") {
				current = home + current[1:]
			}
		}
	}

	// Make absolute
	if !strings.HasPrefix(current, "/") && !winDriveRe.MatchString(current) {
		cwd, err := os.Getwd()
		if err == nil {
			current = cwd + "/" + current
		}
	}

	// Check for .ccb directory
	ccb := current + "/.ccb"
	if info, err := os.Stat(ccb); err == nil && info.IsDir() {
		return current
	}

	// Check for legacy .ccb_config directory
	legacy := current + "/.ccb_config"
	if info, err := os.Stat(legacy); err == nil && info.IsDir() {
		return current
	}

	return ""
}

// ComputeCCBProjectID computes CCB's routing project id.
//
// Priority:
//   - Current directory containing .ccb/ (project anchor).
//   - Current work_dir (fallback).
func ComputeCCBProjectID(workDir string) string {
	wd := workDir
	if wd == "" {
		var err error
		wd, err = os.Getwd()
		if err != nil {
			wd = "."
		}
	}

	// Priority 1: Current directory .ccb/ only
	base := FindCCBConfigRoot(wd)
	if base == "" {
		base = wd
	}

	norm := NormalizeWorkDir(base)
	if norm == "" {
		norm = NormalizeWorkDir(wd)
	}

	hash := sha256.Sum256([]byte(norm))
	return fmt.Sprintf("%x", hash)
}

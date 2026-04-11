//go:build windows

package sessionutil

import (
	"os"
)

const (
	accessWrite   = 0x2
	accessExecute = 0x1
)

// checkAccess on Windows tries to open the path to verify access.
func checkAccess(path string, mode uint32) bool {
	if mode == accessExecute {
		// On Windows, directories are always "executable" (traversable).
		_, err := os.Stat(path)
		return err == nil
	}
	// Write check: try opening for write.
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		// path might be a directory; try creating a temp file inside it.
		info, statErr := os.Stat(path)
		if statErr == nil && info.IsDir() {
			tmp, tmpErr := os.CreateTemp(path, ".curdx-access-check-*")
			if tmpErr != nil {
				return false
			}
			name := tmp.Name()
			tmp.Close()
			os.Remove(name)
			return true
		}
		return false
	}
	f.Close()
	return true
}

// checkFileOwnership is a no-op on Windows (no POSIX uid concept).
func checkFileOwnership(path string, info os.FileInfo) (CheckResult, bool) {
	return CheckResult{}, false
}

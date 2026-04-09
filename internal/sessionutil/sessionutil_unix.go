//go:build !windows

package sessionutil

import (
	"fmt"
	"os"
	"os/user"
	"strconv"
	"syscall"
)

const (
	accessWrite   = 0x2 // W_OK
	accessExecute = 0x1 // X_OK
)

// checkAccess checks file/dir accessibility using syscall.Access.
func checkAccess(path string, mode uint32) bool {
	return syscall.Access(path, mode) == nil
}

// checkFileOwnership checks POSIX file ownership.
// Returns (result, checked). If checked is false, no ownership issue was found.
func checkFileOwnership(path string, info os.FileInfo) (CheckResult, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return CheckResult{}, false
	}
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
			FixSuggestion: fmt.Sprintf("sudo chown %s:%s %s", currentName, currentName, path),
		}, true
	}
	return CheckResult{}, false
}

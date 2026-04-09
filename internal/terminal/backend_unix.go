//go:build !windows

package terminal

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// currentTTYPlatform returns the tty name for the first available fd (0,1,2).
func currentTTYPlatform() string {
	for _, fd := range []int{0, 1, 2} {
		name := ttyname(fd)
		if name != "" {
			return name
		}
	}
	return ""
}

func isatty(fd int) bool {
	var termios syscall.Termios
	_, _, err := syscall.Syscall6(
		syscall.SYS_IOCTL,
		uintptr(fd),
		ioctlReadTermios,
		uintptr(unsafe.Pointer(&termios)),
		0, 0, 0,
	)
	return err == 0
}

func ttyname(fd int) string {
	if !isatty(fd) {
		return ""
	}
	// Read the /proc/self/fd/<N> symlink (Linux) or /dev/fd/<N> (macOS).
	for _, prefix := range []string{"/proc/self/fd", "/dev/fd"} {
		link := fmt.Sprintf("%s/%d", prefix, fd)
		target, err := os.Readlink(link)
		if err == nil && target != "" {
			return target
		}
	}
	return ""
}

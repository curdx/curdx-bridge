//go:build darwin

package terminal

import "syscall"

const ioctlReadTermios = syscall.TIOCGETA

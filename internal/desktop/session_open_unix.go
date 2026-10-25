//go:build !windows

package desktop

import "syscall"

func nonBlockFlag() int { return syscall.O_NONBLOCK }

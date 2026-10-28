//go:build darwin || freebsd || openbsd || netbsd || dragonfly

package pty

import "golang.org/x/sys/unix"

const inputTermiosGet = unix.TIOCGETA

// POSIX terminal disabled-character value from the platform unistd.h.
const inputEOFDisabled = 255

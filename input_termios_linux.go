package pty

import "golang.org/x/sys/unix"

const inputTermiosGet = unix.TCGETS

// POSIX terminal disabled-character value from the platform unistd.h.
const inputEOFDisabled = 0

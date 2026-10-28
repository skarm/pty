package pty_test

import "golang.org/x/sys/unix"

const (
	testTermiosGet = unix.TIOCGETA
	testTermiosSet = unix.TIOCSETA
)

const testEOFDisabled = 255

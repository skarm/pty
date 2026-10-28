package pty_test

import "golang.org/x/sys/unix"

const (
	testTermiosGet  = unix.TCGETS
	testTermiosSet  = unix.TCSETS
	testEOFDisabled = 0
)

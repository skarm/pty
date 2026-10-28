package pty

import "syscall"

const (
	termiosGet = syscall.TIOCGETA
	termiosSet = syscall.TIOCSETA
)

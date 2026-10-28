package pty

import "syscall"

const (
	termiosGet = syscall.TCGETS
	termiosSet = syscall.TCSETS
)

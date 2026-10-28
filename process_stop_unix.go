//go:build !windows && !plan9 && !aix

package pty

import (
	"os"
	"syscall"
)

func terminateProcess(process *os.Process) error { return process.Signal(syscall.SIGTERM) }

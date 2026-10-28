//go:build !windows && !plan9 && !aix

package pty

import (
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

func (t *unixTerminal) nativeTestFiles() (*os.File, *os.File) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.master, t.slave
}

func (t *unixTerminal) nativeTestSize() (Winsize, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	var size Winsize
	err := getsize(t.slave, &size)
	return size, err
}

// TestTerminalCloseOnExec checks kernel descriptor flags before any process starts.
func TestTerminalCloseOnExec(t *testing.T) {
	terminal, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = terminal.Close() })
	master, slave := NativeFiles(terminal)
	for name, file := range map[string]*os.File{"master": master, "slave": slave} {
		t.Run(name, func(t *testing.T) {
			raw, err := file.SyscallConn()
			if err != nil {
				t.Fatal(err)
			}
			var flags int
			var operationErr error
			if err := raw.Control(func(fd uintptr) {
				flags, operationErr = unix.FcntlInt(fd, unix.F_GETFD, 0)
			}); err != nil {
				t.Fatal(err)
			}
			if operationErr != nil {
				t.Fatal(operationErr)
			}
			if flags&unix.FD_CLOEXEC == 0 {
				t.Fatalf("%s descriptor is inherited across exec", name)
			}
		})
	}
}

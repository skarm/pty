//go:build linux || darwin

package pty

import (
	"errors"
	"os"
	"syscall"
	"unsafe"
)

func (t *unixTerminal) SetMode(mode Mode) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return os.ErrClosed
	}

	if t.started {
		return errors.New("pty: mode must be set before start")
	}

	var settings syscall.Termios
	// ioctl synchronously reads into the live termios value.
	if err := ioctl(t.slave, termiosGet, unsafe.Pointer(&settings)); err != nil { // #nosec G103 -- Typed termios storage remains live during ioctl.
		return err
	}

	if mode.Echo {
		settings.Lflag |= syscall.ECHO
	} else {
		settings.Lflag &^= syscall.ECHO
	}

	if mode.Canonical {
		settings.Lflag |= syscall.ICANON
	} else {
		settings.Lflag &^= syscall.ICANON
		settings.Cc[syscall.VMIN] = 1
		settings.Cc[syscall.VTIME] = 0
	}

	return ioctl(t.slave, termiosSet, unsafe.Pointer(&settings)) // #nosec G103 -- Typed termios storage remains live during ioctl.
}

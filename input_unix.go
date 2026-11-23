//go:build darwin || linux || freebsd || openbsd || netbsd || dragonfly || solaris

package pty

import (
	"errors"
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

func (t *unixTerminal) interrupt() error {
	return t.controlInput(func(fd int) error {
		// TIOCGPGRP writes a 32-bit pid_t even on 64-bit kernels.
		var group int32
		if err := ioctlInner(uintptr(fd), unix.TIOCGPGRP, unsafe.Pointer(&group)); err != nil { // #nosec G103 -- Live pid_t storage and a descriptor held by controlInput.
			return err
		}

		caller, err := unix.Getpgid(0)
		if err != nil {
			return err
		}

		if group <= 0 || int(group) == caller {
			return os.ErrInvalid
		}

		return unix.Kill(-int(group), unix.SIGINT)
	})
}

func (t *unixTerminal) eofByte() (byte, error) {
	var value byte

	err := t.controlInput(func(fd int) error {
		settings, err := unix.IoctlGetTermios(fd, inputTermiosGet)
		if err != nil {
			return err
		}

		if settings.Lflag&unix.ICANON == 0 || settings.Cc[unix.VEOF] == inputEOFDisabled {
			return ErrUnsupported
		}

		value = settings.Cc[unix.VEOF]

		return nil
	})

	return value, err
}

func (t *unixTerminal) controlInput(operation func(int) error) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return os.ErrClosed
	}

	// RawConn holds the descriptor for the entire ioctl/signal operation.
	raw, err := t.master.SyscallConn()
	if err != nil {
		return err
	}

	var operationErr error

	err = raw.Control(func(fd uintptr) { operationErr = operation(int(fd)) })

	return errors.Join(err, operationErr)
}

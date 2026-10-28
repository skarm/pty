//go:build darwin

package pty

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// Darwin can race concurrent /dev/ptmx opens for the same minor number.
// Serialize only allocation; setup and I/O remain independent.
//
//nolint:gochecknoglobals // All callers must share the Darwin master-allocation lock.
var ptmxOpenMu sync.Mutex

func open() (pty, tty *os.File, err error) {
	ptmxOpenMu.Lock()
	p, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	ptmxOpenMu.Unlock()

	if err != nil {
		return nil, nil, err
	}

	defer func() {
		if err != nil {
			_ = p.Close()
		}
	}()

	if err = requirePollableMaster(p); err != nil {
		return nil, nil, err
	}

	sname, err := ptsname(p)
	if err != nil {
		return nil, nil, err
	}

	if err := grantpt(p); err != nil {
		return nil, nil, err
	}

	if err := unlockpt(p); err != nil {
		return nil, nil, err
	}

	// #nosec G304 -- sname comes from kernel TIOCPTYGNAME and is checked for NUL termination.
	t, err := os.OpenFile(sname, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, nil, err
	}
	// The slave is already open, so its default termios settings are initialized.
	// Close it as well as the master if configuration fails.

	defer func() {
		if err != nil {
			_ = t.Close()
		}
	}()

	var settings syscall.Termios

	//nolint:gosec // Typed Termios pointer is kept until the syscall argument conversion.
	if err = ioctl(t, syscall.TIOCGETA, unsafe.Pointer(&settings)); err != nil {
		return nil, nil, err
	}
	// Canonical erase must remove a complete UTF-8 character, not its last byte.
	settings.Iflag |= syscall.IUTF8

	//nolint:gosec // Typed Termios pointer is kept until the syscall argument conversion.
	if err = ioctl(t, syscall.TIOCSETA, unsafe.Pointer(&settings)); err != nil {
		return nil, nil, err
	}

	return p, t, nil
}

// os.OpenFile may silently fall back to blocking I/O if poll registration fails.
// An empty deadline checks poll support without imposing an I/O timeout.
func requirePollableMaster(master *os.File) error {
	if err := master.SetDeadline(time.Time{}); err != nil {
		return fmt.Errorf("pty: Darwin master is not pollable: %w", err)
	}

	return nil
}

func ptsname(f *os.File) (string, error) {
	// BSD ioctl encodes the parameter length in bits 16 through 28.
	const paramMask = (1 << 13) - 1

	n := make([]byte, (syscall.TIOCPTYGNAME>>16)&paramMask)

	// #nosec G103 -- n is a live output buffer; ioctl keeps a typed pointer until syscall conversion.
	err := ioctl(f, syscall.TIOCPTYGNAME, unsafe.Pointer(&n[0]))
	if err != nil {
		return "", err
	}

	for i, c := range n {
		if c == 0 {
			return string(n[:i]), nil
		}
	}

	return "", errors.New("TIOCPTYGNAME string not NUL-terminated")
}

func grantpt(f *os.File) error {
	return ioctl(f, syscall.TIOCPTYGRANT, nil)
}

func unlockpt(f *os.File) error {
	return ioctl(f, syscall.TIOCPTYUNLK, nil)
}

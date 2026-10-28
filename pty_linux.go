//go:build linux
// +build linux

package pty

import (
	"os"
	"strconv"
	"syscall"
	"unsafe"
)

func open() (pty, tty *os.File, err error) {
	p, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		return nil, nil, err
	}

	defer func() {
		if err != nil {
			_ = p.Close()
		}
	}()

	sname, err := ptsname(p)
	if err != nil {
		return nil, nil, err
	}

	if err := unlockpt(p); err != nil {
		return nil, nil, err
	}

	t, err := os.OpenFile(sname, os.O_RDWR|syscall.O_NOCTTY, 0) // #nosec G304 -- sname is constructed from the PTY index returned by the kernel.
	if err != nil {
		return nil, nil, err
	}

	return p, t, nil
}

func ptsname(f *os.File) (string, error) {
	var n _C_uint

	err := ioctl(f, syscall.TIOCGPTN, unsafe.Pointer(&n)) // #nosec G103 -- Live typed PTY index buffer passed synchronously to ioctl.
	if err != nil {
		return "", err
	}

	return "/dev/pts/" + strconv.Itoa(int(n)), nil
}

func unlockpt(f *os.File) error {
	var u _C_int
	// TIOCSPTLCK reads an int; passing zero unlocks the slave.
	return ioctl(f, syscall.TIOCSPTLCK, unsafe.Pointer(&u)) // #nosec G103 -- Live typed lock flag passed synchronously to ioctl.
}

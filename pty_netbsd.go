//go:build netbsd
// +build netbsd

package pty

import (
	"errors"
	"os"
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

	if err := grantpt(p); err != nil {
		return nil, nil, err
	}

	// In NetBSD unlockpt() does nothing, so it isn't called here.

	t, err := os.OpenFile(sname, os.O_RDWR|syscall.O_NOCTTY, 0) // #nosec G304 -- TIOCPTSNAME supplies the kernel slave path, checked for NUL termination.
	if err != nil {
		return nil, nil, err
	}

	return p, t, nil
}

func ptsname(f *os.File) (string, error) {
	// TIOCPTSNAME returns the slave path in ptmget.Sn; see ptsname(3).
	var ptm ptmget
	if err := ioctl(f, uintptr(ioctlTIOCPTSNAME), unsafe.Pointer(&ptm)); err != nil { // #nosec G103 -- Synchronous ioctl fills this live ptmget ABI struct.
		return "", err
	}

	name := make([]byte, len(ptm.Sn))
	for i, c := range ptm.Sn {
		name[i] = byte(c) // #nosec G115 -- Preserve each signed C char byte without changing its bit pattern.
		if c == 0 {
			return string(name[:i]), nil
		}
	}

	return "", errors.New("TIOCPTSNAME string not NUL-terminated")
}

func grantpt(f *os.File) error {
	return ioctl(f, uintptr(ioctlTIOCGRANTPT), nil)
}

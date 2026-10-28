//go:build dragonfly
// +build dragonfly

package pty

import (
	"errors"
	"os"
	"strings"
	"syscall"
	"unsafe"
)

// _IOW('f', 120, struct fiodgname_arg): BSD input bit, length, group and number.
const ioctlFiodgname = 0x80000000 | (unsafe.Sizeof(fiodgnameArg{})&0x1fff)<<16 | 'f'<<8 | 120

func open() (pty, tty *os.File, err error) {
	// DragonFly allocates a unique minor under the kernel's devfs bitmap lock.
	// sys/kern/tty_pty.c:ptyclone, sys/vfs/devfs/devfs_helper.c:devfs_clone_bitmap_get.
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

	if err := unlockpt(p); err != nil {
		return nil, nil, err
	}

	// #nosec G304 -- sname is derived from the kernel FIODGNAME response and checked for NUL termination.
	t, err := os.OpenFile(sname, os.O_RDWR, 0)
	if err != nil {
		return nil, nil, err
	}

	return p, t, nil
}

func ptsname(f *os.File) (string, error) {
	name := make([]byte, _C_SPECNAMELEN)
	fa := fiodgnameArg{Name: &name[0], Len: _C_SPECNAMELEN}

	err := ioctl(f, ioctlFiodgname, unsafe.Pointer(&fa)) // #nosec G103 -- Synchronous ioctl with a live ABI struct and name buffer.
	if err != nil {
		return "", err
	}

	for i, c := range name {
		if c == 0 {
			s := "/dev/" + string(name[:i])
			return strings.ReplaceAll(s, "ptm", "pts"), nil
		}
	}

	return "", errors.New("FIODGNAME string not NUL-terminated")
}

func grantpt(f *os.File) error {
	_, err := isptmaster(f)
	return err
}

func unlockpt(f *os.File) error {
	_, err := isptmaster(f)
	return err
}

func isptmaster(f *os.File) (bool, error) {
	err := ioctl(f, syscall.TIOCISPTMASTER, nil)
	return err == nil, err
}

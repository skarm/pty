//go:build freebsd
// +build freebsd

package pty

import (
	"errors"
	"os"
	"syscall"
	"unsafe"
)

// _IOW('f', 120, struct fiodgname_arg): BSD input bit, length, group and number.
const ioctlFiodgname = 0x80000000 | (unsafe.Sizeof(fiodgnameArg{})&0x1fff)<<16 | 'f'<<8 | 120

func open() (pty, tty *os.File, err error) {
	fd, err := posixOpenpt(syscall.O_RDWR | syscall.O_CLOEXEC)
	if err != nil {
		return nil, nil, err
	}

	// posix_openpt rejects O_NONBLOCK. Set it before NewFile so the master
	// joins Go's poller and Close can interrupt an active read.
	if err := syscall.SetNonblock(fd, true); err != nil {
		_ = syscall.Close(fd)

		return nil, nil, err
	}

	p := os.NewFile(uintptr(fd), "/dev/pts")

	defer func() {
		if err != nil {
			_ = p.Close()
		}
	}()

	sname, err := ptsname(p)
	if err != nil {
		return nil, nil, err
	}

	t, err := os.OpenFile("/dev/"+sname, os.O_RDWR, 0) // #nosec G304 -- FIODGNAME supplies the kernel device name, checked for NUL termination.
	if err != nil {
		return nil, nil, err
	}

	return p, t, nil
}

func posixOpenpt(oflag int) (fd int, err error) {
	r0, _, e1 := syscall.Syscall(syscall.SYS_POSIX_OPENPT, uintptr(oflag), 0, 0)
	fd = int(r0)

	if e1 != 0 {
		err = e1
	}

	return fd, err
}

func ptsname(f *os.File) (string, error) {
	master, err := isptmaster(f)
	if err != nil {
		return "", err
	}

	if !master {
		return "", syscall.EINVAL
	}

	const n = _C_SPECNAMELEN + 1

	var (
		buf = make([]byte, n)
		arg = fiodgnameArg{Len: n, Buf: &buf[0]}
	)
	if err := ioctl(f, ioctlFiodgname, unsafe.Pointer(&arg)); err != nil { // #nosec G103 -- Synchronous ioctl fills the live ABI struct and name buffer.
		return "", err
	}

	for i, c := range buf {
		if c == 0 {
			return string(buf[:i]), nil
		}
	}

	return "", errors.New("FIODGNAME string not NUL-terminated")
}

func isptmaster(f *os.File) (bool, error) {
	err := ioctl(f, syscall.TIOCPTMASTER, nil)
	return err == nil, err
}

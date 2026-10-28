//go:build !windows && !plan9 && !aix

package pty

import (
	"os"
	"unsafe"
)

func ioctl(f *os.File, cmd uintptr, ptr unsafe.Pointer) error {
	sc, e := f.SyscallConn()
	if e != nil {
		return e
	}

	// Control invokes the callback synchronously while keeping the fd valid.
	var operationErr error

	e = sc.Control(func(fd uintptr) { operationErr = ioctlInner(fd, cmd, ptr) })
	if e != nil {
		return e
	}

	return operationErr
}

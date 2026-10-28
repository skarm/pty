//go:build !windows && !solaris && !aix && !plan9

package pty

import (
	"syscall"
	"unsafe"
)

func ioctlInner(fd, cmd uintptr, ptr unsafe.Pointer) error {
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, cmd, uintptr(ptr))
	if e != 0 {
		return e
	}

	return nil
}

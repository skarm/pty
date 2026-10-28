//go:build solaris
// +build solaris

package pty

import (
	"os"
	"syscall"
	"unsafe"
)

//go:cgo_import_dynamic libc_ioctl ioctl "libc.so"
//go:linkname procioctl libc_ioctl
var procioctl uintptr //nolint:gochecknoglobals // Dynamic linker supplies the libc ioctl address.

const (
	// STREAMS ABI names from /usr/include/sys/stropts.h.
	ioctlPush = uintptr(int32('S')<<8 | 0o02)
	ioctlStr  = uintptr(int32('S')<<8 | 0o10)
	ioctlFind = uintptr(int32('S')<<8 | 0o13)

	// See /usr/include/sys/ptms.h.
	isptm   = (int32('P') << 8) | 1
	unlkpt  = (int32('P') << 8) | 2
	ownerpt = (int32('P') << 8) | 5
)

type strioctl struct {
	icCmd     int32
	icTimeout int32
	icLen     int32
	icDP      unsafe.Pointer
}

// Defined in asm_solaris_amd64.s.
func sysvicall6(trap, nargs, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2 uintptr, err syscall.Errno)

// ioctlResult preserves the return value used by STREAMS I_FIND.
func ioctlResult(f *os.File, cmd uintptr, ptr unsafe.Pointer) (uintptr, error) {
	sc, err := f.SyscallConn()
	if err != nil {
		return 0, err
	}

	var (
		result       uintptr
		operationErr error
	)

	if err := sc.Control(func(fd uintptr) {
		result, operationErr = ioctlInnerResult(fd, cmd, ptr)
	}); err != nil {
		return 0, err
	}

	return result, operationErr
}

func ioctlInner(fd, cmd uintptr, ptr unsafe.Pointer) error {
	_, err := ioctlInnerResult(fd, cmd, ptr)
	return err
}

func ioctlInnerResult(fd, cmd uintptr, ptr unsafe.Pointer) (uintptr, error) {
	result, _, errno := sysvicall6(uintptr(unsafe.Pointer(&procioctl)), 3, fd, cmd, uintptr(ptr), 0, 0, 0) // #nosec G103 -- libc symbol and typed ioctl argument remain live across the assembly call.
	if errno != 0 {
		return 0, errno
	}

	return result, nil
}

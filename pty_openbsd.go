//go:build openbsd
// +build openbsd

package pty

import (
	"os"
	"syscall"
	"unsafe"
)

func open() (pty, tty *os.File, err error) {
	// PTMGET opens both endpoints, sets ownership and revokes access granted
	// to previous users; see ptm(4).
	p, err := os.OpenFile("/dev/ptm", os.O_RDWR|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = p.Close() }()

	var ptm ptmget
	// PTMGET returns inheritable descriptors independently of /dev/ptm flags.
	// Keep Go forks out of the allocation-to-CLOEXEC window.
	syscall.ForkLock.RLock()

	err = ioctl(p, uintptr(ioctlPTMGET), unsafe.Pointer(&ptm)) // #nosec G103 -- Synchronous PTMGET writes this live ABI struct; ioctl pins the descriptor.
	if err == nil {
		syscall.CloseOnExec(int(ptm.Cfd))
		syscall.CloseOnExec(int(ptm.Sfd))
	}

	syscall.ForkLock.RUnlock()

	if err != nil {
		return nil, nil, err
	}

	// Register the master with Go's poller instead of leaving blocking I/O
	// alive after Close while a child still owns the slave.
	if err := syscall.SetNonblock(int(ptm.Cfd), true); err != nil {
		_ = syscall.Close(int(ptm.Cfd))
		_ = syscall.Close(int(ptm.Sfd))

		return nil, nil, err
	}

	pty = os.NewFile(uintptr(ptm.Cfd), cInt8ToString(ptm.Cn[:])) // #nosec G115 -- Successful PTMGET returns nonnegative int32 file descriptors.
	tty = os.NewFile(uintptr(ptm.Sfd), cInt8ToString(ptm.Sn[:])) // #nosec G115 -- Successful PTMGET returns nonnegative int32 file descriptors.

	return pty, tty, nil
}

func cInt8ToString(in []int8) string {
	var s []byte

	for _, v := range in {
		if v == 0 {
			break
		}

		s = append(s, byte(v)) // #nosec G115 -- Reinterpret signed C char bytes without changing their bit pattern.
	}

	return string(s)
}

//go:build zos
// +build zos

package pty

import (
	"os"
	"runtime"
	"syscall"
	"unsafe"
)

const (
	sysUnlockpt    = 0x37B
	sysGrantpt     = 0x37A
	sysPosixOpenpt = 0xC66
	sysFcntl       = 0x18C
	sysPtsname     = 0x718

	setCvtOn    = 1
	fControlCvt = 13
)

type f_cnvrt struct {
	Cvtcmd int32
	Pccsid int16
	Fccsid int16
}

func open() (pty, tty *os.File, err error) {
	ptmxfd, err := openpt(os.O_RDWR | syscall.O_NOCTTY)
	if err != nil {
		return nil, nil, err
	}

	// Needed for z/OS so that the characters are not garbled if ptyp* is untagged
	cvtreq := f_cnvrt{Cvtcmd: setCvtOn, Pccsid: 0, Fccsid: 1047}
	if _, err = fcntl(uintptr(ptmxfd), fControlCvt, uintptr(unsafe.Pointer(&cvtreq))); err != nil {
		_ = syscall.Close(ptmxfd) // No os.File owns this descriptor yet.
		return nil, nil, err
	}

	p := os.NewFile(uintptr(ptmxfd), "/dev/ptmx")
	if p == nil {
		return nil, nil, err
	}

	defer func() {
		if err != nil {
			_ = p.Close()
		}
	}()

	sname, err := ptsname(ptmxfd)
	if err != nil {
		return nil, nil, err
	}

	_, err = grantpt(ptmxfd)
	if err != nil {
		return nil, nil, err
	}

	if _, err = unlockpt(ptmxfd); err != nil {
		return nil, nil, err
	}

	ptsfd, err := syscall.Open(sname, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, nil, err
	}

	if _, err = fcntl(uintptr(ptsfd), fControlCvt, uintptr(unsafe.Pointer(&cvtreq))); err != nil {
		_ = syscall.Close(ptsfd) // The deferred cleanup closes the master.
		return nil, nil, err
	}

	t := os.NewFile(uintptr(ptsfd), sname)
	if err != nil {
		return nil, nil, err
	}

	return p, t, nil
}

func openpt(oflag int) (fd int, err error) {
	r0, _, e1 := runtime.CallLeFuncWithErr(runtime.GetZosLibVec()+sysPosixOpenpt<<4, uintptr(oflag))
	fd = int(r0)
	if e1 != 0 {
		err = syscall.Errno(e1)
	}
	return
}

func fcntl(fd uintptr, cmd int, arg uintptr) (val int, err error) {
	r0, _, e1 := runtime.CallLeFuncWithErr(runtime.GetZosLibVec()+sysFcntl<<4, uintptr(fd), uintptr(cmd), arg)
	val = int(r0)
	if e1 != 0 {
		err = syscall.Errno(e1)
	}
	return
}

func ptsname(fd int) (name string, err error) {
	r0, _, e1 := runtime.CallLeFuncWithPtrReturn(runtime.GetZosLibVec()+sysPtsname<<4, uintptr(fd))
	if e1 != 0 {
		return "", syscall.Errno(e1)
	}

	return u2s(unsafe.Pointer(r0)), nil
}

func grantpt(fildes int) (rc int, err error) {
	r0, _, e1 := runtime.CallLeFuncWithErr(runtime.GetZosLibVec()+sysGrantpt<<4, uintptr(fildes))
	rc = int(r0)
	if e1 != 0 {
		err = syscall.Errno(e1)
	}
	return
}

func unlockpt(fildes int) (rc int, err error) {
	r0, _, e1 := runtime.CallLeFuncWithErr(runtime.GetZosLibVec()+sysUnlockpt<<4, uintptr(fildes))
	rc = int(r0)
	if e1 != 0 {
		err = syscall.Errno(e1)
	}
	return
}

func u2s(cstr unsafe.Pointer) string {
	str := (*[1024]uint8)(cstr)
	i := 0
	for str[i] != 0 {
		i++
	}
	return string(str[:i])
}

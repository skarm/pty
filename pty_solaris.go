//go:build solaris
// +build solaris

package pty

// Based on the illumos PTY implementation:
// https://github.com/illumos/illumos-gate/blob/master/usr/src/lib/libc/port/gen/pt.c

import (
	"os"
	"strconv"
	"syscall"
	"unsafe"
)

type ptOwn struct {
	rUID uint32
	rGID uint32
}

func open() (pty, tty *os.File, err error) {
	ptmxfd, err := syscall.Open("/dev/ptmx", syscall.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, err
	}

	p := os.NewFile(uintptr(ptmxfd), "/dev/ptmx")

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

	ptsfd, err := syscall.Open(sname, os.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, err
	}

	t := os.NewFile(uintptr(ptsfd), sname)

	defer func() {
		if err != nil {
			_ = t.Close()
		}
	}()

	// Install the terminal STREAMS modules required by pts(7).
	for _, mod := range []string{"ptem", "ldterm", "ttcompat"} {
		if err := streamsPush(t, mod); err != nil {
			return nil, nil, err
		}
	}

	return p, t, nil
}

func ptsname(f *os.File) (string, error) {
	dev, err := ptsdev(f)
	if err != nil {
		return "", err
	}

	fn := "/dev/pts/" + strconv.FormatUint(dev, 10)

	if err := syscall.Access(fn, 0); err != nil {
		return "", err
	}

	return fn, nil
}

func grantpt(f *os.File) error {
	if _, err := ptsdev(f); err != nil {
		return err
	}

	pto := ptOwn{
		rUID: uint32(os.Getuid()), // #nosec G115 -- Solaris getuid returns a 32-bit uid_t.
		// Keep the caller's group; resolving libc's "tty" group requires NSS.
		rGID: uint32(os.Getgid()), // #nosec G115 -- Solaris getgid returns a 32-bit gid_t.
	}
	istr := strioctl{
		icCmd:     ownerpt,
		icTimeout: 0,
		icLen:     int32(unsafe.Sizeof(pto)),
		icDP:      unsafe.Pointer(&pto), // #nosec G103 -- OWNERPT reads exactly icLen bytes from this live payload.
	}

	return ioctl(f, ioctlStr, unsafe.Pointer(&istr)) // #nosec G103 -- Synchronous ioctl with a live strioctl argument.
}

func unlockpt(f *os.File) error {
	istr := strioctl{
		icCmd:     unlkpt,
		icTimeout: 0,
		icLen:     0,
		icDP:      nil,
	}

	return ioctl(f, ioctlStr, unsafe.Pointer(&istr)) // #nosec G103 -- Synchronous ioctl with a live strioctl argument.
}

// streamsPush installs a STREAMS module unless it is already on the stream.
func streamsPush(f *os.File, mod string) error {
	buf, err := syscall.BytePtrFromString(mod)
	if err != nil {
		return err
	}

	// I_FIND returns 1 for a module already on the stream, not an errno.
	// Duplicate terminal modules can break window-size ioctls:
	// https://www.illumos.org/issues/9042
	found, err := ioctlResult(f, ioctlFind, unsafe.Pointer(buf)) // #nosec G103 -- Live NUL-terminated module name for synchronous ioctl.
	if err != nil {
		return err
	}

	if found != 0 {
		return nil
	}

	return ioctl(f, ioctlPush, unsafe.Pointer(buf)) // #nosec G103 -- Live NUL-terminated module name for synchronous ioctl.
}

func ptsdev(f *os.File) (uint64, error) {
	istr := strioctl{
		icCmd:     isptm,
		icTimeout: 0,
		icLen:     0,
		icDP:      nil,
	}

	if err := ioctl(f, ioctlStr, unsafe.Pointer(&istr)); err != nil { // #nosec G103 -- Synchronous ioctl with a live strioctl argument.
		return 0, err
	}

	sc, err := f.SyscallConn()
	if err != nil {
		return 0, err
	}

	var (
		status       syscall.Stat_t
		operationErr error
	)

	if err := sc.Control(func(fd uintptr) {
		operationErr = syscall.Fstat(int(fd), &status)
	}); err != nil {
		return 0, err
	}

	if operationErr != nil {
		return 0, operationErr
	}

	return minor(status.Rdev), nil
}

// Solaris/amd64 dev_t contains a 32-bit minor number (sys/mkdev.h).
func minor(x uint64) uint64 { return x & 0xffffffff }

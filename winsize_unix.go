//go:build !windows && !plan9 && !aix

package pty

import (
	"os"
	"syscall"
	"unsafe"
)

func setsize(file *os.File, size *Winsize) error {
	return ioctl(file, syscall.TIOCSWINSZ, unsafe.Pointer(size)) // #nosec G103 -- Live typed winsize passed synchronously to ioctl.
}

func getsize(file *os.File, size *Winsize) error {
	return ioctl(file, syscall.TIOCGWINSZ, unsafe.Pointer(size)) // #nosec G103 -- Live typed winsize passed synchronously to ioctl.
}

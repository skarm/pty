//go:build !windows && !plan9 && !aix && !darwin && !linux && !freebsd && !openbsd && !netbsd && !dragonfly && !solaris

package pty

func (t *unixTerminal) interrupt() error       { return ErrUnsupported }
func (t *unixTerminal) eofByte() (byte, error) { return 0, ErrUnsupported }

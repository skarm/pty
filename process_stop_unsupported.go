//go:build windows || plan9 || aix

package pty

import "os"

func terminateProcess(*os.Process) error { return ErrUnsupported }

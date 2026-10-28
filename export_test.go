package pty

import "os"

// NativeFiles exposes master/slave descriptors to external regression tests.
// Both results are nil when the backend does not expose descriptors.
// This function is compiled only into the test binary.
func NativeFiles(terminal *Terminal) (master, slave *os.File) {
	if files, ok := terminal.driver.(interface{ nativeTestFiles() (*os.File, *os.File) }); ok {
		return files.nativeTestFiles()
	}
	return nil, nil
}

// NativeSlaveSize reads the slave's size independently of Terminal.Size.
// It returns ErrUnsupported when the backend does not expose the slave.
func NativeSlaveSize(terminal *Terminal) (Winsize, error) {
	if native, ok := terminal.driver.(interface{ nativeTestSize() (Winsize, error) }); ok {
		return native.nativeTestSize()
	}
	return Winsize{}, ErrUnsupported
}

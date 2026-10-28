//go:build windows

package pty

import (
	"os"
	"testing"
)

func inputPipe(t *testing.T) (writer, reader *os.File) {
	t.Helper()
	writer, handle, err := conptyPipe(false)
	if err != nil {
		t.Fatal(err)
	}
	return writer, os.NewFile(uintptr(handle), "test-conpty-input-reader")
}

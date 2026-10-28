//go:build !windows && !plan9 && !aix

package pty

import (
	"os"
	"testing"
)

func inputPipe(t *testing.T) (writer, reader *os.File) {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	return writer, reader
}

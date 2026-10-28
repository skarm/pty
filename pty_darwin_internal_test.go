//go:build darwin

package pty

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRequirePollableMaster(t *testing.T) {
	file, err := os.Create(filepath.Join(t.TempDir(), "regular"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	if err := requirePollableMaster(file); !errors.Is(err, os.ErrNoDeadline) {
		t.Fatalf("non-pollable file: %v", err)
	}
}

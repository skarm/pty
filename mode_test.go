//go:build linux || darwin

package pty_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/skarm/pty"
)

func TestTerminalMode(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	term := newTerminal(t)
	if err := term.SetMode(pty.Mode{Echo: false, Canonical: false}); err != nil {
		t.Fatal(err)
	}
	process, err := term.Start(ctx, terminalFixture(t, "single"))
	if err != nil {
		t.Fatal(err)
	}
	if err := term.SetMode(pty.Mode{Echo: true, Canonical: true}); err == nil {
		t.Fatal("mode changed after start")
	}
	probe := newTerminalProbe(term)
	if _, err := term.Write([]byte("Q")); err != nil {
		t.Fatal(err)
	}
	if err := probe.expect(ctx, 0, []byte("RECEIVED:Q")); err != nil {
		t.Fatal(err)
	}
	probe.mu.Lock()
	data := string(probe.data)
	probe.mu.Unlock()
	if data != "RECEIVED:Q" {
		t.Fatalf("unexpected echo or data: %q", data)
	}
	if _, err := term.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if _, err := process.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := term.Close(); err != nil {
		t.Fatal(err)
	}
	if err := term.SetMode(pty.Mode{}); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("closed SetMode: %v", err)
	}
}

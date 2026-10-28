package pty_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/skarm/pty"
)

func TestInputRejectsInvalidValues(t *testing.T) {
	for _, terminal := range []*pty.Terminal{nil, {}} {
		if _, err := terminal.WriteContext(context.Background(), nil); !errors.Is(err, os.ErrInvalid) {
			t.Fatalf("WriteContext: %v", err)
		}
		if err := terminal.Interrupt(context.Background()); !errors.Is(err, os.ErrInvalid) {
			t.Fatalf("Interrupt: %v", err)
		}
		if _, err := terminal.SendEOF(context.Background()); !errors.Is(err, os.ErrInvalid) {
			t.Fatalf("SendEOF: %v", err)
		}
	}
	terminal := newTerminal(t)
	if _, err := terminal.WriteContext(nil, nil); !errors.Is(err, os.ErrInvalid) {
		t.Fatalf("nil WriteContext context: %v", err)
	}
	if err := terminal.Interrupt(nil); !errors.Is(err, os.ErrInvalid) {
		t.Fatalf("nil Interrupt context: %v", err)
	}
	if _, err := terminal.SendEOF(nil); !errors.Is(err, os.ErrInvalid) {
		t.Fatalf("nil SendEOF context: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if n, err := terminal.WriteContext(ctx, []byte("forbidden")); n != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled WriteContext: %d, %v", n, err)
	}
	if err := terminal.Interrupt(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Interrupt: %v", err)
	}
	if n, err := terminal.SendEOF(ctx); n != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled SendEOF: %d, %v", n, err)
	}
	if err := terminal.Interrupt(context.Background()); !errors.Is(err, os.ErrInvalid) {
		t.Fatalf("Interrupt before Start: %v", err)
	}
	if err := terminal.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := terminal.WriteContext(context.Background(), []byte("x")); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("closed WriteContext: %v", err)
	}
	if err := terminal.Interrupt(context.Background()); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("closed Interrupt: %v", err)
	}
	if _, err := terminal.SendEOF(context.Background()); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("closed SendEOF: %v", err)
	}
}

func TestTerminalInterrupt(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	terminal := newTerminal(t)
	process, err := terminal.Start(ctx, terminalFixture(t, "interrupt"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = process.Kill(); _, _ = process.Wait() })
	probe := newTerminalProbe(terminal)
	if err := probe.expect(ctx, 0, []byte("INTERRUPT_READY")); err != nil {
		t.Fatal(err)
	}
	if err := terminal.Interrupt(ctx); err != nil {
		t.Fatal(err)
	}
	if err := probe.expect(ctx, 0, []byte("INTERRUPT_RECEIVED")); err != nil {
		t.Fatal(err)
	}
	if _, err := terminal.WriteContext(ctx, []byte("done\r")); err != nil {
		t.Fatal(err)
	}
	if _, err := process.Wait(); err != nil {
		t.Fatal(err)
	}
}

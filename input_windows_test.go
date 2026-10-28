package pty_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/skarm/pty"
)

func TestConPTYSendEOFUnsupported(t *testing.T) {
	terminal := newTerminal(t)
	if n, err := terminal.SendEOF(context.Background()); n != 0 || !errors.Is(err, pty.ErrUnsupported) {
		t.Fatalf("ConPTY SendEOF = %d, %v", n, err)
	}
	if err := terminal.Resize(pty.Winsize{Rows: 30, Cols: 90}); err != nil {
		t.Fatal("unsupported EOF closed terminal:", err)
	}
}

func TestConPTYRawInterruptChild(t *testing.T) {
	if os.Getenv("PTY_RAW_INTERRUPT_CHILD") != "1" {
		return
	}
	handle := windows.Handle(os.Stdin.Fd())
	var mode uint32
	if windows.GetConsoleMode(handle, &mode) != nil {
		os.Exit(81)
	}
	mode &^= windows.ENABLE_PROCESSED_INPUT | windows.ENABLE_LINE_INPUT | windows.ENABLE_ECHO_INPUT
	if windows.SetConsoleMode(handle, mode) != nil {
		os.Exit(82)
	}
	fmt.Println("RAW_INTERRUPT_READY")
	var value [1]byte
	if _, err := io.ReadFull(os.Stdin, value[:]); err != nil || value[0] != 3 {
		os.Exit(83)
	}
	fmt.Println("RAW_INTERRUPT_RECEIVED")
	_, _ = io.ReadFull(os.Stdin, value[:])
	os.Exit(0)
}

func TestConPTYInterruptInRawMode(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	terminal := newTerminal(t)
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	process, err := terminal.Start(ctx, pty.Command{Path: path, Args: []string{"-test.run=^TestConPTYRawInterruptChild$"}, Env: append(os.Environ(), "PTY_RAW_INTERRUPT_CHILD=1")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = process.Kill(); _, _ = process.Wait() })
	probe := newTerminalProbe(terminal)
	if err := probe.expect(ctx, 0, []byte("RAW_INTERRUPT_READY")); err != nil {
		t.Fatal(err)
	}
	if err := terminal.Interrupt(ctx); err != nil {
		t.Fatal(err)
	}
	if err := probe.expect(ctx, 0, []byte("RAW_INTERRUPT_RECEIVED")); err != nil {
		t.Fatal(err)
	}
	if _, err := terminal.WriteContext(ctx, []byte("x")); err != nil {
		t.Fatal(err)
	}
	if _, err := process.Wait(); err != nil {
		t.Fatal(err)
	}
}

// Windows inherits the parent's Ctrl+C ignore flag independently of registered
// handlers. Clear it only in the child fixture that promises to observe Ctrl+C.
func prepareInterruptFixture() error {
	ok, _, err := windows.NewLazySystemDLL("kernel32.dll").NewProc("SetConsoleCtrlHandler").Call(0, 0)
	if ok == 0 {
		return fmt.Errorf("enable fixture Ctrl+C handling: %w", err)
	}
	return nil
}

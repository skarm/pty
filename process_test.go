package pty_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/skarm/pty"
)

func TestTerminalExitStatus(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	term := newTerminal(t)
	process, err := term.Start(ctx, terminalFixture(t, "exit"))
	if err != nil {
		t.Fatal(err)
	}
	state, err := process.Wait()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || state == nil || state.ExitCode() != 23 {
		t.Fatalf("state=%v error=%v", state, err)
	}
}

func TestTerminalCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	term := newTerminal(t)
	process, err := term.Start(ctx, terminalFixture(t, "hold"))
	if err != nil {
		t.Fatal(err)
	}
	ready := make(chan error, 1)
	go func() { _, err := bufio.NewReader(term).ReadString('\n'); ready <- err }()
	select {
	case err := <-ready:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("child not ready")
	}
	cancel()
	done := make(chan error, 1)
	go func() { _, err := process.Wait(); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled process succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation did not stop child")
	}
	// Join terminal teardown even if context cancellation is still completing.
	if err := term.Close(); err != nil {
		t.Fatal(err)
	}
	if err := term.Resize(pty.Winsize{Rows: 20, Cols: 60}); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("resize after close: %v", err)
	}
}

func BenchmarkStartWait(b *testing.B) {
	path, err := exec.LookPath("true")
	if err != nil {
		b.Skip(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		terminal, err := pty.Open()
		if err != nil {
			b.Fatal(err)
		}
		process, err := terminal.Start(b.Context(), pty.Command{Path: path})
		if err != nil {
			_ = terminal.Close()
			b.Fatal(err)
		}
		_, waitErr := process.Wait()
		closeErr := terminal.Close()
		if waitErr != nil || closeErr != nil {
			b.Fatal(waitErr, closeErr)
		}
	}
}

// TestTerminalWindowsExecutableLookup checks PATHEXT resolution against os/exec.
func TestTerminalWindowsExecutableLookup(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows executable lookup uses PATHEXT")
	}
	t.Setenv("PATHEXT", ".EXE")
	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	input, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = input.Close() })
	path := filepath.Join(t.TempDir(), "pty child.exe")
	output, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil || closeErr != nil {
		t.Fatal(copyErr, closeErr)
	}
	withoutExtension := strings.TrimSuffix(path, ".exe")
	resolved, err := exec.LookPath(withoutExtension)
	if err != nil || !strings.EqualFold(resolved, path) {
		t.Fatalf("os/exec oracle: path=%q error=%v", resolved, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := terminalFixture(t, "exit")
	command.Path = withoutExtension
	process, err := newTerminal(t).Start(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	state, err := process.Wait()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || state == nil || state.ExitCode() != 23 {
		t.Fatalf("state=%v error=%v", state, err)
	}
}

// TestTerminalWindowsRedirectedParent keeps child I/O inside its ConPTY when
// the hosting process has pipe-backed standard handles, as in services and CI.
func TestTerminalWindowsRedirectedParent(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows may duplicate redirected parent standard handles")
	}

	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, path, "-test.run=^TestTerminalRoundTrip$", "-test.v")
	command.Stdin = strings.NewReader("")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("round trip from redirected parent: %v\n%s", err, output)
	}
}

// BenchmarkStartWaitContexts includes terminal ownership and child reaping.
func BenchmarkStartWaitContexts(b *testing.B) {
	path, err := exec.LookPath("true")
	if err != nil {
		b.Skip(err)
	}
	for _, cancelable := range []bool{false, true} {
		b.Run(fmt.Sprintf("cancelable=%t", cancelable), func(b *testing.B) {
			ctx := context.Background()
			if cancelable {
				ctx = b.Context()
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				terminal, err := pty.Open()
				if err != nil {
					b.Fatal(err)
				}
				process, err := terminal.Start(ctx, pty.Command{Path: path})
				if err != nil {
					_ = terminal.Close()
					b.Fatal(err)
				}
				_, waitErr := process.Wait()
				closeErr := terminal.Close()
				if waitErr != nil || closeErr != nil {
					b.Fatal(waitErr, closeErr)
				}
			}
		})
	}
}

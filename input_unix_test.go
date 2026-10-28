//go:build darwin || linux

package pty_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/skarm/pty"
)

func TestTerminalSendEOFUsesCurrentMode(t *testing.T) {
	terminal := newTerminal(t)
	_, slave := pty.NativeFiles(terminal)
	raw, err := slave.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var zeroEOFError error
	if testEOFDisabled == 0 {
		zeroEOFError = pty.ErrUnsupported
	}
	for _, test := range []struct {
		name      string
		canonical bool
		value     byte
		wantErr   error
	}{
		{"custom EOF", true, 7, nil},
		{"zero EOF", true, 0, zeroEOFError},
		{"raw input", false, 7, pty.ErrUnsupported},
		{"disabled EOF", true, testEOFDisabled, pty.ErrUnsupported},
	} {
		t.Run(test.name, func(t *testing.T) {
			var ioctlErr error
			if err := raw.Control(func(fd uintptr) {
				settings, err := unix.IoctlGetTermios(int(fd), testTermiosGet)
				if err != nil {
					ioctlErr = err
					return
				}
				settings.Lflag &^= unix.ICANON | unix.ECHO
				if test.canonical {
					settings.Lflag |= unix.ICANON
				}
				settings.Cc[unix.VEOF] = test.value
				ioctlErr = unix.IoctlSetTermios(int(fd), testTermiosSet, settings)
			}); err != nil {
				t.Fatal(err)
			}
			if ioctlErr != nil {
				t.Fatal(ioctlErr)
			}
			n, err := terminal.SendEOF(context.Background())
			if !errors.Is(err, test.wantErr) || (err == nil && n != 1) || (err != nil && n != 0) {
				t.Fatalf("SendEOF = %d, %v; want %v", n, err, test.wantErr)
			}
			if err == nil {
				if err := slave.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
					t.Fatal(err)
				}
				var buffer [1]byte
				if n, err := slave.Read(buffer[:]); n != 0 || !errors.Is(err, io.EOF) {
					t.Fatalf("configured EOF was not delivered to slave: %d, %v", n, err)
				}
				if err := slave.SetReadDeadline(time.Time{}); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestTerminalSendEOFDeliversPendingLine(t *testing.T) {
	terminal := newTerminal(t)
	if err := terminal.SetMode(pty.Mode{Canonical: true}); err != nil {
		t.Fatal(err)
	}
	_, slave := pty.NativeFiles(terminal)
	if err := slave.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := terminal.WriteContext(context.Background(), []byte("pending")); err != nil {
		t.Fatal(err)
	}
	if n, err := terminal.SendEOF(context.Background()); n != 1 || err != nil {
		t.Fatalf("SendEOF = %d, %v", n, err)
	}
	var buffer [32]byte
	n, err := slave.Read(buffer[:])
	if err != nil || !bytes.Equal(buffer[:n], []byte("pending")) {
		t.Fatalf("pending line = %q, %v", buffer[:n], err)
	}
	if n, err := terminal.SendEOF(context.Background()); n != 1 || err != nil {
		t.Fatalf("second SendEOF = %d, %v", n, err)
	}
	if n, err := slave.Read(buffer[:]); n != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("empty line EOF = %d, %v", n, err)
	}
}

func TestForegroundInterruptChild(t *testing.T) {
	mode := os.Getenv("PTY_FOREGROUND_INTERRUPT_CHILD")
	if mode == "" {
		return
	}
	if mode == "leaf" {
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, os.Interrupt)
		fmt.Println("FOREGROUND_CHILD_READY")
		<-signals
		fmt.Println("FOREGROUND_CHILD_INTERRUPTED")
		os.Exit(0)
	}
	signal.Ignore(syscall.SIGTTOU)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestForegroundInterruptChild$")
	child.Env = append(os.Environ(), "PTY_FOREGROUND_INTERRUPT_CHILD=leaf")
	child.Stdin, child.Stdout, child.Stderr = os.Stdin, os.Stdout, os.Stderr
	child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if child.Start() != nil {
		cancel()
		os.Exit(84)
	}
	if unix.IoctlSetPointerInt(0, unix.TIOCSPGRP, child.Process.Pid) != nil {
		_ = child.Process.Kill()
		_ = child.Wait()
		cancel()
		os.Exit(85)
	}
	fmt.Println("FOREGROUND_GROUP_SET")
	err := child.Wait()
	cancel()
	if err != nil {
		os.Exit(86)
	}
	os.Exit(0)
}

func TestTerminalInterruptUsesForegroundGroup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	terminal := newTerminal(t)
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	process, err := terminal.Start(ctx, pty.Command{Path: path, Args: []string{"-test.run=^TestForegroundInterruptChild$"}, Env: append(os.Environ(), "PTY_FOREGROUND_INTERRUPT_CHILD=root")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = process.Kill(); _, _ = process.Wait() })
	probe := newTerminalProbe(terminal)
	for _, marker := range []string{"FOREGROUND_GROUP_SET", "FOREGROUND_CHILD_READY"} {
		if err := probe.expect(ctx, 0, []byte(marker)); err != nil {
			t.Fatal(err)
		}
	}
	if err := terminal.Interrupt(ctx); err != nil {
		t.Fatal(err)
	}
	if err := probe.expect(ctx, 0, []byte("FOREGROUND_CHILD_INTERRUPTED")); err != nil {
		t.Fatal(err)
	}
	if _, err := process.Wait(); err != nil {
		t.Fatal(err)
	}
}

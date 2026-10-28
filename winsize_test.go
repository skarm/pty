//go:build darwin || linux

package pty_test

import (
	"errors"
	"os"
	"runtime"
	"sync/atomic"
	"syscall"
	"testing"
	"unsafe"

	"github.com/skarm/pty"
)

func TestClosedTerminalCannotResizeReusedDescriptor(t *testing.T) {
	closed := newTerminal(t)
	original, _ := pty.NativeFiles(closed)
	descriptor := original.Fd()
	if err := closed.Close(); err != nil {
		t.Fatal(err)
	}
	replacement := newTerminal(t)
	master, _ := pty.NativeFiles(replacement)
	if master.Fd() != descriptor {
		t.Skipf("fd %d not reused (got %d)", descriptor, master.Fd())
	}
	want := pty.Winsize{Rows: 37, Cols: 93}
	if err := replacement.Resize(want); err != nil {
		t.Fatal(err)
	}
	if _, err := closed.Size(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("closed Size: %v", err)
	}
	if err := closed.Resize(pty.Winsize{Rows: 11, Cols: 22}); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("closed Resize: %v", err)
	}
	got, err := replacement.Size()
	if err != nil || got != want {
		t.Fatalf("reused descriptor changed: got %+v,%v want %+v", got, err, want)
	}
}

func TestResizeCopiesAllFields(t *testing.T) {
	source, destination := newTerminal(t), newTerminal(t)
	want := pty.Winsize{Rows: 37, Cols: 93, X: 640, Y: 480}
	if err := source.Resize(want); err != nil {
		t.Fatal(err)
	}
	size, err := source.Size()
	if err != nil {
		t.Fatal(err)
	}
	if err := destination.Resize(size); err != nil {
		t.Fatal(err)
	}
	got, err := pty.NativeSlaveSize(destination)
	if err != nil || got != want {
		t.Fatalf("slave size: %+v,%v want %+v", got, err, want)
	}
}

func BenchmarkResize(b *testing.B) {
	terminal, err := pty.Open()
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = terminal.Close() })
	size := pty.Winsize{Rows: 24, Cols: 80}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if err := terminal.Resize(size); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSizeOperations(b *testing.B) {
	terminal, err := pty.Open()
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = terminal.Close() })
	b.Run("changing", func(b *testing.B) {
		size := pty.Winsize{Cols: 80}
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			size.Rows = uint16(24 + i%2)
			if err := terminal.Resize(size); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("get", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if _, err := terminal.Size(); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("copy", func(b *testing.B) {
		destination, err := pty.Open()
		if err != nil {
			b.Fatal(err)
		}
		b.Cleanup(func() { _ = destination.Close() })
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			size, err := terminal.Size()
			if err != nil {
				b.Fatal(err)
			}
			if err := destination.Resize(size); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("parallel", func(b *testing.B) {
		size := pty.Winsize{Rows: 24, Cols: 80}
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				if err := terminal.Resize(size); err != nil {
					b.Error(err)
					return
				}
			}
		})
	})
	b.Run("parallel-independent", func(b *testing.B) {
		terminals := make([]*pty.Terminal, runtime.GOMAXPROCS(0))
		for i := range terminals {
			terminal, err := pty.Open()
			if err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() { _ = terminal.Close() })
			terminals[i] = terminal
		}
		var next atomic.Int64
		size := pty.Winsize{Rows: 24, Cols: 80}
		b.ReportAllocs()
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			terminal := terminals[next.Add(1)-1]
			for pb.Next() {
				if err := terminal.Resize(size); err != nil {
					b.Error(err)
					return
				}
			}
		})
	})
	// A pinned native descriptor is an independent syscall-cost control, not a
	// production API. Close/reuse protection is measured by the other cases.
	b.Run("syscall-control", func(b *testing.B) {
		master, _ := pty.NativeFiles(terminal)
		fd := master.Fd()
		size := pty.Winsize{Rows: 24, Cols: 80}
		b.ReportAllocs()
		for range b.N {
			if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TIOCSWINSZ, uintptr(unsafe.Pointer(&size))); errno != 0 {
				b.Fatal(errno)
			}
		}
	})
}

//go:build linux
// +build linux

package pty_test

// Regression from https://github.com/creack/pty/pull/216, commit
// 2a5bd5891816b21e813e728bd00d12f84afbd70f, under the repository MIT license.

import (
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/skarm/pty"
)

// TestOpenUnderGCStackShrink checks that ioctl writes reach live stack storage.
// The ioctl argument must remain a typed pointer until syscall.Syscall's argument
// list. Converting it to uintptr earlier lets GC stack shrinking move the buffer
// before the kernel writes to it. A lost TIOCGPTN result leaves index zero and can
// pair the master with another terminal's slave.
//
// Deep recursion makes worker stacks eligible for shrinking. Concurrent GC
// exercises the conversion window; an independent ioctl and fstat check that
// each master is paired with its own slave.
func TestOpenUnderGCStackShrink(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping GC stress in short mode.")
	}
	holdIndexZero(t)

	var stop atomic.Bool
	var gcs sync.WaitGroup
	gcs.Add(1)
	go func() {
		defer gcs.Done()
		for !stop.Load() {
			runtime.GC()
		}
	}()

	var (
		mu         sync.Mutex
		failures   []string
		iterations atomic.Int64
	)
	deadline := time.Now().Add(3 * time.Second)
	var workers sync.WaitGroup
	for w := 0; w < 8; w++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for time.Now().Before(deadline) && !stop.Load() {
				// Grow the stack well past what Open() needs and return, so the
				// goroutine is a shrink candidate while Open() runs.
				atDepth(2000, func() {})
				iterations.Add(1)
				if msg := checkOpen(t); msg != "" {
					mu.Lock()
					failures = append(failures, msg)
					mu.Unlock()
					stop.Store(true)
				}
			}
		}()
	}
	workers.Wait()
	stop.Store(true)
	gcs.Wait()

	for _, msg := range failures {
		t.Errorf("%s.", msg)
	}
	t.Logf("%d Open() calls under GC pressure, %d wrong.", iterations.Load(), len(failures))
}

// holdIndexZero keeps one pty open for the test so later ones get a non-zero
// index; a lost TIOCGPTN write then shows up as /dev/pts/0 instead of passing
// by coincidence.
func holdIndexZero(t *testing.T) {
	t.Helper()
	terminal, err := pty.Open()
	if err != nil {
		t.Fatalf("Open: %s.", err)
	}
	t.Cleanup(func() { _ = terminal.Close() })
}

// checkOpen runs Open() and returns a description of what went wrong, or "".
func checkOpen(t *testing.T) string {
	t.Helper()
	terminal, err := pty.Open()
	if err != nil {
		return "Open: " + err.Error()
	}
	defer func() { _ = terminal.Close() }()
	p, tty := pty.NativeFiles(terminal)

	ref := refPtsIndex(t, p)
	var st syscall.Stat_t
	if err := syscall.Fstat(int(tty.Fd()), &st); err != nil {
		return "fstat tty: " + err.Error()
	}
	slave := int(uint32(st.Rdev&0xff) | uint32((uint64(st.Rdev)&0x00000ffffff00000)>>12)) //nolint:unconvert // Rdev width varies by arch.
	want := "/dev/pts/" + strconv.Itoa(ref)
	if tty.Name() != want || slave != ref {
		return "master index " + strconv.Itoa(ref) + ", tty named " + tty.Name() + ", slave is /dev/pts/" + strconv.Itoa(slave)
	}
	return ""
}

// refPtsIndex reads TIOCGPTN with the conversion in the Syscall argument list.
func refPtsIndex(t *testing.T, master *os.File) int {
	t.Helper()
	var n uint32
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&n)))
	if e != 0 {
		t.Fatalf("TIOCGPTN: %s.", e)
	}
	return int(n)
}

// atDepth calls fn with about depth extra frames on the stack.
//
//go:noinline
func atDepth(depth int, fn func()) byte {
	var pad [8]byte
	pad[depth&7] = byte(depth)
	if depth > 0 {
		pad[0] += atDepth(depth-1, fn)
	} else {
		fn()
	}
	return pad[depth&7]
}

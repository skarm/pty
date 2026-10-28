package pty_test

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"sync"
	"syscall"
	"testing"

	"github.com/skarm/pty"
)

// TestDarwinUTF8Erase checks that canonical erase removes a complete UTF-8 rune.
func TestDarwinUTF8Erase(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Darwin enables IUTF8 during Open")
	}
	for _, character := range []string{"a", "я", "明", "😀"} {
		t.Run(character, func(t *testing.T) {
			master := newTerminal(t)
			_, slave := pty.NativeFiles(master)
			input := "A" + character + "\x7f\n"
			if n, err := master.Write([]byte(input)); err != nil || n != len(input) {
				t.Fatalf("write canonical input: n=%d err=%v", n, err)
			}
			got, err := bufio.NewReader(slave).ReadString('\n')
			if err != nil {
				t.Fatal(err)
			}
			if got != "A\n" {
				t.Fatalf("canonical erase: got %q (% x), want %q", got, got, "A\n")
			}
		})
	}
}

func TestOpenCloseResources(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("resource accounting uses Linux/macOS descriptor directories")
	}
	fdDir := "/proc/self/fd"
	if runtime.GOOS == "darwin" {
		fdDir = "/dev/fd"
	}
	countFDs := func() int {
		dir, err := os.Open(fdDir)
		if err != nil {
			t.Fatal(err)
		}
		// Only names are needed, not metadata for the descriptor entries.
		fds, readErr := dir.Readdirnames(-1)
		closeErr := dir.Close()
		if readErr != nil || closeErr != nil {
			t.Fatal(readErr, closeErr)
		}
		return len(fds)
	}
	// Warm up the runtime poller before measuring descriptor ownership.
	p, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	initialFDs := countFDs()
	cycles := 256
	if testing.Short() {
		cycles = 8
	}
	dir := os.Getenv("PTY_PERF_DIR")
	var samples []map[string]any
	for round := 0; round <= 4; round++ {
		if round > 0 {
			for i := 0; i < cycles; i++ {
				p, err := pty.Open()
				if err != nil {
					t.Fatal(err)
				}
				if err := p.Close(); err != nil {
					t.Fatal(err)
				}
			}
		}
		runtime.GC()
		fds := countFDs()
		if fds != initialFDs {
			t.Fatalf("descriptor leak after %d opens: before=%d after=%d", round*cycles, initialFDs, fds)
		}
		var stats runtime.MemStats
		runtime.ReadMemStats(&stats)
		samples = append(samples, map[string]any{"opens": round * cycles, "fds": fds, "goroutines": runtime.NumGoroutine(), "heap_alloc": stats.HeapAlloc, "heap_objects": stats.HeapObjects, "total_alloc": stats.TotalAlloc, "gc_cycles": stats.NumGC, "gc_pause_ns": stats.PauseTotalNs})
		if dir != "" {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			f, err := os.Create(filepath.Join(dir, fmt.Sprintf("goroutines-%d.txt", round)))
			if err != nil {
				t.Fatal(err)
			}
			err = pprof.Lookup("goroutine").WriteTo(f, 2)
			if closeErr := f.Close(); err != nil || closeErr != nil {
				t.Fatal(err, closeErr)
			}
			if round == 0 || round == 4 {
				f, err := os.Create(filepath.Join(dir, fmt.Sprintf("heap-%d.pprof", round)))
				if err != nil {
					t.Fatal(err)
				}
				err = pprof.WriteHeapProfile(f)
				if closeErr := f.Close(); err != nil || closeErr != nil {
					t.Fatal(err, closeErr)
				}
			}
		}
	}
	if dir != "" {
		data, err := json.MarshalIndent(samples, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "resources.json"), append(data, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("%d Open/Close cycles: descriptors=%d, goroutines=%v", 4*cycles, initialFDs, samples[len(samples)-1]["goroutines"])
}

func TestConcurrentOpenClose(t *testing.T) {
	switch runtime.GOOS {
	case "darwin", "linux", "dragonfly", "freebsd", "netbsd", "openbsd", "solaris", "illumos":
	default:
		t.Skip("allocation stress uses native Unix PTY descriptors")
	}
	iterations := 256
	if testing.Short() {
		iterations = 8
	}
	var workers sync.WaitGroup
	start := make(chan struct{})
	failures := make(chan error, 4)
	for w := range 4 {
		workers.Add(1)
		go func(worker int) {
			defer workers.Done()
			<-start
			for i := 0; i < iterations; i++ {
				p, err := pty.Open()
				if err != nil {
					failures <- err
					return
				}
				want := pty.Winsize{Rows: uint16(100 + worker), Cols: uint16(80 + i%2)}
				err = p.Resize(want)
				if err == nil {
					got, e := pty.NativeSlaveSize(p)
					if e != nil {
						err = e
					} else if got != want {
						err = fmt.Errorf("master/slave identity mismatch: got %+v want %+v", got, want)
					}
				}
				err = errors.Join(err, p.Close())
				if err != nil {
					failures <- err
					return
				}
			}
		}(w)
	}
	close(start)
	workers.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
}

// TestSolarisDistinctSlaves catches truncation of the PTY minor number to 8 bits.
func TestSolarisDistinctSlaves(t *testing.T) {
	if runtime.GOOS != "solaris" && runtime.GOOS != "illumos" {
		t.Skip("Solaris constructs the slave path from its minor number")
	}
	names := make(map[string]bool, 257)
	for range 257 {
		terminal, err := pty.Open()
		if errors.Is(err, syscall.EMFILE) || errors.Is(err, syscall.ENFILE) {
			t.Skipf("257 live terminals require a higher open-file limit: %v", err)
		}
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = terminal.Close() })
		_, slave := pty.NativeFiles(terminal)
		if names[slave.Name()] {
			t.Fatalf("distinct terminals share slave %q", slave.Name())
		}
		names[slave.Name()] = true
	}
}

func BenchmarkOpenClose(b *testing.B) {
	for _, parallel := range []bool{false, true} {
		if !b.Run(fmt.Sprintf("parallel=%t", parallel), func(b *testing.B) {
			openAndClose := func() error {
				p, err := pty.Open()
				if err != nil {
					return err
				}
				return p.Close()
			}
			b.ReportAllocs()
			b.ResetTimer()
			if parallel {
				b.RunParallel(func(pb *testing.PB) {
					for pb.Next() {
						if err := openAndClose(); err != nil {
							b.Error(err)
							return
						}
					}
				})
			} else {
				for i := 0; i < b.N; i++ {
					if err := openAndClose(); err != nil {
						b.Fatal(err)
					}
				}
			}
		}) {
			b.Fail()
		}
	}
}

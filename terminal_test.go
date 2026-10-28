package pty_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/skarm/pty"
)

const (
	errMarker byte = 0xEE
	timeout        = time.Second
)

func TestOpen(t *testing.T) {
	t.Parallel()
	terminal := newTerminal(t)
	got, err := terminal.Size()
	if err != nil || got != (pty.Winsize{Rows: 24, Cols: 80}) {
		t.Fatalf("initial size: %+v, %v", got, err)
	}
}

func TestTerminalRejectsInvalidInputs(t *testing.T) {
	term := newTerminal(t)
	for _, size := range []pty.Winsize{{}, {Rows: 1}, {Cols: 1}, {Rows: 65535, Cols: 80}, {Rows: 24, Cols: 65535}} {
		if err := term.Resize(size); err == nil {
			t.Fatalf("accepted size %+v", size)
		}
		got, err := term.Size()
		if err != nil || got != (pty.Winsize{Rows: 24, Cols: 80}) {
			t.Fatalf("invalid resize changed size: %+v, %v", got, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := term.Start(ctx, terminalFixture(t, "hold")); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Start: %v", err)
	}
	if _, err := term.Start(context.Background(), pty.Command{}); err == nil {
		t.Fatal("empty path accepted")
	}
	if _, err := term.Start(context.Background(), pty.Command{Path: filepath.Join(t.TempDir(), "missing-program")}); err == nil {
		t.Fatal("missing executable accepted")
	}
	if _, err := term.Start(nil, pty.Command{Path: "unused"}); !errors.Is(err, os.ErrInvalid) {
		t.Fatalf("nil context: %v", err)
	}
	if runtime.GOOS == "windows" {
		if err := term.Resize(pty.Winsize{Rows: 24, Cols: 80, X: 10}); !errors.Is(err, pty.ErrUnsupported) {
			t.Fatalf("pixel size: %v", err)
		}
	}
}

func TestTerminalRejectsUninitializedReceiver(t *testing.T) {
	for _, terminal := range []*pty.Terminal{nil, {}} {
		if _, err := terminal.Size(); !errors.Is(err, os.ErrInvalid) {
			t.Fatalf("Size: %v", err)
		}
		if err := terminal.Resize(pty.Winsize{Rows: 24, Cols: 80}); !errors.Is(err, os.ErrInvalid) {
			t.Fatalf("Resize: %v", err)
		}
		if _, err := terminal.Read(nil); !errors.Is(err, os.ErrInvalid) {
			t.Fatalf("Read: %v", err)
		}
		if _, err := terminal.Write(nil); !errors.Is(err, os.ErrInvalid) {
			t.Fatalf("Write: %v", err)
		}
		if err := terminal.Close(); !errors.Is(err, os.ErrInvalid) {
			t.Fatalf("Close: %v", err)
		}
	}
}

func TestTerminalRejectsInvalidEnvironment(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	term := newTerminal(t)
	invalid := [][]string{
		{"BAD=value\x00suffix"},
		{"BAD\x00NAME=value"},
		{"REPEATED=bad\x00value", "REPEATED=valid"},
	}
	if runtime.GOOS == "windows" {
		invalid = append(invalid, []string{""}, []string{"NO_SEPARATOR"}, []string{"=NO_SEPARATOR"})
	}
	for _, entries := range invalid {
		command := terminalFixture(t, "exit")
		command.Env = append(command.Env, entries...)
		process, err := term.Start(ctx, command)
		if err == nil {
			_ = process.Kill()
			_, _ = process.Wait()
			t.Fatalf("accepted invalid environment %q", entries)
		}
		if process != nil {
			t.Fatalf("failed Start returned process for environment %q", entries)
		}
	}
	// Failed starts leave the same terminal available for a valid command.
	process, err := term.Start(ctx, terminalFixture(t, "exit"))
	if err != nil {
		t.Fatal(err)
	}
	state, err := process.Wait()
	if err == nil || state == nil || state.ExitCode() != 23 {
		t.Fatalf("retry result: state=%v error=%v", state, err)
	}
}

func TestTerminalRoundTrip(t *testing.T) {
	for _, relative := range []bool{false, true} {
		t.Run(fmt.Sprintf("relativeDir=%t", relative), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			term := newTerminal(t)
			size := pty.Winsize{Rows: 40, Cols: 512}
			if err := term.Resize(size); err != nil {
				t.Fatal(err)
			}
			got, err := term.Size()
			if err != nil || got != size {
				t.Fatalf("size=%+v err=%v want %+v", got, err, size)
			}
			args := []string{"a b", `a"b`, "юникод😀", `trailing slash\`, ""}
			command := terminalFixture(t, "echo", args...)
			directory := t.TempDir()
			command.Dir = directory
			if relative {
				t.Chdir(filepath.Dir(directory))
				command.Dir = filepath.Base(directory)
			}
			process, err := term.Start(ctx, command)
			if err != nil {
				t.Fatal(err)
			}
			if process.PID() <= 0 {
				t.Fatalf("invalid pid %d", process.PID())
			}
			// Drain concurrently with input: both ConPTY communication channels have
			// bounded buffers and must be serviced independently.
			output := make(chan []byte, 1)
			readErr := make(chan error, 1)
			go func() { b, err := io.ReadAll(term); output <- b; readErr <- err }()
			if _, err := term.Write([]byte("hello мир\r")); err != nil {
				t.Fatal(err)
			}
			var data []byte
			select {
			case data = <-output:
			case <-ctx.Done():
				t.Fatal("output did not reach EOF:", ctx.Err())
			}
			if err := <-readErr; err != nil {
				t.Fatal(err)
			}
			const marker = "PTY_RESULT:"
			_, payload, ok := strings.Cut(string(data), marker)
			if !ok {
				t.Fatalf("missing result: %q", data)
			}
			var result struct {
				Args            []string
				Input, Dir, Env string
			}
			if err := json.NewDecoder(strings.NewReader(payload)).Decode(&result); err != nil {
				t.Fatalf("decode %q: %v", data, err)
			}
			if !slices.Equal(result.Args, args) || result.Input != "hello мир" || result.Env != "значение" {
				t.Fatalf("child result: %+v", result)
			}
			gotDir, err := filepath.EvalSymlinks(result.Dir)
			if err != nil {
				t.Fatal(err)
			}
			wantDir, err := filepath.EvalSymlinks(directory)
			if err != nil || !strings.EqualFold(gotDir, wantDir) {
				t.Fatalf("dir=%q want %q err=%v", gotDir, wantDir, err)
			}
			var waits sync.WaitGroup
			for range 4 {
				waits.Go(func() {
					state, err := process.Wait()
					if err != nil || state == nil || !state.Success() {
						t.Errorf("Wait: %v %v", state, err)
					}
				})
			}
			waits.Wait()
			if _, err := term.Start(ctx, command); err == nil {
				t.Fatal("second start succeeded")
			}
		})
	}
}

func TestTerminalCloseUnblocksRead(t *testing.T) {
	term := newTerminal(t)
	entered, done := make(chan struct{}), make(chan struct{})
	go func() { close(entered); _, _ = io.Copy(io.Discard, term); close(done) }()
	<-entered
	if err := term.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("reader remained blocked after Close")
	}
	if err := term.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}

// TestTerminalCloseWithActiveRead keeps the child slave open during Close.
func TestTerminalCloseWithActiveRead(t *testing.T) {
	if testing.Short() {
		t.Skip("active-read teardown uses a subprocess and a blocking-read fixture")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	terminal := newTerminal(t)
	process, err := terminal.Start(ctx, terminalFixture(t, "hold"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = process.Kill(); _, _ = process.Wait() })
	if _, err := bufio.NewReader(terminal).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	entered, readDone := make(chan struct{}), make(chan struct{})
	go func() {
		close(entered)
		_, _ = io.Copy(io.Discard, terminal)
		close(readDone)
	}()
	<-entered
	// Allow the reader to enter the syscall while the child waits for input.
	time.Sleep(timeout / 10)
	closed := make(chan error, 1)
	go func() { closed <- terminal.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(timeout):
		t.Fatal("Close blocked while the child kept its slave open")
	}
	select {
	case <-readDone:
	case <-time.After(timeout):
		t.Fatal("active read remained blocked after Close")
	}
}

func TestTerminalCloseWithUnreadOutput(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("ConPTY close must not wait for a final-frame reader")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	term := newTerminal(t)
	process, err := term.Start(ctx, terminalFixture(t, "flood"))
	if err != nil {
		t.Fatal(err)
	}
	// Begin receiving output once so teardown occurs with an attached client.
	buf := make([]byte, 1)
	if _, err := term.Read(buf); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- term.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("Close blocked without output reader")
	}
	_, _ = process.Wait()
}

// Drain while the child is alive: some Unix kernels can discard unread bytes
// during session teardown, so reading only after Wait cannot guarantee delivery.
func TestTerminalDrainsOutputBeforeExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ConPTY output is a rendered VT stream, not the child's byte stream")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	term := newTerminal(t)
	process, err := term.Start(ctx, terminalFixture(t, "burst"))
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Repeat("x", 64<<10)
	got := make([]byte, len(want))
	if _, err := io.ReadFull(term, got); err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatal("output changed or lost while draining")
	}
	if _, err := term.Write([]byte("done\r")); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, term); err != nil {
		t.Fatal(err)
	}
	if _, err := process.Wait(); err != nil {
		t.Fatal(err)
	}
}

// ConPTY emits screen updates rather than a byte-for-byte copy of writes.
// Drain while the child emits enough output to exercise the bounded pipes.
func TestTerminalDrainsConPTYBeforeExit(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("ConPTY is only available on Windows")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	term := newTerminal(t)
	process, err := term.Start(ctx, terminalFixture(t, "burst"))
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		n   int64
		err error
	}
	output := make(chan result, 1)
	go func() {
		n, err := io.Copy(io.Discard, term)
		output <- result{n, err}
	}()
	if _, err := io.WriteString(term, "done\r"); err != nil {
		t.Fatal(err)
	}
	if _, err := process.Wait(); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-output:
		if got.err != nil || got.n == 0 {
			t.Fatalf("drain: bytes=%d error=%v", got.n, got.err)
		}
	case <-ctx.Done():
		t.Fatalf("output did not reach EOF: %v", ctx.Err())
	}
}

// TestReadDeadline observes the Unix backend deadline through a test-only hook:
// outstanding reads must be interrupted by their deadline.
// See https://github.com/creack/pty/issues/162 (fixed upstream by PR #167).
func TestReadDeadline(t *testing.T) {
	ptmx, success := prepareReadTest(t)

	file, _ := pty.NativeFiles(ptmx)
	if err := file.SetDeadline(time.Now().Add(timeout / 10)); err != nil {
		if errors.Is(err, os.ErrNoDeadline) && runtime.GOOS != "darwin" {
			t.Skipf("Deadline is not supported on %s/%s.", runtime.GOOS, runtime.GOARCH)
		}

		t.Fatalf("Error: set deadline: %s.", err)
	}

	buf := make([]byte, 1)
	n, err := ptmx.Read(buf)
	success()
	if n != 0 || !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("expected deadline without data, got %d bytes; error=%v", n, err)
	}
}

// TestReadClose checks that Close interrupts reads on a pollable master.
// See https://github.com/creack/pty/issues/88 and the poller fix for issue #162.
func TestReadClose(t *testing.T) {
	ptmx, success := prepareReadTest(t)

	go func() {
		time.Sleep(timeout / 10)
		if err := ptmx.Close(); err != nil {
			t.Errorf("Failed to close ptmx: %s.", err)
		}
	}()

	buf := make([]byte, 1)
	n, err := ptmx.Read(buf)
	success()
	if err != nil && !errors.Is(err, os.ErrClosed) && !errors.Is(err, io.EOF) {
		t.Fatalf("Unexpected read error: %s.", err)
	}

	if n != 0 && buf[0] != errMarker {
		t.Errorf("Received unexpected data from ptmx (%d bytes): 0x%X; err=%v.", n, buf, err)
	}
}

// TestTerminalHelperProcess runs a subprocess fixture when PTY_TERMINAL_CHILD is set.
func TestTerminalHelperProcess(t *testing.T) {
	mode := os.Getenv("PTY_TERMINAL_CHILD")
	if mode == "" {
		return
	}
	switch mode {
	case "echo":
		input, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil {
			os.Exit(2)
		}
		dir, err := os.Getwd()
		if err != nil {
			os.Exit(3)
		}
		data, err := json.Marshal(map[string]any{"args": os.Args[3:], "input": strings.TrimSpace(input), "dir": dir, "env": os.Getenv("PTY_TERMINAL_ENV")})
		if err != nil {
			os.Exit(4)
		}
		fmt.Println("PTY_RESULT:" + string(data))
	case "interrupt":
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, os.Interrupt)
		if err := prepareInterruptFixture(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(87)
		}
		fmt.Println("INTERRUPT_READY")
		<-signals
		signal.Stop(signals)
		fmt.Println("INTERRUPT_RECEIVED")
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
	case "exit":
		os.Exit(23)
	case "hold":
		fmt.Println("PTY_READY")
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
	case "flood":
		fmt.Print(strings.Repeat("x", 4<<20))
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
	case "burst":
		fmt.Print(strings.Repeat("x", 64<<10))
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
	case "dialog":
		reader := bufio.NewReader(os.Stdin)
		fmt.Print("PROMPT>")
		line, err := reader.ReadString('\n')
		if err != nil {
			os.Exit(6)
		}
		fmt.Print("ANSWER:" + strings.TrimSpace(line) + ":END")
		_, _ = reader.ReadString('\n')
	case "single":
		var input [1]byte
		if _, err := io.ReadFull(os.Stdin, input[:]); err != nil {
			os.Exit(7)
		}
		fmt.Print("RECEIVED:" + string(input[:]))
		_, _ = io.ReadFull(os.Stdin, input[:])
	default:
		os.Exit(5)
	}
	os.Exit(0)
}

func BenchmarkPTYRoundTrip(b *testing.B) {
	for _, size := range []int{256, 4096, 65536} {
		b.Run(fmt.Sprintf("bytes=%d", size), func(b *testing.B) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			p, err := pty.Open()
			if err != nil {
				b.Fatal(err)
			}
			process, err := p.Start(ctx, pty.Command{Path: "sh", Args: []string{"-c", "stty raw -echo && printf pty-ready && exec cat"}})
			if err != nil {
				_ = p.Close()
				b.Fatal(err)
			}
			var writerDone chan struct{}
			b.Cleanup(func() {
				cancel()
				_ = process.Kill()
				_, _ = process.Wait()
				_ = p.Close()
				if writerDone != nil {
					<-writerDone
				}
			})
			ready := make([]byte, len("pty-ready"))
			if _, err := io.ReadFull(p, ready); err != nil || string(ready) != "pty-ready" {
				b.Fatal(string(ready), err)
			}
			payload, reply := bytes.Repeat([]byte{'x'}, size), make([]byte, size)
			writerDone = make(chan struct{})
			requests := make(chan struct{})
			written := make(chan error, 1)
			// Read concurrently with Write: a PTY has bounded buffers in both
			// directions, so write-all-then-read can deadlock on larger blocks.
			go func() {
				defer close(writerDone)
				for {
					select {
					case <-ctx.Done():
						return
					case <-requests:
					}
					n, err := p.Write(payload)
					if err == nil && n != size {
						err = io.ErrShortWrite
					}
					select {
					case written <- err:
					case <-ctx.Done():
						return
					}
					if err != nil {
						return
					}
				}
			}()
			latency := make([]time.Duration, b.N)
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				start := time.Now()
				select {
				case requests <- struct{}{}:
				case <-ctx.Done():
					b.Fatal(ctx.Err())
				}
				if _, err := io.ReadFull(p, reply); err != nil || !bytes.Equal(payload, reply) {
					b.Fatal(err)
				}
				if err := <-written; err != nil {
					b.Fatal(err)
				}
				latency[i] = time.Since(start)
			}
			b.StopTimer()
			slices.Sort(latency)
			for _, percentile := range []int{50, 95, 99} {
				index := b.N * percentile / 100
				if index >= b.N {
					index = b.N - 1
				}
				b.ReportMetric(float64(latency[index]), fmt.Sprintf("p%d-ns", percentile))
			}
		})
	}
}

func ExampleOpen() {
	terminal, err := pty.Open()
	if err != nil {
		panic(err)
	}
	defer func() { _ = terminal.Close() }()
	size, err := terminal.Size()
	if err != nil {
		panic(err)
	}
	fmt.Printf("%d columns, %d rows\n", size.Cols, size.Rows)
	// Output: 80 columns, 24 rows
}

func newTerminal(t *testing.T) *pty.Terminal {
	t.Helper()
	terminal, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := terminal.Close(); err != nil {
			t.Error(err)
		}
	})
	return terminal
}

func terminalFixture(t *testing.T, mode string, extra ...string) pty.Command {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return pty.Command{Path: path, Args: append([]string{"-test.run=^TestTerminalHelperProcess$", "--"}, extra...), Env: append(os.Environ(), "PTY_TERMINAL_CHILD="+mode, "PTY_TERMINAL_ENV=значение")}
}

// prepareReadTest opens a terminal and installs watchdogs for Unix poller tests.
func prepareReadTest(t *testing.T) (ptmx *pty.Terminal, done func()) {
	t.Helper()
	if testing.Short() {
		t.Skip("deadline and close regressions wait for timers")
	}
	if runtime.GOOS == "windows" {
		t.Skip("Unix poller regression; Windows has portable lifecycle tests")
	}

	ptmx = newTerminal(t)
	_, pts := pty.NativeFiles(ptmx)

	ctx, done := context.WithCancel(context.Background())
	t.Cleanup(done)
	go func() {
		select {
		case <-ctx.Done():
			// The read completed or test cleanup canceled the watchdog.
		case <-time.After(timeout):
			if _, err := pts.Write([]byte{errMarker}); err != nil { // Unblock ptmx.Read().
				t.Errorf("Failed to write to pts: %s.", err)
			}
			t.Error("ptmx.Read() was not unblocked.")
			done() // Stop the fallback panic after reporting the timeout.
		}
	}()
	go func() {
		select {
		case <-ctx.Done():
			// The read completed or the first watchdog reported its timeout.
		case <-time.After(timeout * 10 / 9):
			// Fail the suite if the first watchdog also blocks writing to the slave.
			panic("ptmx.Read() was not unblocked; avoid hanging forever.")
		}
	}()

	return ptmx, done
}

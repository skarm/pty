package pty

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type windowsTerminal struct {
	mu            sync.Mutex
	console       windows.Handle
	input, output *os.File
	size          Winsize
	started       bool
	closed        atomic.Bool
	finishOnce    sync.Once
	closeOnce     sync.Once
	closeErr      error
}

func openTerminal(size Winsize) (terminalDriver, error) {
	if size.X != 0 || size.Y != 0 {
		return nil, ErrUnsupported
	}

	if err := windows.NewLazySystemDLL("kernel32.dll").NewProc("CreatePseudoConsole").Find(); err != nil {
		return nil, fmt.Errorf("ConPTY is unavailable: %w", ErrUnsupported)
	}

	input, inputChild, err := conptyPipe(false)
	if err != nil {
		return nil, err
	}

	defer func() { _ = windows.CloseHandle(inputChild) }()

	output, outputChild, err := conptyPipe(true)
	if err != nil {
		_ = input.Close()
		return nil, err
	}

	defer func() { _ = windows.CloseHandle(outputChild) }()

	var console windows.Handle

	coord := windows.Coord{X: int16(size.Cols), Y: int16(size.Rows)} // #nosec G115 -- Open supplies the fixed 80-column, 24-row default.
	if err := windows.CreatePseudoConsole(coord, inputChild, outputChild, 0, &console); err != nil {
		_ = input.Close()
		_ = output.Close()

		return nil, fmt.Errorf("CreatePseudoConsole: %w", err)
	}

	return &windowsTerminal{console: console, input: input, output: output, size: size}, nil
}

func (t *windowsTerminal) Start(ctx context.Context, command Command) (*Process, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed.Load() || t.console == 0 {
		return nil, os.ErrClosed
	}

	if t.started {
		return nil, errors.New("pty: terminal already started")
	}

	process, err := startConsoleProcess(t.console, command)
	if err != nil {
		return nil, err
	}

	t.started = true

	// Teardown runs off the reader's thread so it can drain ConPTY's final frame.
	return manageProcess(ctx, process, process.Wait, t.Close, t.finish), nil
}

func (t *windowsTerminal) Read(buf []byte) (int, error) {
	if t.closed.Load() {
		return 0, os.ErrClosed
	}

	return t.output.Read(buf)
}

func (t *windowsTerminal) Write(buf []byte) (int, error) { return t.input.Write(buf) }

func (t *windowsTerminal) SetWriteDeadline(deadline time.Time) error {
	return t.input.SetWriteDeadline(deadline)
}

func (t *windowsTerminal) interrupt() error {
	t.mu.Lock()
	closed, started := t.closed.Load(), t.started
	t.mu.Unlock()

	if closed {
		return os.ErrClosed
	}

	if !started {
		return os.ErrInvalid
	}

	_, err := t.input.Write([]byte{3})

	return err
}

func (t *windowsTerminal) eofByte() (byte, error) {
	if t.closed.Load() {
		return 0, os.ErrClosed
	}

	return 0, ErrUnsupported
}

func (t *windowsTerminal) Resize(size Winsize) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed.Load() || t.console == 0 {
		return os.ErrClosed
	}

	if size.X != 0 || size.Y != 0 {
		return ErrUnsupported
	}

	coord := windows.Coord{X: int16(size.Cols), Y: int16(size.Rows)} // #nosec G115 -- Terminal.Resize validates both dimensions against MaxInt16.
	if err := windows.ResizePseudoConsole(t.console, coord); err != nil {
		return err
	}

	t.size = size

	return nil
}

func (t *windowsTerminal) Size() (Winsize, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed.Load() {
		return Winsize{}, os.ErrClosed
	}

	return t.size, nil
}

func (t *windowsTerminal) Close() error {
	t.closeOnce.Do(func() {
		t.closed.Store(true)
		// Abort input immediately, but keep draining output while the native
		// console close flushes its final frame on synchronous Windows versions.
		t.closeErr = errors.Join(t.input.Close(), closeConsoleOutput(t.output, t.finish))
	})

	return t.closeErr
}

// closeConsoleOutput also releases a finish already blocked flushing output.
// The reader stays open until finish returns; closing it then aborts any reads
// still pending after an asynchronous ClosePseudoConsole.
func closeConsoleOutput(output *os.File, finish func()) error {
	drained := make(chan struct{})

	go func() {
		_, _ = io.Copy(io.Discard, output)

		close(drained)
	}()

	finish()

	err := output.Close()

	<-drained

	return err
}

func (t *windowsTerminal) finish() {
	t.finishOnce.Do(func() {
		t.mu.Lock()
		console := t.console
		t.console = 0
		t.mu.Unlock()
		windows.ClosePseudoConsole(console)
	})
}

// conptyPipe gives ConPTY a synchronous endpoint and Go an overlapped endpoint.
// A synchronous host endpoint cannot reliably cancel a blocked Read on Close.
func conptyPipe(hostReads bool) (*os.File, windows.Handle, error) {
	name := `\\.\pipe\skarm-pty-` + rand.Text()

	name16, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, 0, err
	}

	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, 0, err
	}

	descriptor, err := windows.SecurityDescriptorFromString("D:P(A;;GA;;;SY)(A;;GA;;;" + user.User.Sid.String() + ")")
	if err != nil {
		return nil, 0, err
	}

	attributes := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: descriptor}

	// ConPTY owns the synchronous server; Go opens the overlapped client itself
	// so the runtime owns its completion modes and registers it with IOCP.
	direction, access := uint32(windows.PIPE_ACCESS_INBOUND), os.O_WRONLY
	if hostReads {
		direction, access = windows.PIPE_ACCESS_OUTBOUND, os.O_RDONLY
	}

	server, err := windows.CreateNamedPipe(name16,
		direction|windows.FILE_FLAG_FIRST_PIPE_INSTANCE,
		windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT|windows.PIPE_REJECT_REMOTE_CLIENTS,
		1, 65536, 65536, 0, &attributes)
	if err != nil {
		return nil, 0, err
	}

	// #nosec G304 -- name is a freshly generated local pipe name with a private DACL.
	host, err := os.OpenFile(name, access|int(windows.O_FILE_FLAG_OVERLAPPED), 0)
	if err != nil {
		_ = windows.CloseHandle(server)
		return nil, 0, err
	}

	// OpenFile connected the client before ConnectNamedPipe, so
	// ERROR_PIPE_CONNECTED is a successful connection, not an error.
	if err := windows.ConnectNamedPipe(server, nil); err != nil && !errors.Is(err, windows.ERROR_PIPE_CONNECTED) {
		_ = host.Close()
		_ = windows.CloseHandle(server)

		return nil, 0, err
	}

	// OpenFile may silently leave a handle unregistered if IOCP setup fails.
	// Refuse it now: cancellation and Close require a pollable host endpoint.
	if err := host.SetDeadline(time.Time{}); err != nil {
		_ = host.Close()
		_ = windows.CloseHandle(server)

		return nil, 0, fmt.Errorf("ConPTY host pipe is not pollable: %w", err)
	}

	return host, server, nil
}

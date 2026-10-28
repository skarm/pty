package pty

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
)

// ErrUnsupported is returned when an operation is unavailable on the platform.
var ErrUnsupported = errors.New("unsupported")

// Terminal is a bidirectional terminal with independent process and I/O lifetimes.
// Each terminal can start one command. Read and Write may run concurrently with
// each other, Resize, and Close. Close aborts I/O and discards unread output;
// read to EOF before closing when all final output is needed.
// Darwin uses blocking terminal I/O; Close may wait for a read to finish.
type Terminal struct{ driver terminalDriver }

type terminalDriver interface {
	io.ReadWriteCloser
	Resize(Winsize) error
	Size() (Winsize, error)
	Start(context.Context, Command) (*Process, error)
}

// Open creates a terminal with 80 columns and 24 rows on Unix or Windows.
// Call [Terminal.Resize] before [Terminal.Start] to select another initial size.
// Windows requires ConPTY (Windows 10 1809 or later).
func Open() (*Terminal, error) {
	driver, err := openTerminal(Winsize{Rows: 24, Cols: 80})
	if err != nil {
		return nil, err
	}

	return &Terminal{driver: driver}, nil
}

// Start attaches one process to the terminal. It does not invoke a shell.
// A failed Start may be retried; each terminal permits one successful Start.
// Keep ctx alive until the process exits. Close the terminal to abort output I/O.
func (t *Terminal) Start(ctx context.Context, command Command) (*Process, error) {
	if t == nil || t.driver == nil || ctx == nil {
		return nil, os.ErrInvalid
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if command.Path == "" {
		return nil, errors.New("pty: empty command path")
	}

	for _, entry := range command.Env {
		if strings.ContainsRune(entry, 0) {
			return nil, errors.New("pty: environment contains NUL")
		}
	}

	return t.driver.Start(ctx, command)
}

// Read receives terminal output, including terminal escape sequences.
func (t *Terminal) Read(buf []byte) (int, error) {
	if t == nil || t.driver == nil {
		return 0, os.ErrInvalid
	}

	return t.driver.Read(buf)
}

// Write sends terminal input.
func (t *Terminal) Write(buf []byte) (int, error) {
	if t == nil || t.driver == nil {
		return 0, os.ErrInvalid
	}

	return t.driver.Write(buf)
}

// Resize changes the terminal dimensions. Windows rejects nonzero pixel sizes.
func (t *Terminal) Resize(size Winsize) error {
	if t == nil || t.driver == nil {
		return os.ErrInvalid
	}

	if err := validateTerminalSize(size); err != nil {
		return err
	}

	return t.driver.Resize(size)
}

// Size returns the current dimensions. Windows returns the last successful size
// requested by this host; the ConPTY API has no size query.
func (t *Terminal) Size() (Winsize, error) {
	if t == nil || t.driver == nil {
		return Winsize{}, os.ErrInvalid
	}

	return t.driver.Size()
}

// Close releases terminal resources and discards unread output. It is idempotent.
// On Windows, Close interrupts pending I/O and terminates attached console clients.
// On Darwin, Close may wait for an outstanding read to finish.
func (t *Terminal) Close() error {
	if t == nil || t.driver == nil {
		return os.ErrInvalid
	}

	return t.driver.Close()
}

func validateTerminalSize(size Winsize) error {
	if size.Rows == 0 || size.Cols == 0 || size.Rows > 32767 || size.Cols > 32767 {
		return errors.New("pty: rows and columns must be between 1 and 32767")
	}

	return nil
}

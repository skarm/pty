//go:build !plan9 && !aix

package pty

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fileInputDriver struct {
	terminalDriver
	input   *os.File
	entered chan struct{}
	once    sync.Once
	calls   atomic.Int32
}

func (d *fileInputDriver) Write(buf []byte) (int, error) {
	d.calls.Add(1)
	d.once.Do(func() { close(d.entered) })
	return d.input.Write(buf)
}

func (d *fileInputDriver) SetWriteDeadline(deadline time.Time) error {
	return d.input.SetWriteDeadline(deadline)
}

func newInputPipe(t *testing.T) (*Terminal, *fileInputDriver, *os.File) {
	t.Helper()
	writer, reader := inputPipe(t)
	t.Cleanup(func() { _ = writer.Close(); _ = reader.Close() })
	driver := &fileInputDriver{input: writer, entered: make(chan struct{})}
	return &Terminal{driver: driver, writeGate: make(chan struct{}, 1)}, driver, reader
}

func TestWriteContextCancelsBlockedIOWithoutClosingInput(t *testing.T) {
	terminal, driver, reader := newInputPipe(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	payload := bytes.Repeat([]byte("x"), 1<<20)
	n, err := terminal.WriteContext(ctx, payload)
	if n < 0 || n >= len(payload) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked WriteContext = %d, %v", n, err)
	}
	drained := make(chan []byte, 1)
	go func() { data, _ := io.ReadAll(reader); drained <- data }()
	const marker = "AFTER_CANCEL"
	if n, err := terminal.WriteContext(context.Background(), []byte(marker)); n != len(marker) || err != nil {
		t.Fatalf("write after cancellation = %d, %v", n, err)
	}
	_ = driver.input.Close()
	select {
	case got := <-drained:
		if !bytes.HasSuffix(got, []byte(marker)) {
			t.Fatalf("missing subsequent write: %d bytes", len(got))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("input did not drain after cancellation")
	}
}

func TestWriteContextCancelsQueuedWriter(t *testing.T) {
	terminal, driver, _ := newInputPipe(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := terminal.WriteContext(ctx, bytes.Repeat([]byte("x"), 1<<20)); done <- err }()
	select {
	case <-driver.entered:
	case err := <-done:
		t.Fatalf("active writer did not reach pipe: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("active writer did not start")
	}
	queued, stop := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer stop()
	n, err := terminal.WriteContext(queued, []byte("MUST_NOT_WRITE"))
	if n != 0 || !errors.Is(err, context.DeadlineExceeded) || driver.calls.Load() != 1 {
		t.Fatalf("queued writer reached input: n=%d err=%v calls=%d", n, err, driver.calls.Load())
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("first writer: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("active write did not cancel")
	}
}

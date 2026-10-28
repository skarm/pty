//go:build !windows && !plan9 && !aix

package pty

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

type unixTerminal struct {
	mu              sync.Mutex
	master, slave   *os.File
	closed, started bool
	closeOnce       sync.Once
	closeErr        error
}

func openTerminal(size Winsize) (terminalDriver, error) {
	master, slave, err := open()
	if err != nil {
		return nil, err
	}

	if err := setsize(master, &size); err != nil {
		_ = slave.Close()
		_ = master.Close()

		return nil, err
	}

	return &unixTerminal{master: master, slave: slave}, nil
}

func (t *unixTerminal) Start(ctx context.Context, command Command) (*Process, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return nil, os.ErrClosed
	}

	if t.started {
		return nil, errors.New("pty: terminal already started")
	}
	//nolint:noctx // Process owns context cancellation and terminal teardown together.
	cmd := exec.Command(command.Path, command.Args...) // #nosec G204 -- Starting the caller-specified executable is the API contract.
	cmd.Dir, cmd.Env = command.Dir, command.Env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = t.slave, t.slave, t.slave

	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	t.started = true
	_ = t.slave.Close()
	t.slave = nil

	return manageProcess(ctx, cmd.Process, func() (*os.ProcessState, error) {
		err := cmd.Wait()
		return cmd.ProcessState, err
	}, t.Close, nil), nil
}

func (t *unixTerminal) Read(buf []byte) (int, error) {
	n, err := t.master.Read(buf)
	if errors.Is(err, syscall.EIO) {
		err = io.EOF
	}

	return n, err
}

func (t *unixTerminal) Write(buf []byte) (int, error) { return t.master.Write(buf) }

func (t *unixTerminal) SetWriteDeadline(deadline time.Time) error {
	return t.master.SetWriteDeadline(deadline)
}

func (t *unixTerminal) Resize(size Winsize) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return os.ErrClosed
	}

	return setsize(t.master, &size)
}

func (t *unixTerminal) Size() (Winsize, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return Winsize{}, os.ErrClosed
	}

	var size Winsize
	if err := getsize(t.master, &size); err != nil {
		return Winsize{}, err
	}

	return size, nil
}

func (t *unixTerminal) Close() error {
	t.closeOnce.Do(func() {
		t.mu.Lock()
		t.closed = true
		slave := t.slave
		t.slave = nil
		t.mu.Unlock()

		if slave != nil {
			t.closeErr = slave.Close()
		}

		t.closeErr = errors.Join(t.closeErr, t.master.Close())
	})

	return t.closeErr
}

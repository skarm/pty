package pty

import (
	"context"
	"os"
	"os/exec"
)

// Command describes a process attached to a Terminal. Args excludes the program
// name. A nil Env inherits the environment; a non-nil Env replaces it.
// Environment entries containing NUL are rejected.
// Path uses os/exec executable lookup. Windows drive-relative paths must be
// made absolute. Dir sets the working directory; Windows preserves SYSTEMROOT.
type Command struct {
	Path string
	Args []string
	Env  []string
	Dir  string
}

// Process owns a child process. Wait may be called concurrently and repeatedly.
// The child is reaped even if Wait is not called. Canceling the Start context
// while the child is running kills it and closes its terminal. Always close the
// terminal to release its I/O resources.
type Process struct {
	process *os.Process
	done    chan struct{}
	state   *os.ProcessState
	err     error
}

// PID returns the operating-system process identifier.
func (p *Process) PID() int {
	if p == nil || p.process == nil {
		return 0
	}

	return p.process.Pid
}

// Kill terminates the child process. It does not replace closing the terminal.
func (p *Process) Kill() error {
	if p == nil || p.process == nil {
		return os.ErrInvalid
	}

	return p.process.Kill()
}

// Wait returns the exit state and error. An unsuccessful exit returns an
// *[exec.ExitError]. Wait does not wait for the caller to consume terminal output.
func (p *Process) Wait() (*os.ProcessState, error) {
	if p == nil || p.done == nil {
		return nil, os.ErrInvalid
	}

	<-p.done

	return p.state, p.err
}

func manageProcess(ctx context.Context, process *os.Process, wait func() (*os.ProcessState, error), closeTerminal func() error, finish func()) *Process {
	p := &Process{process: process, done: make(chan struct{})}
	stop := context.AfterFunc(ctx, func() {
		// Kill is asynchronous. Reap first so PTY teardown cannot wake the
		// child with EOF and let it exit successfully before the signal acts.
		// A failed kill must not prevent terminal I/O from being closed.
		if err := process.Kill(); err == nil {
			<-p.done
		}

		_ = closeTerminal()
	})

	go func() {
		p.state, p.err = wait()
		if p.err == nil && !p.state.Success() {
			p.err = &exec.ExitError{ProcessState: p.state}
		}

		stop()
		// Publish the exit result before teardown, which may wait for output to drain.
		close(p.done)

		if finish != nil {
			finish()
		}
	}()

	return p
}

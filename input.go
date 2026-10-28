package pty

import (
	"context"
	"errors"
	"os"
	"time"
)

// WriteContext sends input, returning the number of bytes accepted by the
// terminal. Cancellation interrupts this operation without closing the terminal
// or killing its process. A partial write is not rolled back and must not be
// replayed automatically. Writes, SendEOF and Interrupt are serialized; waiting
// for another writer also observes ctx. Output reads remain independent.
// A completed write may succeed concurrently with context cancellation.
func (t *Terminal) WriteContext(ctx context.Context, buf []byte) (int, error) {
	return t.withWriteContext(ctx, func() (int, error) { return t.driver.Write(buf) })
}

// Interrupt requests a foreground application interrupt. Unix sends SIGINT to
// the terminal's current foreground process group. Windows sends Ctrl+C input
// through ConPTY; applications that disable processed input receive it as input
// instead of a console control event. Success does not guarantee process exit.
// Cancellation stops waiting for or writing input without closing the terminal.
func (t *Terminal) Interrupt(ctx context.Context) error {
	_, err := t.withWriteContext(ctx, func() (int, error) { return 0, t.driver.interrupt() })
	return err
}

// SendEOF writes the terminal's configured EOF character on Unix in canonical
// mode. It returns the accepted byte count (zero or one); a nonempty line buffer
// may be delivered without ending input. It does not close either stream.
// Raw mode, a disabled EOF character and Windows ConPTY return ErrUnsupported
// before writing. ConPTY has no equivalent canonical EOF operation; Ctrl+Z is
// application-specific and is not substituted for EOF.
func (t *Terminal) SendEOF(ctx context.Context) (int, error) {
	return t.withWriteContext(ctx, func() (int, error) {
		value, err := t.driver.eofByte()
		if err != nil {
			return 0, err
		}

		return t.driver.Write([]byte{value})
	})
}

func (t *Terminal) withWriteContext(ctx context.Context, operation func() (int, error)) (int, error) {
	if t == nil || t.driver == nil || ctx == nil {
		return 0, os.ErrInvalid
	}

	if err := ctx.Err(); err != nil {
		return 0, err
	}

	select {
	case t.writeGate <- struct{}{}:
		defer func() { <-t.writeGate }()
	case <-ctx.Done():
		return 0, ctx.Err()
	}

	if err := ctx.Err(); err != nil {
		return 0, err
	}

	if ctx.Done() == nil {
		return operation()
	}

	return t.writeWithDeadline(ctx, operation)
}

func (t *Terminal) writeWithDeadline(ctx context.Context, operation func() (int, error)) (int, error) {
	deadline, _ := ctx.Deadline()
	if err := t.driver.SetWriteDeadline(deadline); err != nil {
		return 0, err
	}

	canceled := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		// Closing a terminal may race cancellation; it already aborts the write.
		_ = t.driver.SetWriteDeadline(time.Now())

		close(canceled)
	})
	n, err := operation()

	if !stop() {
		<-canceled
	}

	resetErr := t.driver.SetWriteDeadline(time.Time{})

	if errors.Is(err, os.ErrDeadlineExceeded) {
		if ctxErr := ctx.Err(); ctxErr != nil {
			err = ctxErr
		} else if !deadline.IsZero() && !time.Now().Before(deadline) {
			// The I/O deadline may fire before the context timer publishes Err.
			err = context.DeadlineExceeded
		}
	}

	return n, errors.Join(err, resetErr)
}

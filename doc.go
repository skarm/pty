// Package pty manages terminal sessions through Unix PTYs and Windows ConPTY.
//
// [Open] creates a [Terminal] with 80 columns and 24 rows. Terminal implements
// io.ReadWriteCloser and owns its input, output and dimensions; a [Process]
// owns the child process. Call [Terminal.Resize] before [Terminal.Start] when
// the child needs a different initial size.
//
// # Starting a process
//
// Call [Terminal.Start] with a context and a [Command]. Each terminal permits
// one successful Start; a failed Start can be retried. Command.Args excludes
// the program name. Commands execute directly without an implicit shell.
// Command.Dir sets the working directory. A nil Command.Env inherits the parent
// environment; a non-nil slice replaces it. Windows retains required SYSTEMROOT.
//
// # I/O and shutdown
//
// Service input and output concurrently: terminal buffers are bounded. Output
// can include virtual terminal escape sequences. The caller owns any external
// input reader and must arrange cancellation if that reader can block.
//
// [Process.Wait] reports the exit state independently of output consumption.
// It may be called repeatedly or concurrently. The child is reaped in the
// background even when Wait is not called. [Process.Kill] terminates the child;
// the terminal must still be closed.
//
// Read concurrently while the child runs and continue to EOF before
// [Terminal.Close]. Some Unix systems can discard unread output during terminal
// teardown, so reading to EOF cannot guarantee preservation of every byte.
// Close releases
// terminal resources, discards unread output and is idempotent. Always close
// a terminal, including when Start fails. Canceling the Start context while
// the child is running kills the child and closes the terminal. After process
// exit, use Close to abort remaining output I/O.
//
// # Dimensions and platforms
//
// [Winsize] specifies cell and pixel dimensions. Rows and Cols must be between
// 1 and 32767. [Terminal.Resize] changes the dimensions; [Terminal.Size] reports
// them. Windows supports cell dimensions only, requires zero X and Y, and Size
// returns the last successfully requested size because ConPTY has no size query.
//
// Windows requires Windows 10 1809 or Windows Server 2019 or later. ConPTY
// streams are UTF-8. On Darwin, Close interrupts an outstanding master read
// even while the child holds the slave open.
//
// Plan 9 and AIX return [ErrUnsupported].
//
// This package is a fork of https://github.com/creack/pty.
package pty

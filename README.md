# pty

A Go library for Unix pseudo-terminals and Windows ConPTY, with one
`Terminal` / `Process` API.

This repository is a fork of [creack/pty](https://github.com/creack/pty).
It uses the import path `github.com/skarm/pty` and retains the upstream
[MIT license](LICENSE).

## Install

Requires Go 1.26 or later.

```sh
go get github.com/skarm/pty
```

## API

```go
terminal, err := pty.Open()
```

`Open()` creates a terminal with 80 columns and 24 rows. Call `Resize` before
`Start` when the child needs a different initial size. `Terminal` implements
`io.ReadWriteCloser` and has:

- `Start(ctx, Command) (*Process, error)` — attach one child process.
- `Resize(Winsize) error` — change terminal dimensions.
- `Size() (Winsize, error)` — read the current dimensions.
- `SetMode(Mode) error` — configure echo and canonical input before Start on
  Linux and macOS; other platforms return `ErrUnsupported`.
- `Close() error` — release terminal resources; repeated calls are safe.
- `WriteContext(ctx, []byte) (int, error)` — cancel one input write without
  closing the terminal or killing its process; queued writers also observe ctx.
- `Interrupt(ctx) error` — send SIGINT to the Unix foreground process group, or
  Ctrl+C input through Windows ConPTY. With Windows processed input disabled,
  the application receives the Ctrl+C byte instead of a console control event.
- `SendEOF(ctx) (int, error)` — send the current canonical EOF character on Unix.
  Raw mode, a disabled EOF character and Windows ConPTY return `ErrUnsupported`.
  This operation does not close input or output and does not substitute Ctrl+Z.

`Command` contains `Path`, `Args`, `Dir` and `Env`. `Args` excludes the program
name. Arguments are passed directly; use an explicit shell command when shell
syntax is needed. A nil `Env` inherits the parent environment; a non-nil slice
replaces it. Windows preserves required `SYSTEMROOT`. Use absolute paths for
Windows drive-relative executable names. Environment entries containing NUL
are rejected; Windows also rejects malformed name=value entries.

`Process` provides `PID() int`, `Kill() error`, `Stop(ctx) error` and
`Wait() (*os.ProcessState, error)`. Wait is safe to call repeatedly or
concurrently. A nonzero exit status returns an `*exec.ExitError`. The library
reaps the child even if Wait is not called.

On Unix, Stop sends SIGTERM to the direct child and waits; if its context
expires, Stop kills and reaps the child and returns the context error. It does
not terminate descendants or close the terminal. Windows returns ErrUnsupported
for Stop because ConPTY has no equivalent portable graceful signal.

## Example

This program runs a command that finishes without interactive input and copies
its terminal output to stdout. Pass the executable and its arguments on the
program's command line.

```go
package main

import (
    "context"
    "errors"
    "io"
    "log"
    "os"
    "os/signal"

    "github.com/skarm/pty"
)

func run(ctx context.Context, args []string) error {
    if len(args) == 0 {
        return errors.New("usage: terminal <program> [args...]")
    }
    terminal, err := pty.Open()
    if err != nil {
        return err
    }
    defer func() { _ = terminal.Close() }()

    process, err := terminal.Start(ctx, pty.Command{
        Path: args[0],
        Args: args[1:],
    })
    if err != nil {
        return err
    }
    _, readErr := io.Copy(os.Stdout, terminal)
    if readErr != nil {
        _ = process.Kill()
    }
    _, waitErr := process.Wait()
    return errors.Join(readErr, waitErr, terminal.Close())
}

func main() {
    ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
    defer stop()
    if err := run(ctx, os.Args[1:]); err != nil {
        log.Fatal(err)
    }
}
```

For an interactive application, service input and output concurrently with
`terminal.Write` and `terminal.Read`. The caller owns any input reader: a reader
that can block indefinitely, such as stdin, needs its own cancellation strategy.
Forward resize notifications by calling `terminal.Resize` with the new size.
Use `WriteContext` when an individual input operation needs cancellation.
`Write`, `WriteContext`, `Interrupt` and `SendEOF` are serialized independently
of output reads. An expired or canceled queued operation does not reach input.
Partial input writes return the reported byte count and are not rolled back;
do not automatically resend them. A write completed concurrently with
cancellation may return success. Canceling an input operation leaves the
terminal available for subsequent writes.

`Interrupt` requests interruption and does not guarantee process exit.
`SendEOF` writes one configured character; when a Unix canonical line buffer is
nonempty, that can deliver the pending line without ending input. Windows has
no equivalent canonical EOF operation in ConPTY: Ctrl+Z interpretation depends
on the client application, so the library returns `ErrUnsupported` without
sending input.

## Lifecycle

A terminal permits one successful Start. A failed Start may be retried.
Canceling its context while the child is running kills the child and closes the
terminal. After the child exits, use Close to abort any remaining output I/O.
Kill terminates the child process; it does not replace closing the terminal.

Wait reports the process result independently of output consumption. Read
concurrently while the child runs and continue to EOF before Close. Close
discards unread output. Some Unix systems can discard unread PTY output during
terminal teardown when a short-lived child exits, so reading to EOF cannot
guarantee preservation of bytes not yet read from the kernel.
Always close the terminal, including when Start fails.

## Platforms

Unix uses native PTYs; Windows uses ConPTY without CGO. Windows requires
Windows 10 1809 / Windows Server 2019 or later. Rows and columns must be between
1 and 32767. Pixel dimensions (`X`, `Y`) are available on Unix; Windows requires
both to be zero. Windows Size returns the last successfully requested size,
since ConPTY does not expose a size query. Windows output is UTF-8 with virtual
terminal escape sequences.
The host owns overlapped pipe endpoints opened through Go and verifies deadline
support before Open succeeds; failure to register with the runtime poller is
returned as an error.

On Darwin, Open requires a pollable master and fails if Go's poller cannot
register it. Close can interrupt an outstanding read even while the child holds
the slave open.

Plan 9 and AIX return `ErrUnsupported`.

## Checks

CI is configured for Go 1.26 and 1.27 on Linux, macOS, Windows Server 2022 and
Windows Server 2025. It includes lint, vet, race checks, repeated lifecycle tests
and a Linux GC stack-shrink regression. Cross-build checks cover the remaining
targets in `test_crosscompile.sh`.

Runtime tests have run on macOS and Linux ARM64, FreeBSD/OpenBSD/NetBSD ARM64
VMs, DragonFly BSD x86-64 under QEMU, and Windows 11 ARM64. Windows amd64 and
386 test binaries have also run under Windows ARM64 emulation. The Windows
Server CI jobs above have not been run from this fork yet.

//go:build linux || darwin || freebsd || openbsd || netbsd || dragonfly

package pty_test

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/skarm/pty"
)

func TestChildStdioHelper(t *testing.T) {
	if os.Getenv("PTY_STDIO_HELPER") == "" {
		return
	}
	for fd := 0; fd < 3; fd++ {
		flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
		if err != nil || flags&unix.O_NONBLOCK != 0 {
			fmt.Printf("NONBLOCK:%d:%v\n", fd, err)
			os.Exit(1)
		}
	}
	fmt.Println("STDIO_BLOCKING")
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
	os.Exit(0)
}

func TestChildStdioIsBlocking(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	term := newTerminal(t)
	process, err := term.Start(ctx, pty.Command{Path: os.Args[0], Args: []string{"-test.run=^TestChildStdioHelper$"}, Env: append(os.Environ(), "PTY_STDIO_HELPER=1")})
	if err != nil {
		t.Fatal(err)
	}
	probe := newTerminalProbe(term)
	if err := probe.expect(ctx, 0, []byte("STDIO_BLOCKING")); err != nil {
		t.Fatal(err)
	}
	if _, err := term.Write([]byte("done\r")); err != nil {
		t.Fatal(err)
	}
	if _, err := process.Wait(); err != nil {
		t.Fatal(err)
	}
}

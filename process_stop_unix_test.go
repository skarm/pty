//go:build !windows && !plan9 && !aix

package pty_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"

	"github.com/skarm/pty"
)

func TestStopHelper(t *testing.T) {
	if os.Getenv("PTY_STOP_HELPER") == "" {
		return
	}
	if os.Getenv("PTY_STOP_HELPER") == "ignore" {
		signal.Ignore(syscall.SIGTERM)
	}
	fmt.Println("READY")
	for {
		time.Sleep(time.Hour)
	}
}

func TestProcessStop(t *testing.T) {
	for _, ignore := range []bool{false, true} {
		t.Run(fmt.Sprint("ignore=", ignore), func(t *testing.T) {
			if ignore && testing.Short() {
				t.Skip("forced termination waits for the Stop deadline")
			}
			term := newTerminal(t)
			mode := "normal"
			if ignore {
				mode = "ignore"
			}
			p, err := term.Start(context.Background(), pty.Command{Path: os.Args[0], Args: []string{"-test.run=^TestStopHelper$"}, Env: append(os.Environ(), "PTY_STOP_HELPER="+mode)})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = p.Kill(); _, _ = p.Wait() })
			if _, err := bufio.NewReader(term).ReadString('\n'); err != nil {
				t.Fatal(err)
			}
			canceled, cancelEarly := context.WithCancel(context.Background())
			cancelEarly()
			if err := p.Stop(canceled); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled Stop: %v", err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			if ignore {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), 100*time.Millisecond)
			}
			defer cancel()
			err = p.Stop(ctx)
			if ignore && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("Stop: %v", err)
			}
			if !ignore && err == nil {
				t.Fatal("SIGTERM should report unsuccessful exit")
			}
			state, _ := p.Wait()
			if state == nil {
				t.Fatal("missing process state")
			}
		})
	}
}

package pty_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/skarm/pty"
)

// terminalProbe is a raw byte test driver. A mark prevents a previous turn
// from satisfying an expectation in the current turn.
type terminalProbe struct {
	mu      sync.Mutex
	data    []byte
	err     error
	changed chan struct{}
}

func newTerminalProbe(term *pty.Terminal) *terminalProbe {
	p := &terminalProbe{changed: make(chan struct{})}
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := term.Read(buf)
			p.mu.Lock()
			p.data = append(p.data, buf[:n]...)
			p.err = err
			close(p.changed)
			p.changed = make(chan struct{})
			p.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	return p
}

func (p *terminalProbe) mark() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.data)
}

func (p *terminalProbe) expect(ctx context.Context, after int, pattern []byte) error {
	for {
		p.mu.Lock()
		if bytes.Contains(p.data[after:], pattern) {
			p.mu.Unlock()
			return nil
		}
		err, changed := p.err, p.changed
		p.mu.Unlock()
		if err != nil {
			return fmt.Errorf("terminal ended before %q: %w", pattern, err)
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return fmt.Errorf("waiting for %q: %w", pattern, ctx.Err())
		}
	}
}

func TestInteractiveDialog(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	term := newTerminal(t)
	process, err := term.Start(ctx, terminalFixture(t, "dialog"))
	if err != nil {
		t.Fatal(err)
	}
	probe := newTerminalProbe(term)
	if err := probe.expect(ctx, 0, []byte("PROMPT>")); err != nil {
		t.Fatal(err)
	}
	mark := probe.mark()
	if _, err := io.WriteString(term, "hello\r"); err != nil {
		t.Fatal(err)
	}
	if err := probe.expect(ctx, mark, []byte("ANSWER:hello:END")); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(term, "done\r"); err != nil {
		t.Fatal(err)
	}
	if _, err := process.Wait(); err != nil {
		t.Fatal(err)
	}
}

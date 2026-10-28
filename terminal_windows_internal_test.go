//go:build windows

package pty

import (
	"bytes"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// TestCloseConsoleOutput models both synchronous final-frame flushing and a
// close that returns before its output writer disconnects.
func TestCloseConsoleOutput(t *testing.T) {
	for _, tc := range []struct {
		name             string
		flush            bool
		alreadyFinishing bool
	}{
		{name: "flush during finish", flush: true},
		{name: "finish already flushing", flush: true, alreadyFinishing: true},
		{name: "finish leaves writer open"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output, writer, err := conptyPipe(true)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_ = output.Close()
				_ = windows.CloseHandle(writer)
			})

			// Exceed the pipe capacity so finish cannot return without a reader.
			payload := bytes.Repeat([]byte("x"), 256<<10)
			var written uint32
			var writeErr error
			var once sync.Once
			entered := make(chan struct{})
			finish := func() {
				once.Do(func() {
					close(entered)
					if tc.flush {
						writeErr = windows.WriteFile(writer, payload, &written, nil)
					}
				})
			}
			if tc.alreadyFinishing {
				go finish()
				<-entered
			}

			closed := make(chan error, 1)
			go func() { closed <- closeConsoleOutput(output, finish) }()
			select {
			case err := <-closed:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("close blocked while finishing console output")
			}
			if tc.flush && (writeErr != nil || int(written) != len(payload)) {
				t.Fatalf("final flush: wrote %d of %d bytes: %v", written, len(payload), writeErr)
			}
		})
	}
}

func TestConPTYPipeDeadlines(t *testing.T) {
	for _, test := range []struct {
		name      string
		hostReads bool
	}{{"input", false}, {"output", true}} {
		t.Run(test.name, func(t *testing.T) {
			host, peer, err := conptyPipe(test.hostReads)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = host.Close(); _ = windows.CloseHandle(peer) })
			if err := host.SetDeadline(time.Now().Add(-time.Second)); err != nil {
				t.Fatal(err)
			}
			var buffer [16]byte
			var n int
			if test.hostReads {
				n, err = host.Read(buffer[:])
			} else {
				n, err = host.Write([]byte("blocked"))
			}
			if n != 0 || !errors.Is(err, os.ErrDeadlineExceeded) {
				t.Fatalf("expired deadline: n=%d err=%v", n, err)
			}
			if err := host.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
				t.Fatal(err)
			}
			payload := []byte("still open")
			var transferred uint32
			if test.hostReads {
				if err := windows.WriteFile(peer, payload, &transferred, nil); err != nil || int(transferred) != len(payload) {
					t.Fatalf("peer write after deadline: n=%d err=%v", transferred, err)
				}
				n, err = host.Read(buffer[:])
			} else {
				if n, err := host.Write(payload); err != nil || n != len(payload) {
					t.Fatalf("host write after deadline: n=%d err=%v", n, err)
				}
				err = windows.ReadFile(peer, buffer[:], &transferred, nil)
				n = int(transferred)
			}
			if err != nil || !bytes.Equal(buffer[:n], payload) {
				t.Fatalf("I/O after deadline: data=%q err=%v", buffer[:n], err)
			}
		})
	}
}

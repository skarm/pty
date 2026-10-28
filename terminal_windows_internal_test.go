//go:build windows

package pty

import (
	"bytes"
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

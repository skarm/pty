//go:build aix || plan9

package pty

func openTerminal(Winsize) (terminalDriver, error) { return nil, ErrUnsupported }

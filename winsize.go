package pty

// Winsize describes terminal cell and pixel dimensions. Rows and Cols must be
// between 1 and 32767. Windows requires zero X and Y.
type Winsize struct {
	Rows uint16
	Cols uint16
	X    uint16
	Y    uint16
}

//go:build ignore
// +build ignore

package pty

/*
#include <sys/time.h>
#include <stdlib.h>
#include <sys/tty.h>
*/
import "C"

type ptmget C.struct_ptmget

const (
	ioctlTIOCPTSNAME = C.TIOCPTSNAME
	ioctlTIOCGRANTPT = C.TIOCGRANTPT
)

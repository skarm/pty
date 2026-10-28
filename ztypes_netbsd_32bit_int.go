//go:build (386 || amd64 || arm || arm64) && netbsd
// +build 386 amd64 arm arm64
// +build netbsd

package pty

type ptmget struct {
	Cfd int32
	Sfd int32
	Cn  [1024]int8
	Sn  [1024]int8
}

const (
	ioctlTIOCPTSNAME = 0x48087448
	ioctlTIOCGRANTPT = 0x20007447
)

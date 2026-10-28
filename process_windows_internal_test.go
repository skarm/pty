//go:build windows

package pty

import (
	"fmt"
	"math/rand/v2"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestConsoleEnvironment(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  []string
		want []uint16
	}{
		{
			name: "sort by name",
			env:  []string{"A0=2", "A=1"},
			want: []uint16{65, 61, 49, 0, 65, 48, 61, 50, 0, 0},
		},
		{
			name: "case insensitive",
			env:  []string{"Z=2", "a=1"},
			want: []uint16{97, 61, 49, 0, 90, 61, 50, 0, 0},
		},
		{
			name: "UTF-16 surrogate pair",
			env:  []string{"A=😀"},
			want: []uint16{65, 61, 0xd83d, 0xde00, 0, 0},
		},
		{
			name: "invalid UTF-8",
			env:  []string{"A=\xff"},
			want: []uint16{65, 61, 0xfffd, 0, 0},
		},
		{
			name: "truncated UTF-8",
			env:  []string{"A=\xe2\x82"},
			want: []uint16{65, 61, 0xfffd, 0xfffd, 0, 0},
		},
		{
			name: "separator in value",
			env:  []string{"A=="},
			want: []uint16{65, 61, 61, 0, 0},
		},
		{
			name: "empty block",
			env:  []string{},
			want: []uint16{0, 0},
		},
		{
			name: "drive directory",
			env:  []string{"Z=1", `=C:=C:\work`},
			want: []uint16{61, 67, 58, 61, 67, 58, 92, 119, 111, 114, 107, 0, 90, 61, 49, 0, 0},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := slices.Clone(tc.env)
			got, err := consoleEnvironment(tc.env)
			if err != nil || !slices.Equal(got, tc.want) {
				t.Fatalf("block=%v error=%v want %v", got, err, tc.want)
			}
			if !slices.Equal(tc.env, before) {
				t.Fatalf("input changed: got %q want %q", tc.env, before)
			}
		})
	}
}

func TestConsoleEnvironmentRejectsInvalidEntries(t *testing.T) {
	for _, entry := range []string{"", "NO_SEPARATOR", "=NO_SEPARATOR", "==value", "A=bad\x00value"} {
		if got, err := consoleEnvironment([]string{entry}); err == nil || got != nil {
			t.Fatalf("accepted invalid entry %q: block=%v error=%v", entry, got, err)
		}
	}
}

// BenchmarkConsoleEnvironment measures environment preparation without process creation.
func BenchmarkConsoleEnvironment(b *testing.B) {
	for _, count := range []int{16, 128, 1024} {
		for _, mixedCase := range []bool{false, true} {
			b.Run(fmt.Sprintf("vars=%d/mixedCase=%t", count, mixedCase), func(b *testing.B) {
				env := make([]string, count)
				var size int64
				for i := range env {
					key := fmt.Sprintf("PTY_VAR_%04d", i)
					if mixedCase {
						key = fmt.Sprintf("ptY_Var_%04d", i)
					}
					value := strings.Repeat("value-", 10)
					if i%4 == 0 {
						value += "мир😀"
					}
					env[i] = key + "=" + value
					size += int64(len(env[i]))
				}
				random := rand.New(rand.NewPCG(1, 2))
				random.Shuffle(len(env), func(i, j int) { env[i], env[j] = env[j], env[i] })
				b.SetBytes(size)
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					block, err := consoleEnvironment(env)
					if err != nil {
						b.Fatal(err)
					}
					runtime.KeepAlive(block)
				}
			})
		}
	}
}

// BenchmarkConsoleEnvironmentUnicodeHeavy exercises UTF-16 sizing and encoding.
func BenchmarkConsoleEnvironmentUnicodeHeavy(b *testing.B) {
	env := make([]string, 128)
	for i := range env {
		env[i] = fmt.Sprintf("ptY_Var_%04d=", 127-i) + strings.Repeat("明😀", 32)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		block, err := consoleEnvironment(env)
		if err != nil {
			b.Fatal(err)
		}
		runtime.KeepAlive(block)
	}
}

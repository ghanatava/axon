//go:build linux && amd64

package main

import (
	"encoding/binary"
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestKernelMapABI(t *testing.T) {
	for _, tc := range []struct {
		v    any
		size int
	}{{kernelConnectionKey{}, 24}, {kernelConnectionState{}, 64}, {kernelCallKey{}, 32}, {kernelCall{}, 48}} {
		if got := binary.Size(tc.v); got != tc.size {
			t.Fatalf("%T size %d want %d", tc.v, got, tc.size)
		}
	}
}
func TestProcessBirthIdentity(t *testing.T) {
	raw, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(raw)[strings.LastIndexByte(string(raw), ')')+1:])
	ticks, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	pid := uint32(os.Getpid())
	if !processAlive(pid, ticks*10_000_000) || processAlive(pid, (ticks+1)*10_000_000) {
		t.Fatal("process birth identity ignored")
	}
}

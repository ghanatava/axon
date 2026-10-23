// Package tlscapture describes the target Go ABI and decodes TLS probe events.
package tlscapture

import (
	"debug/buildinfo"
	"debug/dwarf"
	"debug/elf"
	"fmt"
	"strings"
)

type Target struct {
	Version      string
	RegisterABI  bool
	GoIDOffset   uint64
	LayoutSource string
}

// Inspect reads runtime.g's layout from the target, never the agent's Go version.
// Stripped binaries use standard-release runtime layouts verified against Go source.
// Unknown versions require DWARF rather than guessing an unsafe memory offset.
func Inspect(path string) (Target, error) {
	var t Target
	f, err := elf.Open(path)
	if err != nil {
		return t, err
	}
	defer f.Close()
	if f.Machine != elf.EM_X86_64 {
		return t, fmt.Errorf("TLS capture requires Linux amd64")
	}
	bi, err := buildinfo.ReadFile(path)
	if err != nil {
		return t, err
	}
	t.Version = bi.GoVersion
	var major, minor int
	if _, err := fmt.Sscanf(strings.TrimPrefix(t.Version, "go"), "%d.%d", &major, &minor); err != nil || major != 1 || minor < 16 {
		return t, fmt.Errorf("unsupported Go version %q (requires Go 1.16+)", t.Version)
	}
	t.RegisterABI = minor >= 17
	for _, setting := range bi.Settings {
		if setting.Key == "GOEXPERIMENT" && strings.Contains(setting.Value, "noregabi") {
			return t, fmt.Errorf("unsupported GOEXPERIMENT=%s", setting.Value)
		}
	}
	d, err := f.DWARF()
	if err != nil {
		if f.Section(".debug_info") != nil || f.Section(".zdebug_info") != nil {
			return t, fmt.Errorf("reading target DWARF: %w", err)
		}
		offset, ok := releaseGoIDOffset(minor)
		if !ok {
			return t, fmt.Errorf("no stripped runtime layout for %s; provide an unstripped binary or add a verified layout", t.Version)
		}
		t.GoIDOffset = offset
		t.LayoutSource = "release layout (stripped)"
		return t, nil
	}
	t.LayoutSource = "DWARF"
	r := d.Reader()
	for {
		e, err := r.Next()
		if err != nil {
			return t, err
		}
		if e == nil {
			break
		}
		if e.Tag != dwarf.TagStructType || e.Val(dwarf.AttrName) != "runtime.g" {
			continue
		}
		typ, err := d.Type(e.Offset)
		if err != nil {
			return t, err
		}
		st, ok := typ.(*dwarf.StructType)
		if !ok {
			continue
		}
		stackOK := false
		for _, field := range st.Field {
			if field.Name == "stack" && field.ByteOffset == 0 {
				stackType := field.Type
				for {
					alias, ok := stackType.(*dwarf.TypedefType)
					if !ok {
						break
					}
					stackType = alias.Type
				}
				stack, ok := stackType.(*dwarf.StructType)
				if ok {
					lo, hi := false, false
					for _, part := range stack.Field {
						lo = lo || (part.Name == "lo" && part.ByteOffset == 0)
						hi = hi || (part.Name == "hi" && part.ByteOffset == 8)
					}
					stackOK = lo && hi
				}
			}
			if field.Name == "goid" {
				t.GoIDOffset = uint64(field.ByteOffset)
			}
		}
		if !stackOK || t.GoIDOffset == 0 {
			return t, fmt.Errorf("unsupported runtime.g stack/goid layout")
		}
		return t, nil
	}
	return t, fmt.Errorf("runtime.g.goid not found in target DWARF")
}

// Linux amd64 standard runtime.g layouts. Source: Go release runtime/runtime2.go.
// Go 1.23 adds syscallbp; Go 1.25 removes gobuf.ret, undoing the size increase.
// stack.lo/hi remain at offsets 0/8 across these releases.
func releaseGoIDOffset(minor int) (uint64, bool) {
	switch {
	case minor >= 16 && minor <= 22:
		return 152, true
	case minor == 23 || minor == 24:
		return 160, true
	case minor == 25 || minor == 26:
		return 152, true
	default:
		return 0, false
	}
}

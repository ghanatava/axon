// Package symbols resolves Go function symbols in a compiled binary and
// finds RET instruction offsets inside them -- the basis for uprobe
// placement on Go binaries (see EPIC.md).
package symbols

import (
	"debug/elf"
	"fmt"
)

// Symbol is a resolved function. Address is a virtual address (where the
// function lives once loaded); FileOffset is its byte position in the
// file itself. perf_event_open (uprobe attachment) wants FileOffset, not
// Address -- they differ whenever a section's link-time virtual address
// isn't equal to its file offset, which is normal, not an edge case.
type Symbol struct {
	Name       string
	Address    uint64
	Size       uint64
	FileOffset uint64
}

// Resolve finds a function symbol by its Go-mangled name (e.g.
// "main.classify", "crypto/tls.(*Conn).Write").
//
// Tries .symtab first, falls back to .gopclntab. -s strips .symtab but
// not .gopclntab -- the Go runtime needs it for panics/reflection, so it
// survives stripping. Size semantics differ slightly between the two
// (gopclntab's End can include trailing alignment padding); callers
// bounding disassembly should treat Size as an upper bound, not exact.
func Resolve(path, name string) (*Symbol, error) {
	f, err := elf.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening ELF file: %w", err)
	}
	defer f.Close()

	if sym, err := resolveViaSymtab(f, name); err == nil {
		return sym, nil
	}

	sym, err := resolveViaGopclntab(f, name)
	if err != nil {
		return nil, fmt.Errorf("symbol %q not found via .symtab or .gopclntab: %w", name, err)
	}
	return sym, nil
}

func resolveViaSymtab(f *elf.File, name string) (*Symbol, error) {
	syms, err := f.Symbols()
	if err != nil {
		return nil, err // no .symtab -- expected on stripped binaries
	}
	for _, s := range syms {
		if s.Name == name {
			off, err := fileOffset(f, s.Value)
			if err != nil {
				return nil, err
			}
			return &Symbol{Name: s.Name, Address: s.Value, Size: s.Size, FileOffset: off}, nil
		}
	}
	return nil, fmt.Errorf("%q not in .symtab", name)
}

// fileOffset converts a virtual address to its byte position in the file,
// via whichever section contains it. Same containment check as RetSites.
func fileOffset(f *elf.File, addr uint64) (uint64, error) {
	for _, sec := range f.Sections {
		if sec.Addr != 0 && sec.Addr <= addr && addr < sec.Addr+sec.Size {
			return addr - sec.Addr + sec.Offset, nil
		}
	}
	return 0, fmt.Errorf("no section contains address 0x%x", addr)
}

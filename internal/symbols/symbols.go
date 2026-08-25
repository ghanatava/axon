// Package symbols resolves Go function symbols from a binary's ELF symbol
// table -- step one before placing a uprobe. See EPIC.md.
package symbols

import (
	"debug/elf"
	"fmt"
)

// Symbol is a resolved function: address and size (st_value/st_size).
type Symbol struct {
	Name    string
	Address uint64
	Size    uint64
}

// Resolve finds symbol `name` (Go-mangled, e.g. "main.classify" or
// "crypto/tls.(*Conn).Write") in the ELF binary at path.
//
// Unsure of the name? go tool nm <binary> | grep <partial-name>
func Resolve(path, name string) (*Symbol, error) {
	f, err := elf.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening ELF file: %w", err)
	}
	defer f.Close()

	syms, err := f.Symbols()
	if err != nil {
		return nil, fmt.Errorf(
			"reading symbol table (binary may be stripped -- rebuild "+
				"without -ldflags=\"-s -w\"): %w", err)
	}
	for _, s := range syms {
		if s.Name == name {
			return &Symbol{Name: s.Name, Address: s.Value, Size: s.Size}, nil
		}
	}
	return nil, fmt.Errorf(
		"symbol %q not found -- list candidates with: go tool nm <binary> | grep <partial-name>",
		name)
}

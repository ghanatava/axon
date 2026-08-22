// Package symbols resolves Go function symbols inside a compiled binary's
// ELF symbol table -- the first piece needed before we can place a uprobe
// anywhere. See EPIC.md's concepts register for the fuller picture.
package symbols

import (
    "debug/elf"
    "fmt"
)

// Symbol describes a resolved function: its virtual address and size, as
// recorded in the ELF symbol table (st_value and st_size, in ELF terms).

type Symbol struct {
	Name    string
	Address uint64
	Size    uint64
}


// Resolve finds a named function symbol in the given ELF binary.
//
// name must be the Go-mangled symbol name, e.g. "main.classify" for a
// function `classify` in package main. Later, for a method on a type,
// it'll look like "crypto/tls.(*Conn).Write" -- note the parens.
//
// If you're not sure of the exact name, list candidates on the built
// binary with: go tool nm <binary> | grep <partial-name>

func Resolve(path,name string) (*Symbol,error){
    f,err := elf.Open(path)
    if err!=nil{
        return nil, fmt.Errorf("opening ELF file: %w", err)
    }
    defer f.Close()

    syms,err := f.Symbols()
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
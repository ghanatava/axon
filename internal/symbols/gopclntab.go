package symbols

import (
	"debug/elf"
	"debug/gosym"
	"fmt"
)

// resolveViaGopclntab resolves a function via Go's own pclntab, for
// binaries built with -ldflags="-s -w". Go still needs this table at
// runtime (stack traces, reflection), so strip doesn't remove it.
func resolveViaGopclntab(f *elf.File, name string) (*Symbol, error) {
	pclntab := f.Section(".gopclntab")
	if pclntab == nil {
		return nil, fmt.Errorf(".gopclntab not present")
	}
	pclntabData, err := pclntab.Data()
	if err != nil {
		return nil, fmt.Errorf("reading .gopclntab: %w", err)
	}

	text := f.Section(".text")
	if text == nil {
		return nil, fmt.Errorf(".text not present")
	}

	// nil symtab param is the pre-Go1.2 legacy format -- not relevant to
	// any binary built with a modern toolchain.
	table, err := gosym.NewTable(nil, gosym.NewLineTable(pclntabData, text.Addr))
	if err != nil {
		return nil, fmt.Errorf("parsing pclntab: %w", err)
	}

	fn := table.LookupFunc(name)
	if fn == nil {
		return nil, fmt.Errorf("%q not in .gopclntab", name)
	}

	off, err := fileOffset(f, fn.Entry)
	if err != nil {
		return nil, err
	}

	// fn.End can include trailing alignment padding past the real last
	// instruction -- treat as upper bound, verified against int3 bytes
	// harmlessly disassembling as valid single-byte instructions.
	return &Symbol{Name: name, Address: fn.Entry, Size: fn.End - fn.Entry, FileOffset: off}, nil
}

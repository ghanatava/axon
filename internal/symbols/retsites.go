package symbols

import (
	"debug/elf"
	"fmt"

	"golang.org/x/arch/x86/x86asm"
)

// RetSites returns function-relative offsets of every RET in `name`.
// Replaces uretprobes -- Go relocates goroutine stacks, which corrupts a
// uretprobe's patched return address mid-flight. amd64 only.
func RetSites(path, name string) ([]uint64, error) {
	f, err := elf.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening ELF file: %w", err)
	}
	defer f.Close()

	sym, err := Resolve(path, name)
	if err != nil {
		return nil, err
	}
	if sym.Size == 0 {
		return nil, fmt.Errorf("symbol %q has zero size -- can't bound disassembly", name)
	}

	var text *elf.Section
	for _, sec := range f.Sections {
		if sec.Addr != 0 && sec.Addr <= sym.Address && sym.Address < sec.Addr+sec.Size {
			text = sec
			break
		}
	}
	if text == nil {
		return nil, fmt.Errorf("no section contains address 0x%x", sym.Address)
	}

	data, err := text.Data()
	if err != nil {
		return nil, fmt.Errorf("reading section data: %w", err)
	}

	start := sym.Address - text.Addr
	code := data[start : start+sym.Size]

	var rets []uint64
	offset := uint64(0)
	for offset < uint64(len(code)) {
		inst, err := x86asm.Decode(code[offset:], 64)
		if err != nil {
			return nil, fmt.Errorf("disassembling at offset 0x%x: %w", offset, err)
		}
		if inst.Op == x86asm.RET {
			rets = append(rets, offset)
		}
		offset += uint64(inst.Len)
	}

	if len(rets) == 0 {
		return nil, fmt.Errorf("no RET found in %q -- inlined?", name)
	}
	return rets, nil
}

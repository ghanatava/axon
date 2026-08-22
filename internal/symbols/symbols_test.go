package symbols

import "testing"

// This test proves Resolve() correctly reads the ELF symbol table, by
// checking its answer against what `go tool nm` independently reported
// for the same binary -- two different readers of the same on-disk
// structure, so agreement here is a real correctness check, not a
// tautology.
//
// NOTE: 0x499b40 is the exact address YOUR build produced. This will
// almost certainly change if you rebuild (different Go version, any code
// change, even toolchain patch versions can shift addresses) -- this test
// is a one-time proof, not something to leave hardcoded long-term. We'll
// talk about how to make this durable once it passes once.
func TestResolveClassify(t *testing.T) {
	const binPath = "../../testtargets/retsites/classify"
	const wantAddr = uint64(0x499b40)

	sym, err := Resolve(binPath, "main.classify")
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}

	t.Logf("resolved %s: address=0x%x size=%d", sym.Name, sym.Address, sym.Size)

	if sym.Address != wantAddr {
		t.Errorf("address mismatch: got 0x%x, want 0x%x (from `nm`)", sym.Address, wantAddr)
	}
	if sym.Size == 0 {
		t.Error("size is 0 -- Phase 1's disassembly step needs a nonzero size to bound itself")
	}
}

func TestRetSitesClassify(t *testing.T) {
	const binPath = "../../testtargets/retsites/classify"

	rets, err := RetSites(binPath, "main.classify")
	if err != nil {
		t.Fatalf("RetSites failed: %v", err)
	}

	t.Logf("found %d RET site(s):", len(rets))
	for i, off := range rets {
		t.Logf("  [%d] offset 0x%x", i, off)
	}

	if len(rets) != 3 {
		t.Errorf("expected 3 RET sites (one per return statement in classify), got %d", len(rets))
	}
}

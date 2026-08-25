package symbols

import "testing"

// Cross-checks Resolve() against `go tool nm` for the same binary -- two
// independent readers of the symbol table agreeing is a real check.
//
// wantAddr is this build's actual address, will drift on rebuild (Go
// version, toolchain patch, any code change). One-time proof, not durable.
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

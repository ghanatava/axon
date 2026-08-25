package symbols

import "testing"

func TestResolveClassifyStripped(t *testing.T) {
	const binPath = "../../testtargets/retsites/classify_stripped"
	const wantAddr = uint64(0x499b40)

	sym, err := Resolve(binPath, "main.classify")
	if err != nil {
		t.Fatalf("Resolve failed on stripped binary: %v", err)
	}

	t.Logf("resolved %s: address=0x%x size=%d", sym.Name, sym.Address, sym.Size)

	if sym.Address != wantAddr {
		t.Errorf("address mismatch: got 0x%x, want 0x%x", sym.Address, wantAddr)
	}
	if sym.Size == 0 {
		t.Error("size is 0")
	}
}

func TestRetSitesClassifyStripped(t *testing.T) {
	const binPath = "../../testtargets/retsites/classify_stripped"

	rets, err := RetSites(binPath, "main.classify")
	if err != nil {
		t.Fatalf("RetSites failed on stripped binary: %v", err)
	}

	t.Logf("found %d RET site(s) on stripped binary:", len(rets))
	for i, off := range rets {
		t.Logf("  [%d] offset 0x%x", i, off)
	}

	if len(rets) != 3 {
		t.Errorf("expected 3 RET sites, got %d", len(rets))
	}
}

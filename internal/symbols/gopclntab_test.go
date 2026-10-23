package symbols

import "testing"

func TestResolveClassifyStripped(t *testing.T) {
	plain, err := Resolve(buildClassify(t, false), "main.classify")
	if err != nil {
		t.Fatal(err)
	}
	stripped, err := Resolve(buildClassify(t, true), "main.classify")
	if err != nil {
		t.Fatal(err)
	}
	if stripped.Address != plain.Address || stripped.FileOffset != plain.FileOffset || stripped.Size == 0 {
		t.Fatalf("stripped %+v differs from unstripped %+v", stripped, plain)
	}
}
func TestRetSitesClassifyStripped(t *testing.T) {
	plain, err := RetSites(buildClassify(t, false), "main.classify")
	if err != nil {
		t.Fatal(err)
	}
	stripped, err := RetSites(buildClassify(t, true), "main.classify")
	if err != nil {
		t.Fatal(err)
	}
	if len(plain) != len(stripped) {
		t.Fatalf("RET count differs: plain %v stripped %v", plain, stripped)
	}
	for i := range plain {
		if plain[i] != stripped[i] {
			t.Fatalf("RET offsets differ: plain %v stripped %v", plain, stripped)
		}
	}
}

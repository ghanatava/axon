package symbols

import (
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func buildClassify(t *testing.T, stripped bool) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "classify")
	args := []string{"build", "-o", bin}
	if stripped {
		args = append(args, "-ldflags=-s -w")
	}
	args = append(args, "../../testtargets/retsites/main.go")
	if out, err := exec.Command("go", args...).CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

// Cross-check an independently built fixture against Go's symbol reader.
func TestResolveClassify(t *testing.T) {
	bin := buildClassify(t, false)
	sym, err := Resolve(bin, "main.classify")
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("go", "tool", "nm", bin).Output()
	if err != nil {
		t.Fatal(err)
	}
	var want uint64
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 3 && fields[2] == "main.classify" {
			want, err = strconv.ParseUint(fields[0], 16, 64)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if want == 0 || sym.Address != want || sym.Size == 0 || sym.FileOffset == 0 {
		t.Fatalf("resolved %+v; nm address %#x", sym, want)
	}
}
func TestRetSitesClassify(t *testing.T) {
	rets, err := RetSites(buildClassify(t, false), "main.classify")
	if err != nil {
		t.Fatal(err)
	}
	if len(rets) != 3 {
		t.Fatalf("expected 3 RET sites, got %v", rets)
	}
}

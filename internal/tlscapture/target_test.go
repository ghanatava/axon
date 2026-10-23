package tlscapture

import (
	"github.com/ghanatava/axon/internal/symbols"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestTargetMatrix(t *testing.T) {
	compilers := []struct {
		path string
		reg  bool
	}{{"go", true}}
	if old := os.Getenv("AXON_GO116"); old != "" {
		compilers = append(compilers, struct {
			path string
			reg  bool
		}{old, false})
	}
	for _, compiler := range strings.Split(os.Getenv("AXON_GO_EXTRA"), ":") {
		if compiler != "" {
			compilers = append(compilers, struct {
				path string
				reg  bool
			}{compiler, true})
		}
	}
	for _, compiler := range compilers {
		t.Run(compiler.path, func(t *testing.T) {
			var dwarfTarget Target
			for _, stripped := range []bool{false, true} {
				name := "unstripped"
				if stripped {
					name = "stripped"
				}
				t.Run(name, func(t *testing.T) {
					bin := filepath.Join(t.TempDir(), "fixture")
					args := []string{"build", "-o", bin}
					if stripped {
						args = append(args, "-ldflags=-s -w")
					}
					args = append(args, "../../testtargets/tlsphase2/main.go")
					cmd := exec.Command(compiler.path, args...)
					for _, variable := range os.Environ() {
						if !strings.HasPrefix(variable, "GOROOT=") && !strings.HasPrefix(variable, "GO111MODULE=") {
							cmd.Env = append(cmd.Env, variable)
						}
					}
					cmd.Env = append(cmd.Env, "GO111MODULE=off")
					if out, err := cmd.CombinedOutput(); err != nil {
						t.Fatalf("build: %v\n%s", err, out)
					}
					target, err := Inspect(bin)
					if err != nil {
						t.Fatal(err)
					}
					if !stripped {
						dwarfTarget = target
					} else if target.GoIDOffset != dwarfTarget.GoIDOffset || target.RegisterABI != dwarfTarget.RegisterABI {
						t.Fatalf("stripped layout %+v differs from DWARF %+v", target, dwarfTarget)
					}
					for _, symbol := range []string{"crypto/tls.(*Conn).Read", "crypto/tls.(*Conn).Write"} {
						if _, err := symbols.Resolve(bin, symbol); err != nil {
							t.Fatal(err)
						}
						if _, err := symbols.RetSites(bin, symbol); err != nil {
							t.Fatal(err)
						}
					}
					if target.RegisterABI != compiler.reg || target.GoIDOffset == 0 {
						t.Fatalf("bad ABI/layout: %+v", target)
					}
				})
			}
		})
	}
}

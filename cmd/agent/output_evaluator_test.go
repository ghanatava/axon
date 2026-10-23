//go:build linux && amd64

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/ghanatava/axon/internal/h2demux"
	"github.com/ghanatava/axon/internal/tlscapture"
	"os"
	"path/filepath"
	"testing"
)

func TestEvaluatorReplay(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       error
		bad        bool
	}{{"valid", `{"PID":1,"Conn":2,"GoID":3,"Total":9,"Data":"AAAABAAAAAAA"}`, nil, false}, {"malformed", `{"PID":`, nil, true}, {"null", `null`, nil, true}, {"partial", `{"PID":1,"Conn":2,"GoID":3,"Total":1,"Data":"UA=="}`, h2demux.ErrIncomplete, true}, {"missing chunk", `{"PID":1,"Conn":2,"GoID":3,"Total":9,"Chunked":true,"Data":"AAAA"}`, h2demux.ErrIncomplete, true}} {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "events")
			os.WriteFile(p, []byte(tc.body), 0600)
			var out bytes.Buffer
			proc, e := newProcessor("h2", &out)
			if e != nil {
				t.Fatal(e)
			}
			e = replayEvents(p, proc)
			if (e != nil) != tc.bad {
				t.Fatal(e)
			}
			if tc.want != nil && !errors.Is(e, tc.want) {
				t.Fatal(e)
			}
			if !tc.bad {
				var record map[string]any
				if e = json.Unmarshal(out.Bytes(), &record); e != nil {
					t.Fatal(e)
				}
				if record["type"] != "SETTINGS" || record["sequence"] != float64(1) {
					t.Fatal(record)
				}
			}
		})
	}
	p, _ := newProcessor("h2", &bytes.Buffer{})
	if replayEvents(filepath.Join(t.TempDir(), "missing"), p) == nil {
		t.Fatal("missing path accepted")
	}
	if _, e := newProcessor("invalid", &bytes.Buffer{}); e == nil {
		t.Fatal("mode accepted")
	}
}

type evalBrokenWriter struct{}

func (evalBrokenWriter) Write([]byte) (int, error) { return 0, errors.New("closed output") }
func TestEvaluatorOutputErrors(t *testing.T) {
	for _, mode := range []string{"raw", "h2"} {
		p, _ := newProcessor(mode, evalBrokenWriter{})
		if e := p.Process(tlscapture.Event{PID: 1, Conn: 2, GoID: 3, Total: 9, Data: []byte{0, 0, 0, 4, 0, 0, 0, 0, 0}}); e == nil {
			t.Fatal("output error discarded", mode)
		}
	}
}

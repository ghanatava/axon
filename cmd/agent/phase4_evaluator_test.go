//go:build linux && amd64

package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"github.com/ghanatava/axon/internal/h2demux"
	"github.com/ghanatava/axon/internal/tlscapture"
	"golang.org/x/net/http2/hpack"
	"os"
	"path/filepath"
	"testing"
)

func phase4Wire(typ byte, flags byte, id uint32, p []byte) []byte {
	b := make([]byte, 9+len(p))
	b[0] = byte(len(p) >> 16)
	b[1] = byte(len(p) >> 8)
	b[2] = byte(len(p))
	b[3] = typ
	b[4] = flags
	binary.BigEndian.PutUint32(b[5:], id)
	copy(b[9:], p)
	return b
}
func phase4Head(fields ...string) []byte {
	var b bytes.Buffer
	e := hpack.NewEncoder(&b)
	for i := 0; i < len(fields); i += 2 {
		e.WriteField(hpack.HeaderField{Name: fields[i], Value: fields[i+1]})
	}
	return b.Bytes()
}
func TestPhase4EvaluatorFullReplay(t *testing.T) {
	req := append([]byte(h2demux.ClientPreface), phase4Wire(4, 0, 0, nil)...)
	req = append(req, phase4Wire(1, 5, 1, phase4Head(":method", "POST", ":path", "/eval/Call", "content-type", "application/grpc"))...)
	resp := append(phase4Wire(4, 0, 0, nil), phase4Wire(1, 5, 1, phase4Head(":status", "200", "content-type", "application/grpc", "grpc-status", "7", "grpc-message", "denied%20+%20reason"))...)
	events := []tlscapture.Event{{PID: 1, Conn: 2, GoID: 3, Direction: 1, Started: 10, Finished: 20, Total: uint64(len(req)), Data: req}, {PID: 1, Conn: 2, GoID: 4, Started: 30, Finished: 40, Total: uint64(len(resp)), Data: resp}}
	var input bytes.Buffer
	enc := json.NewEncoder(&input)
	for _, e := range events {
		enc.Encode(e)
	}
	path := filepath.Join(t.TempDir(), "events")
	os.WriteFile(path, input.Bytes(), 0600)
	var out bytes.Buffer
	p, err := newProcessor("grpc", &out)
	if err != nil {
		t.Fatal(err)
	}
	if err = replayEvents(path, p); err != nil {
		t.Fatal(err)
	}
	var call struct {
		Method        string
		Status        int
		StatusMessage string `json:"status_message"`
		Latency       uint64 `json:"latency_ns"`
	}
	if err = json.Unmarshal(out.Bytes(), &call); err != nil {
		t.Fatal(err)
	}
	if call.Method != "/eval/Call" || call.Status != 7 || call.StatusMessage != "denied + reason" || call.Latency != 20 {
		t.Fatal(call)
	}
	p, _ = newProcessor("grpc", evalBrokenWriter{})
	for _, e := range events {
		err = p.Process(e)
	}
	if err == nil {
		t.Fatal("discarded JSON output error")
	}
	// Premature EOF must fail closed even if all HTTP/2 frames are intact.
	os.WriteFile(path, bytes.SplitN(input.Bytes(), []byte{'\n'}, 2)[0], 0600)
	p, _ = newProcessor("grpc", &bytes.Buffer{})
	if err = replayEvents(path, p); err == nil {
		t.Fatal("open call accepted at EOF")
	}
}

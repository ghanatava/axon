package session

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"github.com/ghanatava/axon/internal/h2demux"
	"github.com/ghanatava/axon/internal/tlscapture"
	"golang.org/x/net/http2/hpack"
	"testing"
	"time"
)

func wire(kind, flags byte, id uint32, payload []byte) []byte {
	b := make([]byte, 9+len(payload))
	b[0] = byte(len(payload) >> 16)
	b[1] = byte(len(payload) >> 8)
	b[2] = byte(len(payload))
	b[3] = kind
	b[4] = flags
	binary.BigEndian.PutUint32(b[5:], id)
	copy(b[9:], payload)
	return b
}
func headers(fields ...string) []byte {
	var b bytes.Buffer
	e := hpack.NewEncoder(&b)
	for i := 0; i < len(fields); i += 2 {
		e.WriteField(hpack.HeaderField{Name: fields[i], Value: fields[i+1]})
	}
	return b.Bytes()
}
func envelope(token string) []byte {
	b := make([]byte, 5+len(token))
	binary.BigEndian.PutUint32(b[1:], uint32(len(token)))
	copy(b[5:], token)
	return b
}
func traffic(id uint32, token string, first bool) ([]byte, []byte) {
	var req, res []byte
	if first {
		req = append([]byte(h2demux.ClientPreface), wire(4, 0, 0, nil)...)
		res = wire(4, 0, 0, nil)
	}
	req = append(req, wire(1, 4, id, headers(":method", "POST", ":path", "/test/"+token, "content-type", "application/grpc"))...)
	req = append(req, wire(0, 1, id, envelope(token))...)
	res = append(res, wire(1, 4, id, headers(":status", "200", "content-type", "application/grpc"))...)
	res = append(res, wire(0, 0, id, envelope(token))...)
	res = append(res, wire(1, 5, id, headers("grpc-status", "0"))...)
	return req, res
}
func event(key Key, dir uint32, sequence uint64, data []byte) tlscapture.Event {
	return tlscapture.Event{Version: 3, PID: key.PID, ProcessStart: key.ProcessStart, Conn: key.Conn, Generation: key.Generation, Direction: dir, Sequence: sequence, Started: sequence * 100, Finished: sequence*100 + 50, GoID: 100 + uint64(dir), TID: 1, Total: uint64(len(data)), Data: data, Chunked: true}
}
func must(t *testing.T, r *Runtime, e tlscapture.Event) Result {
	t.Helper()
	out, err := r.Push(e)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func newRuntime(t *testing.T, c Config) *Runtime {
	t.Helper()
	if c.Mode == "" {
		c.Mode = "grpc"
	}
	r, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func TestConnectionIncarnationsAndPIDReuse(t *testing.T) {
	r := newRuntime(t, Config{})
	keys := []Key{{1, 100, 7, 1}, {2, 200, 7, 1}, {1, 300, 7, 1}}
	for i, k := range keys {
		req, _ := traffic(1, fmt.Sprint(i), true)
		must(t, r, event(k, 1, 1, req))
	}
	for i, k := range keys {
		_, res := traffic(1, fmt.Sprint(i), true)
		out := must(t, r, event(k, 0, 1, res))
		if len(out.Calls) != 1 || string(out.Calls[0].Request[0].Data) != fmt.Sprint(i) || out.Calls[0].ProcessStart != k.ProcessStart {
			t.Fatalf("cross attribution: %+v", out)
		}
	}
	old := keys[0]
	closed := event(old, 0, 0, nil)
	closed.Kind = tlscapture.Close
	must(t, r, closed)
	next := old
	next.Generation = 2
	req, res := traffic(1, "reused", true)
	must(t, r, event(next, 1, 1, req))
	must(t, r, event(old, 0, 2, res))
	out := must(t, r, event(next, 0, 1, res))
	if len(out.Calls) != 1 || out.Calls[0].Generation != 2 || out.Calls[0].Method != "/test/reused" {
		t.Fatalf("pointer reuse: %+v", out)
	}
}
func TestLossIsolationAndLateData(t *testing.T) {
	for _, kind := range []string{"gap", "offset", "duplicate", "producer"} {
		t.Run(kind, func(t *testing.T) {
			r := newRuntime(t, Config{})
			bad := Key{1, 1, 1, 1}
			good := Key{1, 1, 2, 2}
			req, res := traffic(1, "good", true)
			e := event(bad, 1, 1, req)
			switch kind {
			case "gap":
				e.Sequence = 2
			case "offset":
				e.Offset = 1
			case "duplicate":
				must(t, r, e)
			case "producer":
				lost := e
				lost.Kind = tlscapture.Loss
				lost.Total = 0
				lost.Data = nil
				must(t, r, lost)
			}
			must(t, r, e)
			if out := must(t, r, event(bad, 0, 1, res)); len(out.Calls) != 0 {
				t.Fatal("lost connection emitted call")
			}
			must(t, r, event(good, 1, 1, req))
			out := must(t, r, event(good, 0, 1, res))
			if len(out.Calls) != 1 || out.Calls[0].Conn != 2 {
				t.Fatal("healthy connection affected")
			}
			if r.Metrics().Invalidated == 0 {
				t.Fatal("loss not counted")
			}
		})
	}
}
func TestCloseIdleAndResourceBounds(t *testing.T) {
	now := time.Unix(0, 0)
	r := newRuntime(t, Config{Now: func() time.Time { return now }, IdleTimeout: time.Second, MaxConnections: 2})
	k := Key{1, 1, 1, 1}
	req, _ := traffic(1, "open", true)
	must(t, r, event(k, 1, 1, req))
	closeEvent := event(k, 0, 0, nil)
	closeEvent.Kind = tlscapture.Close
	must(t, r, closeEvent)
	if m := r.Metrics(); m.Active != 0 || m.RetainedBytes != 0 || m.Incomplete != 1 {
		t.Fatal(m)
	}
	if out := must(t, r, event(k, 1, 1, req)); len(out.Calls) != 0 {
		t.Fatal("late data reopened closed generation")
	}
	k.Generation = 2
	must(t, r, event(k, 1, 1, req))
	now = now.Add(2 * time.Second)
	r.Sweep()
	if m := r.Metrics(); m.Active != 0 || m.RetainedBytes != 0 || m.Expired != 1 {
		t.Fatal(m)
	}
	must(t, r, event(k, 1, 2, req))
	if r.Metrics().Active != 0 {
		t.Fatal("idle connection silently resynchronized")
	}
	for i := 3; i < 20; i++ {
		next := Key{1, 1, uint64(i), uint64(i)}
		must(t, r, event(next, 1, 1, req))
	}
	if len(r.states) > 2 || len(r.latest) > 2 {
		t.Fatal("unbounded identities")
	}
	tiny := newRuntime(t, Config{MaxBufferedBytes: 1})
	must(t, tiny, event(k, 1, 1, req))
	if m := tiny.Metrics(); m.Active != 0 || m.RetainedBytes != 0 || m.Invalidated != 1 {
		t.Fatal(m)
	}
}
func TestLongLivedConnectionAndThreadMigration(t *testing.T) {
	r := newRuntime(t, Config{})
	k := Key{10, 20, 30, 40}
	for i := 0; i < 5000; i++ {
		req, res := traffic(uint32(1+2*i), fmt.Sprint(i), i == 0)
		a := event(k, 1, uint64(i+1), req)
		a.TID = uint32(1 + i%2)
		must(t, r, a)
		out := must(t, r, event(k, 0, uint64(i+1), res))
		if len(out.Calls) != 1 || string(out.Calls[0].Response[0].Data) != fmt.Sprint(i) {
			t.Fatalf("call %d misattributed", i)
		}
	}
	if m := r.Metrics(); m.Calls != 5000 || m.RetainedBytes > 140000 || m.GoroutineMigrations == 0 {
		t.Fatal(m)
	}
	if err := r.Finish(); err != nil {
		t.Fatal(err)
	}
}
func TestChunkSequenceAndFinish(t *testing.T) {
	r := newRuntime(t, Config{})
	k := Key{1, 1, 1, 1}
	req, res := traffic(1, "chunked", true)
	a := event(k, 1, 1, req)
	a.Data = req[:7]
	must(t, r, a)
	a.Offset = 7
	a.Data = req[7:]
	must(t, r, a)
	out := must(t, r, event(k, 0, 1, res))
	if len(out.Calls) != 1 {
		t.Fatal(out)
	}
	if err := r.Finish(); err != nil {
		t.Fatal(err)
	}
}

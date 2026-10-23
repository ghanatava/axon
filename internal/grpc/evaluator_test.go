package grpc

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"github.com/ghanatava/axon/internal/h2demux"
	"golang.org/x/net/http2/hpack"
	"testing"
)

func evalBlock(e *hpack.Encoder, b *bytes.Buffer, fields ...string) []byte {
	b.Reset()
	for i := 0; i < len(fields); i += 2 {
		if err := e.WriteField(hpack.HeaderField{Name: fields[i], Value: fields[i+1]}); err != nil {
			panic(err)
		}
	}
	return append([]byte(nil), b.Bytes()...)
}
func evalFrame(d h2demux.Direction, id uint32, typ h2demux.Type, flags uint8, p []byte) h2demux.Frame {
	return h2demux.Frame{Stream: h2demux.Stream{Connection: h2demux.Connection{PID: 7, Conn: 8}, ID: id}, Direction: d, Type: typ, Flags: flags, Payload: p, StartedNS: 90, FinishedNS: 100}
}
func evalEnvelope(p []byte, compressed bool) []byte {
	b := make([]byte, 5+len(p))
	if compressed {
		b[0] = 1
	}
	binary.BigEndian.PutUint32(b[1:], uint32(len(p)))
	copy(b[5:], p)
	return b
}
func evalPush(t *testing.T, tr *Tracker, f h2demux.Frame) []Call {
	t.Helper()
	c, e := tr.Push(f)
	if e != nil {
		t.Fatal(e)
	}
	return c
}
func TestEvaluatorDynamicTablesAndResponseFirst(t *testing.T) {
	for _, rd := range []h2demux.Direction{h2demux.Read, h2demux.Write} {
		t.Run(rd.String(), func(t *testing.T) {
			tr, _ := New(Config{})
			var req, res bytes.Buffer
			qe, re := hpack.NewEncoder(&req), hpack.NewEncoder(&res)
			// An ordinary HTTP stream inserts the path before the gRPC stream reuses it.
			evalPush(t, tr, evalFrame(rd, 1, h2demux.Headers, h2demux.EndHeaders|h2demux.EndStream, evalBlock(qe, &req, ":method", "GET", ":path", "/svc/Call", "content-type", "text/plain")))
			evalPush(t, tr, evalFrame(1-rd, 1, h2demux.Headers, h2demux.EndHeaders|h2demux.EndStream, evalBlock(re, &res, ":status", "200")))
			response := evalBlock(re, &res, ":status", "200", "content-type", "application/grpc", "grpc-status", "3", "grpc-message", "bad%20input%20+%20details")
			evalPush(t, tr, evalFrame(1-rd, 3, h2demux.Headers, h2demux.EndHeaders|h2demux.EndStream, response))
			header := evalBlock(qe, &req, ":method", "POST", ":path", "/svc/Call", "content-type", "application/grpc")
			// Padding, priority and split HPACK all apply together.
			padded := append([]byte{2, 0, 0, 0, 0, 0}, header[:len(header)/2]...)
			padded = append(padded, 0, 0)
			evalPush(t, tr, evalFrame(rd, 3, h2demux.Headers, h2demux.Padded|h2demux.HasPriority, padded))
			evalPush(t, tr, evalFrame(rd, 3, h2demux.Continuation, h2demux.EndHeaders, header[len(header)/2:]))
			data := append(evalEnvelope([]byte("payload"), false), evalEnvelope(nil, false)...)
			for _, b := range data[:len(data)-1] {
				evalPush(t, tr, evalFrame(rd, 3, h2demux.Data, 0, []byte{b}))
			}
			calls := evalPush(t, tr, evalFrame(rd, 3, h2demux.Data, h2demux.EndStream, data[len(data)-1:]))
			if len(calls) != 1 {
				t.Fatal(calls)
			}
			c := calls[0]
			if c.Method != "/svc/Call" || c.Status != 3 || c.StatusMessage != "bad input + details" || len(c.Request) != 2 || string(c.Request[0].Data) != "payload" || c.RequestBytes != 7 || c.RequestWireBytes != 17 || c.RequestDirection != rd.String() || !c.TimingValid {
				t.Fatalf("bad call %+v", c)
			}
			if e := tr.Finish(); e != nil {
				t.Fatal(e)
			}
		})
	}
}
func TestEvaluatorMessageBoundsAndCompression(t *testing.T) {
	cfg, _ := defaults(Config{MaxMessageBytes: 100, MaxCallBytes: 110, MaxMessages: 4})
	var z bytes.Buffer
	w := gzip.NewWriter(&z)
	w.Write(bytes.Repeat([]byte("x"), 100))
	w.Close()
	p := messageParser{}
	budget := 0
	m, e := p.push(evalEnvelope(z.Bytes(), true), "gzip", cfg, &budget, 4)
	if e != nil || len(m) != 1 || len(m[0].Data) != 100 || !m[0].Compressed {
		t.Fatal(m, e)
	}
	for _, tc := range []struct {
		name, enc string
		wire      []byte
		want      error
	}{{"unsupported", "br", evalEnvelope(z.Bytes(), true), ErrProtocol}, {"badflag", "identity", []byte{2, 0, 0, 0, 0}, ErrProtocol}, {"wirelimit", "identity", evalEnvelope(make([]byte, 101), false), ErrLimit}, {"count", "identity", append(evalEnvelope(nil, false), evalEnvelope(nil, false)...), ErrLimit}} {
		t.Run(tc.name, func(t *testing.T) {
			p := messageParser{}
			n := 0
			remaining := 4
			if tc.name == "count" {
				remaining = 1
			}
			_, e := p.push(tc.wire, tc.enc, cfg, &n, remaining)
			if !errors.Is(e, tc.want) {
				t.Fatal(e)
			}
		})
	}
	z.Reset()
	w = gzip.NewWriter(&z)
	w.Write(make([]byte, 101))
	w.Close()
	p = messageParser{}
	budget = 0
	if _, e = p.push(evalEnvelope(z.Bytes(), true), "gzip", cfg, &budget, 4); !errors.Is(e, ErrLimit) {
		t.Fatal(e)
	}
	p = messageParser{}
	budget = 0
	p.push([]byte{0, 0, 0}, "identity", cfg, &budget, 4)
	if !errors.Is(p.finish(), ErrIncomplete) {
		t.Fatal("partial accepted")
	}
}
func TestEvaluatorFailClosedAndIsolation(t *testing.T) {
	tr, _ := New(Config{})
	f := evalFrame(h2demux.Write, 1, h2demux.Continuation, 0, nil)
	if _, e := tr.Push(f); !errors.Is(e, ErrProtocol) {
		t.Fatal(e)
	}
	f.Type = h2demux.Settings
	f.ID = 0
	if _, e := tr.Push(f); !errors.Is(e, ErrProtocol) {
		t.Fatal("failure not sticky", e)
	}
	f.Conn = 9
	evalPush(t, tr, f)
	tr.Forget(h2demux.Connection{PID: 7, Conn: 8})
	if e := tr.Finish(); e != nil {
		t.Fatal(e)
	}
}
func TestEvaluatorResetAndFallback(t *testing.T) {
	for _, status := range []string{"200", "401", "404", "503"} {
		tr, _ := New(Config{})
		var b bytes.Buffer
		e := hpack.NewEncoder(&b)
		evalPush(t, tr, evalFrame(h2demux.Write, 1, h2demux.Headers, 5, evalBlock(e, &b, ":method", "POST", ":path", "/s/M", "content-type", "application/grpc")))
		var r bytes.Buffer
		re := hpack.NewEncoder(&r)
		calls := evalPush(t, tr, evalFrame(h2demux.Read, 1, h2demux.Headers, 5, evalBlock(re, &r, ":status", status)))
		want := map[string]int{"200": 2, "401": 16, "404": 12, "503": 14}[status]
		if len(calls) != 1 || calls[0].Status != want {
			t.Fatal(calls)
		}
	}
	tr, _ := New(Config{})
	var b bytes.Buffer
	e := hpack.NewEncoder(&b)
	evalPush(t, tr, evalFrame(h2demux.Read, 1, h2demux.Headers, 4, evalBlock(e, &b, ":method", "POST", ":path", "/s/M", "content-type", "application/grpc")))
	calls := evalPush(t, tr, evalFrame(h2demux.Write, 1, h2demux.RSTStream, 0, []byte{0, 0, 0, 8}))
	if len(calls) != 1 || calls[0].Status != 1 || calls[0].ResetCode == nil {
		t.Fatal(calls)
	}
}
func FuzzEvaluatorMessageSegmentation(f *testing.F) {
	f.Add([]byte("abc"), uint8(2))
	f.Add([]byte{}, uint8(1))
	f.Fuzz(func(t *testing.T, data []byte, step uint8) {
		if len(data) > 4096 {
			return
		}
		cfg, _ := defaults(Config{MaxMessageBytes: 4096})
		wire := evalEnvelope(data, false)
		var p messageParser
		budget := 0
		var got []Message
		n := int(step) + 1
		for len(wire) > 0 {
			take := n
			if take > len(wire) {
				take = len(wire)
			}
			m, e := p.push(wire[:take], "identity", cfg, &budget, 1-len(got))
			if e != nil {
				t.Fatal(e)
			}
			got = append(got, m...)
			wire = wire[take:]
		}
		if p.finish() != nil || len(got) != 1 || !bytes.Equal(got[0].Data, data) {
			t.Fatal("segmentation changed message")
		}
	})
}

func TestEvaluatorPushPromisePreservesHPACK(t *testing.T) {
	tr, _ := New(Config{})
	var b bytes.Buffer
	e := hpack.NewEncoder(&b)
	block := evalBlock(e, &b, ":method", "GET", ":path", "/push/table-entry", "content-type", "application/grpc")
	promise := append([]byte{0, 0, 0, 2}, block...)
	evalPush(t, tr, evalFrame(h2demux.Write, 1, h2demux.PushPromise, h2demux.EndHeaders, promise))
	evalPush(t, tr, evalFrame(h2demux.Write, 3, h2demux.Headers, 5, evalBlock(e, &b, ":method", "POST", ":path", "/push/table-entry", "content-type", "application/grpc")))
	var r bytes.Buffer
	re := hpack.NewEncoder(&r)
	calls := evalPush(t, tr, evalFrame(h2demux.Read, 3, h2demux.Headers, 5, evalBlock(re, &r, ":status", "200", "grpc-status", "0")))
	if len(calls) != 1 || calls[0].Method != "/push/table-entry" {
		t.Fatal(calls)
	}
}
func TestEvaluatorSettingsAndIncomplete(t *testing.T) {
	tr, _ := New(Config{})
	evalPush(t, tr, evalFrame(h2demux.Read, 0, h2demux.Settings, 0, []byte{0, 1, 0, 0, 0, 0}))
	var b bytes.Buffer
	e := hpack.NewEncoder(&b)
	e.SetMaxDynamicTableSize(0)
	evalPush(t, tr, evalFrame(h2demux.Write, 1, h2demux.Headers, 4, evalBlock(e, &b, ":method", "POST", ":path", "/s/M", "content-type", "application/grpc")))
	if !errors.Is(tr.Finish(), ErrIncomplete) {
		t.Fatal("open call accepted")
	}
	tr, _ = New(Config{})
	evalPush(t, tr, evalFrame(h2demux.Write, 1, h2demux.Headers, 0, []byte{0x82}))
	if !errors.Is(tr.Finish(), ErrIncomplete) {
		t.Fatal("open block accepted")
	}
	tr, _ = New(Config{})
	evalPush(t, tr, evalFrame(h2demux.Read, 0, h2demux.Settings, 0, []byte{0, 1, 0, 0, 0, 0}))
	var x bytes.Buffer
	xe := hpack.NewEncoder(&x)
	xe.SetMaxDynamicTableSize(4097)
	if _, err := tr.Push(evalFrame(h2demux.Write, 1, h2demux.Headers, 4, evalBlock(xe, &x, ":method", "POST", ":path", "/s/M"))); !errors.Is(err, ErrProtocol) {
		t.Fatal("table growth accepted", err)
	}
}
func TestEvaluatorPayloadOwnershipAndTiming(t *testing.T) {
	tr, _ := New(Config{})
	var b bytes.Buffer
	e := hpack.NewEncoder(&b)
	header := evalBlock(e, &b, ":method", "POST", ":path", "/s/M", "content-type", "application/grpc")
	f := evalFrame(h2demux.Write, 1, h2demux.Headers, 4, header)
	f.FinishedNS = 20
	evalPush(t, tr, f)
	clear(header)
	data := evalEnvelope([]byte("owned"), false)
	evalPush(t, tr, evalFrame(h2demux.Write, 1, h2demux.Data, 1, data))
	clear(data)
	var r bytes.Buffer
	re := hpack.NewEncoder(&r)
	f = evalFrame(h2demux.Read, 1, h2demux.Headers, 5, evalBlock(re, &r, ":status", "200", "grpc-status", "0"))
	f.FinishedNS = 120
	calls := evalPush(t, tr, f)
	if len(calls) != 1 || calls[0].Method != "/s/M" || string(calls[0].Request[0].Data) != "owned" || calls[0].LatencyNS != 100 || !calls[0].TimingValid {
		t.Fatal(calls)
	}
}

func TestEvaluatorMalformedHeadersAndEOF(t *testing.T) {
	for _, fields := range [][]string{{":method", "POST", ":method", "POST"}, {"content-type", "application/grpc", ":method", "POST"}, {":method", "POST", ":path", "/s/M", "X-Test", "bad"}, {":method", "POST", ":status", "200"}, {":method", "GET", ":path", "/s/M", "content-type", "application/grpc"}} {
		tr, _ := New(Config{})
		var b bytes.Buffer
		e := hpack.NewEncoder(&b)
		if _, err := tr.Push(evalFrame(h2demux.Write, 1, h2demux.Headers, 4, evalBlock(e, &b, fields...))); !errors.Is(err, ErrProtocol) {
			t.Fatalf("accepted %v: %v", fields, err)
		}
	}
	tr, _ := New(Config{})
	var b bytes.Buffer
	e := hpack.NewEncoder(&b)
	evalPush(t, tr, evalFrame(h2demux.Write, 1, h2demux.Headers, 4, evalBlock(e, &b, ":method", "POST", ":path", "/s/M", "content-type", "application/grpc")))
	if _, err := tr.Push(evalFrame(h2demux.Write, 1, h2demux.Data, 1, []byte{0, 0})); !errors.Is(err, ErrIncomplete) {
		t.Fatal("partial payload at END_STREAM accepted", err)
	}
}

func TestEvaluatorDuplicateCriticalMetadata(t *testing.T) {
	for _, pair := range [][]string{{"content-type", "application/grpc", "content-type", "text/plain"}, {"grpc-encoding", "gzip", "grpc-encoding", "br"}} {
		tr, _ := New(Config{})
		var b bytes.Buffer
		e := hpack.NewEncoder(&b)
		fields := []string{":method", "POST", ":path", "/s/M"}
		fields = append(fields, pair...)
		if _, err := tr.Push(evalFrame(h2demux.Write, 1, h2demux.Headers, 4, evalBlock(e, &b, fields...))); !errors.Is(err, ErrProtocol) {
			t.Fatalf("ambiguous critical header accepted: %v (%v)", pair, err)
		}
	}
}

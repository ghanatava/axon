package h2demux

import (
	"bytes"
	"encoding/binary"
	"errors"
	"github.com/ghanatava/axon/internal/tlscapture"
	"math/rand"
	"reflect"
	"testing"
)

func evalFrame(typ Type, flags byte, id uint32, b []byte) []byte {
	h := make([]byte, 9)
	h[0] = byte(len(b) >> 16)
	h[1] = byte(len(b) >> 8)
	h[2] = byte(len(b))
	h[3] = byte(typ)
	h[4] = flags
	binary.BigEndian.PutUint32(h[5:], id)
	return append(h, b...)
}
func evalWire() []byte {
	b := append([]byte(ClientPreface), evalFrame(Settings, 0, 0, nil)...)
	for _, f := range [][]byte{evalFrame(Headers, 0, 1, []byte{1}), evalFrame(Continuation, EndHeaders, 1, []byte{2}), evalFrame(Headers, EndHeaders, 3, []byte{3}), evalFrame(Data, 0, 1, bytes.Repeat([]byte("a"), 10000)), evalFrame(Data, EndStream, 3, []byte("three")), evalFrame(Type(222), 0, 0x80000001, []byte("extension"))} {
		b = append(b, f...)
	}
	return b
}
func evalNew(t *testing.T, c Config) *Demux {
	t.Helper()
	d, e := New(c)
	if e != nil {
		t.Fatal(e)
	}
	return d
}
func TestEvaluatorSegmentationAndOwnership(t *testing.T) {
	wire := evalWire()
	key := Connection{1, 2}
	base := evalNew(t, Config{})
	want, e := base.Feed(key, Write, wire)
	if e != nil {
		t.Fatal(e)
	}
	if len(want) != 7 || want[6].ID != 1 {
		t.Fatal(want)
	}
	for seed := int64(0); seed < 40; seed++ {
		d := evalNew(t, Config{})
		r := rand.New(rand.NewSource(seed))
		var got []Frame
		for pos := 0; pos < len(wire); {
			n := 1
			if seed > 0 {
				n = 1 + r.Intn(2000)
			}
			if n > len(wire)-pos {
				n = len(wire) - pos
			}
			chunk := append([]byte(nil), wire[pos:pos+n]...)
			f, e := d.Feed(key, Write, chunk)
			if e != nil {
				t.Fatalf("seed %d: %v", seed, e)
			}
			got = append(got, f...)
			for i := range chunk {
				chunk[i] = 255
			}
			pos += n
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("seed %d differs", seed)
		}
		if e := d.Finish(); e != nil {
			t.Fatal(e)
		}
	}
}
func TestEvaluatorChunkCallsAndIdentity(t *testing.T) {
	d := evalNew(t, Config{})
	wire := evalWire()
	for _, pid := range []uint32{1, 2} {
		for _, direction := range []uint32{0, 1} {
			for pos := 0; pos < len(wire); {
				end := pos + 4096
				if end > len(wire) {
					end = len(wire)
				}
				f, e := d.Push(tlscapture.Event{PID: pid, Conn: 7, GoID: 8, Direction: direction, Started: 1, Total: uint64(len(wire)), Offset: uint64(pos), Chunked: true, Data: wire[pos:end]})
				if e != nil {
					t.Fatal(e)
				}
				if end == len(wire) && len(f) != 7 {
					t.Fatal(len(f))
				}
				pos = end
			}
			b := evalFrame(Data, EndStream, 1, []byte("next goroutine"))
			f, e := d.Push(tlscapture.Event{PID: pid, Conn: 7, GoID: 999, Direction: direction, Started: 2, Total: uint64(len(b)), Data: b})
			if e != nil || len(f) != 1 || f[0].Sequence != 8 {
				t.Fatal(f, e)
			}
		}
	}
	if e := d.Finish(); e != nil {
		t.Fatal(e)
	}
}
func TestEvaluatorChunkLoss(t *testing.T) {
	first := tlscapture.Event{PID: 1, Conn: 2, GoID: 3, Started: 4, Total: 20, Chunked: true, Data: []byte("12345")}
	for _, kind := range []string{"missing", "duplicate", "mixed goid", "mixed call", "total", "legacy", "oversize"} {
		t.Run(kind, func(t *testing.T) {
			d := evalNew(t, Config{})
			if _, e := d.Push(first); e != nil {
				t.Fatal(e)
			}
			next := first
			next.Offset = 5
			switch kind {
			case "missing":
				next.Offset = 10
			case "duplicate":
				next.Offset = 0
			case "mixed goid":
				next.GoID++
			case "mixed call":
				next.Started++
			case "total":
				next.Total++
			case "legacy":
				next.Chunked = false
			case "oversize":
				next.Total = MaxCallBytes + 1
			}
			if _, e := d.Push(next); !errors.Is(e, ErrGap) {
				t.Fatal(e)
			}
			if _, e := d.Feed(Connection{1, 2}, Read, evalFrame(Settings, 0, 0, nil)); !errors.Is(e, ErrGap) {
				t.Fatal("direction should stay failed", e)
			}
		})
	}
	d := evalNew(t, Config{})
	d.Push(first)
	if !errors.Is(d.Finish(), ErrIncomplete) {
		t.Fatal("missing final chunk accepted")
	}
}
func TestEvaluatorInvalidFramesAndEOF(t *testing.T) {
	for _, tc := range []struct {
		name string
		b    []byte
	}{{"data stream zero", evalFrame(Data, 0, 0, nil)}, {"padding", evalFrame(Data, Padded, 1, []byte{3, 1})}, {"priority padding", evalFrame(Headers, Padded|HasPriority|EndHeaders, 1, []byte{5, 0, 0, 0, 0, 0})}, {"continuation", evalFrame(Continuation, EndHeaders, 1, nil)}, {"ping", evalFrame(Ping, 0, 0, []byte{1})}, {"settings ack", evalFrame(Settings, ACK, 0, make([]byte, 6))}, {"window zero", evalFrame(WindowUpdate, 0, 1, make([]byte, 4))}, {"interrupted headers", append(evalFrame(Headers, 0, 1, nil), evalFrame(Data, 0, 3, nil)...)}, {"wrong continuation", append(evalFrame(Headers, 0, 1, nil), evalFrame(Continuation, EndHeaders, 3, nil)...)}} {
		t.Run(tc.name, func(t *testing.T) {
			d := evalNew(t, Config{})
			_, e := d.Feed(Connection{1, 1}, Read, append(evalFrame(Settings, 0, 0, nil), tc.b...))
			if !errors.Is(e, ErrProtocol) {
				t.Fatal(e)
			}
		})
	}
	for _, b := range [][]byte{[]byte("PRI"), []byte(ClientPreface), append(evalFrame(Settings, 0, 0, nil), 1), append(evalFrame(Settings, 0, 0, nil), evalFrame(Data, 0, 1, []byte("abc"))[:10]...), append(evalFrame(Settings, 0, 0, nil), evalFrame(Headers, 0, 1, nil)...)} {
		d := evalNew(t, Config{})
		if _, e := d.Feed(Connection{1, 1}, Read, b); e != nil {
			t.Fatal(e)
		}
		if !errors.Is(d.Finish(), ErrIncomplete) {
			t.Fatalf("accepted incomplete %x", b)
		}
	}
}
func TestEvaluatorBounds(t *testing.T) {
	d := evalNew(t, Config{MaxConnections: 1, MaxFrameSize: 16})
	key := Connection{1, 1}
	d.Feed(key, Read, evalFrame(Settings, 0, 0, nil))
	if _, e := d.Feed(Connection{2, 1}, Read, nil); !errors.Is(e, ErrLimit) {
		t.Fatal(e)
	}
	if _, e := d.Feed(key, Read, evalFrame(Data, 0, 1, make([]byte, 17))[:9]); !errors.Is(e, ErrLimit) {
		t.Fatal(e)
	}
	d.Forget(key)
	if _, e := d.Feed(Connection{2, 1}, Read, evalFrame(Settings, 0, 0, nil)); e != nil {
		t.Fatal(e)
	}
}
func FuzzEvaluatorSegmentation(f *testing.F) {
	f.Add([]byte{1, 2, 3})
	f.Fuzz(func(t *testing.T, sizes []byte) {
		wire := evalWire()
		d := evalNew(t, Config{})
		var got []Frame
		for i, pos := 0, 0; pos < len(wire); i++ {
			n := 1
			if len(sizes) > 0 {
				n += int(sizes[i%len(sizes)])
			}
			if n > len(wire)-pos {
				n = len(wire) - pos
			}
			out, e := d.Feed(Connection{1, 1}, Write, wire[pos:pos+n])
			if e != nil {
				t.Fatal(e)
			}
			got = append(got, out...)
			pos += n
		}
		if len(got) != 7 || d.Finish() != nil {
			t.Fatal("lost frames")
		}
	})
}

func TestEvaluatorCompletedCallDuplicate(t *testing.T) {
	d := evalNew(t, Config{})
	e := tlscapture.Event{PID: 1, Conn: 2, GoID: 3, Started: 4, Total: 9, Chunked: true, Data: evalFrame(Settings, 0, 0, nil)}
	if f, err := d.Push(e); err != nil || len(f) != 1 {
		t.Fatal(f, err)
	}
	if _, err := d.Push(e); !errors.Is(err, ErrGap) {
		t.Fatal("accepted duplicate singleton", err)
	}
	if _, err := d.Push(tlscapture.Event{PID: 1, Conn: 2, GoID: 3, Started: 5, Total: 9, Chunked: true, Data: evalFrame(Settings, 0, 0, nil)}); !errors.Is(err, ErrGap) {
		t.Fatal("did not fail persistently", err)
	}
}
func TestEvaluatorOversizedChunk(t *testing.T) {
	d := evalNew(t, Config{})
	e := tlscapture.Event{PID: 1, Conn: 2, GoID: 3, Started: 4, Total: 4097, Chunked: true, Data: make([]byte, 4097)}
	if _, err := d.Push(e); !errors.Is(err, ErrGap) {
		t.Fatal("oversized chunk accepted", err)
	}
}
func TestEvaluatorAmbiguousInitialSettings(t *testing.T) {
	// High byte of the 24-bit length is ASCII P, exactly like the client magic.
	// Unknown SETTINGS identifiers are permitted and ignored; all-zero settings
	// form a valid payload of six-byte entries under the configured full range.
	wire := evalFrame(Settings, 0, 0, make([]byte, 0x500004))
	for _, split := range []int{1, 2, 9, len(wire)} {
		d := evalNew(t, Config{})
		var frames []Frame
		for pos := 0; pos < len(wire); {
			n := split
			if n > len(wire)-pos {
				n = len(wire) - pos
			}
			f, err := d.Feed(Connection{1, 1}, Read, wire[pos:pos+n])
			if err != nil {
				t.Fatalf("split %d: %v", split, err)
			}
			frames = append(frames, f...)
			pos += n
		}
		if len(frames) != 1 || len(frames[0].Payload) != 0x500004 || frames[0].Type != Settings {
			t.Fatalf("split %d lost frame", split)
		}
		if err := d.Finish(); err != nil {
			t.Fatal(err)
		}
	}
}

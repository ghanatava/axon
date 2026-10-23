package tlscapture

import (
	"encoding/binary"
	"testing"
)

func TestLifecycleBinaryABI(t *testing.T) {
	raw := make([]byte, LifecycleHeaderSize+MaxData)
	put := func(offset int, value uint64) { binary.LittleEndian.PutUint64(raw[offset:], value) }
	put(0, 10)
	put(8, 20)
	put(16, 30)
	put(24, 40)
	put(32, 3)
	binary.LittleEndian.PutUint32(raw[40:], 50)
	binary.LittleEndian.PutUint32(raw[44:], 60)
	binary.LittleEndian.PutUint32(raw[48:], 1)
	binary.LittleEndian.PutUint32(raw[52:], 3)
	put(64, 70)
	put(72, 80)
	put(80, 90)
	copy(raw[96:], "abc")
	e, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if e.Version != 3 || e.ProcessStart != 70 || e.Generation != 80 || e.Sequence != 90 || e.Kind != Data || string(e.Data) != "abc" {
		t.Fatal(e)
	}
	put(32, 0)
	put(80, 0)
	binary.LittleEndian.PutUint32(raw[52:], 0)
	binary.LittleEndian.PutUint32(raw[88:], 1)
	if e, err := Decode(raw); err != nil || e.Kind != Close || len(e.Data) != 0 {
		t.Fatal(e, err)
	}
	binary.LittleEndian.PutUint32(raw[88:], 3)
	if _, err := Decode(raw); err == nil {
		t.Fatal("invalid kind accepted")
	}
}

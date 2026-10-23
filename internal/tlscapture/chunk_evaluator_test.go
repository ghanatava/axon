package tlscapture

import (
	"encoding/binary"
	"testing"
)

func TestEvaluatorChunkDecode(t *testing.T) {
	b := make([]byte, ChunkHeaderSize+MaxData)
	put := func(i int, v uint64) { binary.LittleEndian.PutUint64(b[i:], v) }
	put(0, 11)
	put(8, 12)
	put(16, 13)
	put(24, 14)
	put(32, 9000)
	binary.LittleEndian.PutUint32(b[40:], 15)
	binary.LittleEndian.PutUint32(b[44:], 16)
	binary.LittleEndian.PutUint32(b[48:], 1)
	binary.LittleEndian.PutUint32(b[52:], 3)
	put(56, 4096)
	copy(b[64:], "abc")
	e, err := Decode(b)
	if err != nil || !e.Chunked || e.Offset != 4096 || string(e.Data) != "abc" || e.Started != 11 || e.PID != 15 || e.Total != 9000 {
		t.Fatal(e, err)
	}
	b[64] = 'x'
	if string(e.Data) != "abc" {
		t.Fatal("borrowed event memory")
	}
	for _, offset := range []uint64{8998, 9001, ^uint64(0)} {
		put(56, offset)
		if _, e := Decode(b); e == nil {
			t.Fatal("accepted invalid offset", offset)
		}
	}
	if _, e := Decode(b[:len(b)-1]); e == nil {
		t.Fatal("accepted short record")
	}
}

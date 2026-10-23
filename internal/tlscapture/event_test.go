package tlscapture

import (
	"encoding/binary"
	"testing"
)

func TestDecode(t *testing.T) {
	raw := make([]byte, HeaderSize+MaxData)
	binary.LittleEndian.PutUint64(raw[16:], 37)
	binary.LittleEndian.PutUint64(raw[24:], 0x1234)
	binary.LittleEndian.PutUint64(raw[32:], 12)
	binary.LittleEndian.PutUint32(raw[40:], 100)
	binary.LittleEndian.PutUint32(raw[48:], 1)
	binary.LittleEndian.PutUint32(raw[52:], 3)
	copy(raw[56:], "abc")
	e, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if e.GoID != 37 || e.Conn != 0x1234 || e.PID != 100 || e.Direction != 1 || string(e.Data) != "abc" || e.Total != 12 {
		t.Fatalf("bad event: %+v", e)
	}
	raw[56] = 'z'
	if string(e.Data) != "abc" {
		t.Fatal("data aliases ring buffer")
	}
	for _, n := range []uint32{MaxData + 1, 13} {
		binary.LittleEndian.PutUint32(raw[52:], n)
		if _, err := Decode(raw); err == nil {
			t.Fatalf("accepted invalid length %d", n)
		}
	}
	if _, err := Decode(raw[:56]); err == nil {
		t.Fatal("accepted short event")
	}
}

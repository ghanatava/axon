package tlscapture

import (
	"encoding/binary"
	"fmt"
)

const MaxData = 4096
const HeaderSize = 56 // Legacy Phase 2 event header.
const ChunkHeaderSize = 64
const LifecycleHeaderSize = 96
const LifecycleVersion = 3

type Kind uint32

const (
	Data Kind = iota
	Close
	Loss
)

type Event struct {
	Started, Finished, GoID, Conn, Total uint64
	PID, TID, Direction                  uint32
	Data                                 []byte
	Offset                               uint64
	Chunked                              bool
	ProcessStart, Generation, Sequence   uint64
	Kind                                 Kind
	Failure                              uint32
	Version                              uint32
}

func Decode(raw []byte) (Event, error) {
	var e Event
	if len(raw) != HeaderSize+MaxData && len(raw) != ChunkHeaderSize+MaxData && len(raw) != LifecycleHeaderSize+MaxData {
		return e, fmt.Errorf("event size %d, want %d (legacy), %d (chunked), or %d (lifecycle)", len(raw), HeaderSize+MaxData, ChunkHeaderSize+MaxData, LifecycleHeaderSize+MaxData)
	}
	u64 := func(i int) uint64 { return binary.LittleEndian.Uint64(raw[i:]) }
	u32 := func(i int) uint32 { return binary.LittleEndian.Uint32(raw[i:]) }
	e = Event{Started: u64(0), Finished: u64(8), GoID: u64(16), Conn: u64(24), Total: u64(32), PID: u32(40), TID: u32(44), Direction: u32(48)}
	n := u32(52)
	if n > MaxData || uint64(n) > e.Total || e.Direction > 1 {
		return Event{}, fmt.Errorf("invalid TLS event header")
	}
	header := HeaderSize
	if len(raw) == ChunkHeaderSize+MaxData || len(raw) == LifecycleHeaderSize+MaxData {
		header = ChunkHeaderSize
		e.Offset = u64(56)
		e.Chunked = true
		if e.Offset > e.Total || uint64(n) > e.Total-e.Offset {
			return Event{}, fmt.Errorf("TLS chunk outside call bounds")
		}
	}
	if len(raw) == LifecycleHeaderSize+MaxData {
		header = LifecycleHeaderSize
		e.Version = LifecycleVersion
		e.ProcessStart = u64(64)
		e.Generation = u64(72)
		e.Sequence = u64(80)
		e.Kind = Kind(u32(88))
		e.Failure = u32(92)
		if e.Kind > Loss || e.ProcessStart == 0 || e.Generation == 0 || (e.Kind == Data && (e.Sequence == 0 || n == 0)) || (e.Kind != Data && (n != 0 || e.Total != 0)) {
			return Event{}, fmt.Errorf("invalid lifecycle event")
		}
	}
	e.Data = append([]byte(nil), raw[header:header+int(n)]...)
	return e, nil
}

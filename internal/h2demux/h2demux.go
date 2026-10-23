// Package h2demux reassembles ordered TLS plaintext into HTTP/2 frames.
// It preserves wire order and labels each frame with its connection and stream;
// HPACK and gRPC message interpretation belong to the next phase.
package h2demux

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const ClientPreface = "PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n"
const MaxFrameSize = 1<<24 - 1

type Direction uint32

const (
	Read Direction = iota
	Write
)

func (d Direction) String() string {
	if d == Read {
		return "read"
	}
	if d == Write {
		return "write"
	}
	return "invalid"
}

type Type uint8

const (
	Data Type = iota
	Headers
	Priority
	RSTStream
	Settings
	PushPromise
	Ping
	GoAway
	WindowUpdate
	Continuation
)

func (t Type) String() string {
	names := [...]string{"DATA", "HEADERS", "PRIORITY", "RST_STREAM", "SETTINGS", "PUSH_PROMISE", "PING", "GOAWAY", "WINDOW_UPDATE", "CONTINUATION"}
	if int(t) < len(names) {
		return names[t]
	}
	return fmt.Sprintf("UNKNOWN_%d", t)
}

const (
	EndStream   uint8 = 0x1
	ACK         uint8 = 0x1
	EndHeaders  uint8 = 0x4
	Padded      uint8 = 0x8
	HasPriority uint8 = 0x20
)

// Connection uses the TLS receiver pointer, not the calling goroutine: several
// goroutines can share a connection and one goroutine can use many connections.
type Connection struct {
	PID  uint32
	Conn uint64
}
type Stream struct {
	Connection
	ID uint32
}
type Frame struct {
	Stream
	Direction             Direction
	Sequence              uint64 // Wire position within this connection direction, including stream 0.
	Type                  Type
	Flags                 uint8
	Payload               []byte // Owned, raw frame payload; includes any padding/priority prefix.
	StartedNS, FinishedNS uint64 // Capture clock bounds of the frame bytes; zero when unavailable.
}

var (
	ErrProtocol   = errors.New("invalid HTTP/2 framing")
	ErrGap        = errors.New("TLS capture gap")
	ErrLimit      = errors.New("HTTP/2 resource limit")
	ErrIncomplete = errors.New("incomplete HTTP/2 capture")
)

type Config struct {
	MaxFrameSize   uint32 // 0 accepts the full 24-bit HTTP/2 length range.
	MaxConnections int    // 0 defaults to 1024. Explicit Forget is available for lifecycle cleanup.
}
type Demux struct {
	config      Config
	connections map[Connection]*connection
}
type connection struct{ directions [2]parser }
type parser struct {
	sniff                       []byte
	identified                  bool
	first                       bool
	pending                     []byte
	expected                    int
	headerStream                uint32
	sequence                    uint64
	failure                     error
	call                        *call
	completed                   *callIdentity
	frameStarted, frameFinished uint64
}

func New(config Config) (*Demux, error) {
	if config.MaxFrameSize == 0 {
		config.MaxFrameSize = MaxFrameSize
	}
	if config.MaxFrameSize > MaxFrameSize || config.MaxConnections < 0 {
		return nil, fmt.Errorf("%w: invalid configuration", ErrLimit)
	}
	if config.MaxConnections == 0 {
		config.MaxConnections = 1024
	}
	return &Demux{config: config, connections: make(map[Connection]*connection)}, nil
}
func (d *Demux) direction(key Connection, direction Direction) (*parser, error) {
	if direction > Write || key.PID == 0 || key.Conn == 0 {
		return nil, fmt.Errorf("%w: invalid connection or direction", ErrProtocol)
	}
	c := d.connections[key]
	if c == nil {
		if len(d.connections) >= d.config.MaxConnections {
			return nil, fmt.Errorf("%w: connection count", ErrLimit)
		}
		c = &connection{}
		d.connections[key] = c
	}
	return &c.directions[direction], nil
}

// Feed accepts contiguous, lossless bytes in wire order. It handles a client
// preface or server SETTINGS at the beginning, including arbitrary fragmentation.
// Starting capture in the middle of an established connection is unsupported.
// If input contains valid frames followed by a malformed frame, valid frames
// are returned alongside the error. That direction remains failed until Forget.
func (d *Demux) Feed(key Connection, direction Direction, data []byte) ([]Frame, error) {
	return d.FeedAt(key, direction, data, 0, 0)
}

// FeedAt is Feed with the capture-clock bounds of this contiguous byte chunk.
func (d *Demux) FeedAt(key Connection, direction Direction, data []byte, started, finished uint64) ([]Frame, error) {
	p, err := d.direction(key, direction)
	if err != nil {
		return nil, err
	}
	if p.failure != nil {
		return nil, p.failure
	}
	frames, err := p.feed(data, d.config.MaxFrameSize, started, finished)
	for i := range frames {
		frames[i].Stream.Connection = key
		frames[i].Direction = direction
	}
	if err != nil {
		p.fail(err)
	}
	return frames, err
}
func (p *parser) fail(err error)       { p.failure = err; p.pending = nil; p.sniff = nil; p.call = nil }
func (d *Demux) Forget(key Connection) { delete(d.connections, key) }

// Finish reports partial prefaces, frames, header blocks and TLS calls. Complete
// open application streams are allowed: stream lifecycle semantics are Phase 4.
func (d *Demux) Finish() error {
	for key, c := range d.connections {
		for direction := range c.directions {
			p := &c.directions[direction]
			if p.failure != nil {
				return fmt.Errorf("pid=%d conn=%#x %s: %w", key.PID, key.Conn, Direction(direction), p.failure)
			}
			if len(p.sniff) > 0 || len(p.pending) > 0 || p.headerStream != 0 || p.call != nil || p.identified && !p.first {
				return fmt.Errorf("%w: pid=%d conn=%#x %s", ErrIncomplete, key.PID, key.Conn, Direction(direction))
			}
		}
	}
	return nil
}
func (p *parser) feed(data []byte, max uint32, started, finished uint64) ([]Frame, error) {
	var out []Frame
	for len(data) > 0 {
		if !p.identified {
			// Sniff the optional client magic without confusing a length byte
			// with the first preface byte. Both directions begin with SETTINGS.
			if len(p.sniff) == 0 && data[0] != ClientPreface[0] {
				p.identified = true
				continue
			}
			n := len(p.sniff)
			if data[0] != ClientPreface[n] {
				// A frame length can also start with 'P'. Once the magic
				// diverges, reinterpret only the saved prefix as header bytes.
				prefix := p.sniff
				p.sniff = nil
				p.identified = true
				frames, err := p.feed(prefix, max, started, finished)
				out = append(out, frames...)
				if err != nil {
					return out, err
				}
				continue
			}
			p.sniff = append(p.sniff, data[0])
			data = data[1:]
			if len(p.sniff) == len(ClientPreface) {
				p.sniff = nil
				p.identified = true
			}
			continue
		}
		target := 9
		if p.expected != 0 {
			target = p.expected
		}
		take := target - len(p.pending)
		if take > len(data) {
			take = len(data)
		}
		if len(p.pending) == 0 {
			p.frameStarted = started
		}
		if take > 0 {
			p.frameFinished = finished
		}
		p.pending = append(p.pending, data[:take]...)
		data = data[take:]
		if len(p.pending) < target {
			continue
		}
		if p.expected == 0 {
			h := p.pending
			size := uint32(h[0])<<16 | uint32(h[1])<<8 | uint32(h[2])
			if size > max {
				return out, fmt.Errorf("%w: frame length %d exceeds %d", ErrLimit, size, max)
			}
			typ := Type(h[3])
			flags := h[4]
			id := binary.BigEndian.Uint32(h[5:9]) & 0x7fffffff
			if !p.first && (typ != Settings || flags&ACK != 0) {
				return out, fmt.Errorf("%w: first frame must be non-ACK SETTINGS", ErrProtocol)
			}
			if p.headerStream != 0 && (typ != Continuation || id != p.headerStream) {
				return out, fmt.Errorf("%w: interrupted header block", ErrProtocol)
			}
			if p.headerStream == 0 && typ == Continuation {
				return out, fmt.Errorf("%w: unexpected CONTINUATION", ErrProtocol)
			}
			if err := validateHeader(typ, flags, id, size); err != nil {
				return out, err
			}
			p.expected = 9 + int(size)
			if len(p.pending) < p.expected {
				continue
			}
		}
		h := p.pending
		typ := Type(h[3])
		flags := h[4]
		id := binary.BigEndian.Uint32(h[5:9]) & 0x7fffffff
		payload := h[9:]
		if err := validatePayload(typ, flags, id, payload); err != nil {
			return out, err
		}
		p.first = true
		if (typ == Headers || typ == PushPromise) && flags&EndHeaders == 0 {
			p.headerStream = id
		}
		if typ == Continuation && flags&EndHeaders != 0 {
			p.headerStream = 0
		}
		p.sequence++
		out = append(out, Frame{Stream: Stream{ID: id}, Sequence: p.sequence, Type: typ, Flags: flags, Payload: append([]byte(nil), payload...), StartedNS: p.frameStarted, FinishedNS: p.frameFinished})
		// Do not retain a large frame buffer for the life of an idle connection.
		p.pending = nil
		p.expected = 0
	}
	return out, nil
}
func protocol(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrProtocol, fmt.Sprintf(format, args...))
}
func validateHeader(typ Type, flags uint8, id, size uint32) error {
	switch typ {
	case Data, Headers, Priority, RSTStream, PushPromise, Continuation:
		if id == 0 {
			return protocol("%s requires a stream", typ)
		}
	case Settings, Ping, GoAway:
		if id != 0 {
			return protocol("%s requires stream 0", typ)
		}
	}
	switch typ {
	case Priority:
		if size != 5 {
			return protocol("PRIORITY length")
		}
	case RSTStream, WindowUpdate:
		if size != 4 {
			return protocol("%s length", typ)
		}
	case Settings:
		if size%6 != 0 || flags&ACK != 0 && size != 0 {
			return protocol("SETTINGS length")
		}
	case Ping:
		if size != 8 {
			return protocol("PING length")
		}
	case GoAway:
		if size < 8 {
			return protocol("GOAWAY length")
		}
	}
	minimum := uint32(0)
	if (typ == Data || typ == Headers || typ == PushPromise) && flags&Padded != 0 {
		minimum++
	}
	if typ == Headers && flags&HasPriority != 0 {
		minimum += 5
	}
	if typ == PushPromise {
		minimum += 4
	}
	if size < minimum {
		return protocol("%s payload too short", typ)
	}
	return nil
}
func validatePayload(typ Type, flags uint8, id uint32, data []byte) error {
	body := data
	if (typ == Data || typ == Headers || typ == PushPromise) && flags&Padded != 0 {
		padding := int(body[0])
		body = body[1:]
		if padding > len(body) {
			return protocol("padding exceeds payload")
		}
		body = body[:len(body)-padding]
	}
	if typ == Priority || typ == Headers && flags&HasPriority != 0 {
		if len(body) < 5 {
			return protocol("priority prefix too short")
		}
		if binary.BigEndian.Uint32(body)&0x7fffffff == id {
			return protocol("stream depends on itself")
		}
	}
	if typ == PushPromise && (len(body) < 4 || binary.BigEndian.Uint32(body)&0x7fffffff == 0) {
		return protocol("invalid promised stream")
	}
	if typ == WindowUpdate && binary.BigEndian.Uint32(body)&0x7fffffff == 0 {
		return protocol("zero window increment")
	}
	if typ == Settings {
		for len(body) > 0 {
			setting := binary.BigEndian.Uint16(body)
			value := binary.BigEndian.Uint32(body[2:])
			body = body[6:]
			switch setting {
			case 2:
				if value > 1 {
					return protocol("invalid ENABLE_PUSH")
				}
			case 4:
				if value > 0x7fffffff {
					return protocol("invalid INITIAL_WINDOW_SIZE")
				}
			case 5:
				if value < 16384 || value > MaxFrameSize {
					return protocol("invalid MAX_FRAME_SIZE")
				}
			}
		}
	}
	return nil
}

// RetainedBytes estimates buffered payload capacity (not the Go heap/RSS).
func (d *Demux) RetainedBytes() int {
	n := 0
	for _, c := range d.connections {
		for i := range c.directions {
			p := &c.directions[i]
			n += cap(p.pending) + cap(p.sniff)
			if p.call != nil {
				n += cap(p.call.data)
			}
		}
	}
	return n
}

// Package grpc reconstructs gRPC calls from an ordered, lossless HTTP/2 capture.
// HPACK tables belong to connection directions; messages and calls to streams.
package grpc

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/ghanatava/axon/internal/h2demux"
	"golang.org/x/net/http2/hpack"
)

var (
	ErrProtocol   = errors.New("invalid gRPC/HPACK capture")
	ErrLimit      = errors.New("gRPC resource limit")
	ErrIncomplete = errors.New("incomplete gRPC capture")
)

func protocol(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrProtocol, fmt.Sprintf(format, args...))
}

type Config struct {
	MaxConnections       int
	MaxStreams           int // Maximum concurrently retained streams per connection.
	MaxHeaderBytes       int // Compressed header block and expanded header list, separately.
	MaxDynamicTableBytes uint32
	MaxMessageBytes      int // Both wire and decompressed size of each message.
	MaxMessages          int // Per call across both directions.
	MaxCallBytes         int // Total retained decoded payload bytes per call.
}

func defaults(c Config) (Config, error) {
	if c.MaxConnections == 0 {
		c.MaxConnections = 1024
	}
	if c.MaxStreams == 0 {
		c.MaxStreams = 4096
	}
	if c.MaxHeaderBytes == 0 {
		c.MaxHeaderBytes = 64 << 10
	}
	if c.MaxDynamicTableBytes == 0 {
		c.MaxDynamicTableBytes = 64 << 10
	}
	if c.MaxMessageBytes == 0 {
		c.MaxMessageBytes = 4 << 20
	}
	if c.MaxMessages == 0 {
		c.MaxMessages = 1024
	}
	if c.MaxCallBytes == 0 {
		c.MaxCallBytes = 8 << 20
	}
	if c.MaxConnections < 1 || c.MaxStreams < 1 || c.MaxHeaderBytes < 1 || c.MaxDynamicTableBytes < 4096 || c.MaxMessageBytes < 1 || c.MaxMessages < 1 || c.MaxCallBytes < 1 {
		return c, fmt.Errorf("%w: invalid config", ErrLimit)
	}
	return c, nil
}

type Headers map[string][]string

func (h Headers) first(name string) string {
	if v := h[name]; len(v) > 0 {
		return v[0]
	}
	return ""
}

type Call struct {
	PID               uint32    `json:"pid"`
	ProcessStart      uint64    `json:"process_start,omitempty"`
	Generation        uint64    `json:"generation,omitempty"`
	Conn              uint64    `json:"conn"`
	StreamID          uint32    `json:"stream_id"`
	Method            string    `json:"method"`
	RequestDirection  string    `json:"request_direction"`
	RequestHeaders    Headers   `json:"request_headers"`
	ResponseHeaders   Headers   `json:"response_headers"`
	Trailers          Headers   `json:"trailers"`
	Request           []Message `json:"request"`
	Response          []Message `json:"response"`
	RequestBytes      uint64    `json:"request_bytes"`
	ResponseBytes     uint64    `json:"response_bytes"`
	RequestWireBytes  uint64    `json:"request_wire_bytes"`
	ResponseWireBytes uint64    `json:"response_wire_bytes"`
	Status            int       `json:"status"`
	StatusSource      string    `json:"status_source"`
	StatusMessage     string    `json:"status_message"`
	HTTPStatus        int       `json:"http_status"`
	ResetCode         *uint32   `json:"reset_code,omitempty"`
	StartedNS         uint64    `json:"started_ns"`
	FinishedNS        uint64    `json:"finished_ns"`
	LatencyNS         uint64    `json:"latency_ns"`
	TimingValid       bool      `json:"timing_valid"`
}
type Tracker struct {
	config      Config
	connections map[h2demux.Connection]*connection
}
type connection struct {
	directions       [2]direction
	streams          map[uint32]*stream
	requestDirection *h2demux.Direction
	failure          error
	highestRequest   uint32
}
type direction struct {
	decoder  *hpack.Decoder
	block    *headerBlock
	sequence uint64
}
type headerBlock struct {
	stream    uint32
	data      []byte
	end       bool
	skip      bool
	timestamp uint64
}
type side struct {
	headers           Headers
	trailers          Headers
	initial           bool
	ended             bool
	isGRPC            bool
	encoding          string
	messages          []Message
	parser            messageParser
	started, finished uint64
}
type stream struct {
	sides     [2]side
	bytes     int
	completed bool
}

func New(config Config) (*Tracker, error) {
	c, err := defaults(config)
	if err != nil {
		return nil, err
	}
	return &Tracker{config: c, connections: make(map[h2demux.Connection]*connection)}, nil
}
func (t *Tracker) get(key h2demux.Connection) (*connection, error) {
	if key.PID == 0 || key.Conn == 0 {
		return nil, protocol("missing connection identity")
	}
	c := t.connections[key]
	if c != nil {
		return c, nil
	}
	if len(t.connections) >= t.config.MaxConnections {
		return nil, fmt.Errorf("%w: connections", ErrLimit)
	}
	c = &connection{streams: make(map[uint32]*stream)}
	for i := range c.directions {
		d := hpack.NewDecoder(4096, func(hpack.HeaderField) {})
		d.SetMaxStringLength(t.config.MaxHeaderBytes)
		c.directions[i].decoder = d
	}
	t.connections[key] = c
	return c, nil
}
func (t *Tracker) Forget(key h2demux.Connection) { delete(t.connections, key) }

// Push consumes frames in their original per-direction order. Sequence=0 is
// allowed for direct callers; nonzero sequences must be consecutive. Timestamps
// are observation times, not application execution or network round-trip times.
func (t *Tracker) Push(f h2demux.Frame) ([]Call, error) {
	if f.Direction > h2demux.Write {
		return nil, protocol("invalid direction")
	}
	c, err := t.get(f.Connection)
	if err != nil {
		return nil, err
	}
	if c.failure != nil {
		return nil, c.failure
	}
	calls, err := t.push(c, f)
	if err != nil {
		c.failure = err
	}
	return calls, err
}
func (t *Tracker) getStream(c *connection, id uint32) (*stream, error) {
	if id == 0 {
		return nil, protocol("stream 0 has no call")
	}
	s := c.streams[id]
	if s != nil {
		return s, nil
	}
	if c.requestDirection != nil && id <= c.highestRequest {
		return nil, protocol("frame on closed or skipped stream %d", id)
	}
	if len(c.streams) >= t.config.MaxStreams {
		return nil, fmt.Errorf("%w: streams", ErrLimit)
	}
	s = &stream{}
	c.streams[id] = s
	return s, nil
}
func (t *Tracker) push(c *connection, f h2demux.Frame) ([]Call, error) {
	d := &c.directions[f.Direction]
	if f.Sequence != 0 {
		if f.Sequence != d.sequence+1 {
			return nil, protocol("frame sequence gap")
		}
		d.sequence = f.Sequence
	}
	if d.block != nil && (f.Type != h2demux.Continuation || f.ID != d.block.stream) {
		return nil, protocol("interrupted HPACK header block")
	}
	switch f.Type {
	case h2demux.Settings:
		if f.ID != 0 || len(f.Payload)%6 != 0 || f.Flags&h2demux.ACK != 0 && len(f.Payload) != 0 {
			return nil, protocol("invalid SETTINGS")
		}
		for p := f.Payload; len(p) > 0; p = p[6:] {
			if binary.BigEndian.Uint16(p) == 1 {
				limit := binary.BigEndian.Uint32(p[2:])
				if limit > t.config.MaxDynamicTableBytes {
					limit = t.config.MaxDynamicTableBytes
				}
				c.directions[1-f.Direction].decoder.SetAllowedMaxDynamicTableSize(limit)
			}
		}
		return nil, nil
	case h2demux.Headers, h2demux.PushPromise:
		if f.ID == 0 {
			return nil, protocol("header block on stream 0")
		}
		payload, err := unpad(f.Payload, f.Flags)
		if err != nil {
			return nil, err
		}
		if f.Type == h2demux.Headers && f.Flags&h2demux.HasPriority != 0 {
			if len(payload) < 5 {
				return nil, protocol("short HEADERS priority")
			}
			payload = payload[5:]
		}
		if f.Type == h2demux.PushPromise {
			if len(payload) < 4 {
				return nil, protocol("short PUSH_PROMISE")
			}
			payload = payload[4:]
		}
		d.block = &headerBlock{stream: f.ID, end: f.Flags&h2demux.EndStream != 0, skip: f.Type == h2demux.PushPromise, timestamp: f.FinishedNS}
		return t.headerFragment(c, f, payload)
	case h2demux.Continuation:
		if d.block == nil {
			return nil, protocol("unexpected CONTINUATION")
		}
		return t.headerFragment(c, f, f.Payload)
	case h2demux.Data:
		s, err := t.getStream(c, f.ID)
		if err != nil {
			return nil, err
		}
		if s.completed {
			return nil, protocol("DATA after completed call")
		}
		b := &s.sides[f.Direction]
		if !b.initial || b.ended {
			return nil, protocol("DATA without open headers")
		}
		payload, err := unpad(f.Payload, f.Flags)
		if err != nil {
			return nil, err
		}
		if b.isGRPC {
			messages, err := b.parser.push(payload, b.encoding, t.config, &s.bytes, t.config.MaxMessages-len(s.sides[0].messages)-len(s.sides[1].messages))
			if err != nil {
				return nil, err
			}
			b.messages = append(b.messages, messages...)
			if len(s.sides[0].messages)+len(s.sides[1].messages) > t.config.MaxMessages {
				return nil, fmt.Errorf("%w: message count", ErrLimit)
			}
		}
		if f.Flags&h2demux.EndStream != 0 {
			if b.isGRPC {
				if err := b.parser.finish(); err != nil {
					return nil, err
				}
			}
			b.ended = true
			b.finished = f.FinishedNS
		}
		return t.complete(c, f.Connection, f.ID, s)
	case h2demux.RSTStream:
		if f.ID == 0 || len(f.Payload) != 4 {
			return nil, protocol("invalid RST_STREAM")
		}
		s := c.streams[f.ID]
		if s == nil || s.completed {
			return nil, nil
		}
		if c.requestDirection == nil {
			return nil, fmt.Errorf("%w: reset without request direction", ErrIncomplete)
		}
		req := &s.sides[*c.requestDirection]
		if !req.isGRPC {
			return nil, nil
		}
		code := binary.BigEndian.Uint32(f.Payload)
		status := 13
		switch code {
		case 7:
			status = 14
		case 8:
			status = 1
		case 11:
			status = 8
		case 12:
			status = 7
		}
		req.ended = true
		resp := &s.sides[1-*c.requestDirection]
		resp.ended = true
		resp.finished = f.FinishedNS
		record := t.record(f.Connection, f.ID, s, *c.requestDirection, status, "rst-stream", "")
		record.ResetCode = &code
		t.release(c, f.ID, s)
		return []Call{record}, nil
	}
	return nil, nil
}
func unpad(payload []byte, flags uint8) ([]byte, error) {
	if flags&h2demux.Padded == 0 {
		return payload, nil
	}
	if len(payload) == 0 || int(payload[0]) > len(payload)-1 {
		return nil, protocol("invalid padding")
	}
	return payload[1 : len(payload)-int(payload[0])], nil
}
func (t *Tracker) complete(c *connection, key h2demux.Connection, id uint32, s *stream) ([]Call, error) {
	if s.completed || c.requestDirection == nil {
		return nil, nil
	}
	rd := *c.requestDirection
	req, resp := &s.sides[rd], &s.sides[1-rd]
	if !req.initial || !resp.initial || !req.ended || !resp.ended {
		return nil, nil
	}
	if !req.isGRPC {
		t.release(c, id, s)
		return nil, nil
	}
	status, source, message, err := responseStatus(resp)
	if err != nil {
		return nil, err
	}
	record := t.record(key, id, s, rd, status, source, message)
	t.release(c, id, s)
	return []Call{record}, nil
}
func responseStatus(resp *side) (int, string, string, error) {
	headers := resp.trailers
	if headers == nil {
		headers = resp.headers
	}
	values := headers["grpc-status"]
	if len(values) > 0 {
		if len(values) != 1 {
			return 0, "", "", protocol("duplicate grpc-status")
		}
		text := values[0]
		n, err := strconv.Atoi(text)
		if err != nil || n < 0 || n > 16 || strconv.Itoa(n) != text {
			return 0, "", "", protocol("invalid grpc-status %q", text)
		}
		message := headers.first("grpc-message")
		if decoded, err := url.PathUnescape(message); err == nil {
			message = decoded
		}
		return n, "grpc-status", message, nil
	}
	httpStatus, _ := strconv.Atoi(resp.headers.first(":status"))
	switch httpStatus {
	case 400:
		return 13, "http-status", "", nil
	case 401:
		return 16, "http-status", "", nil
	case 403:
		return 7, "http-status", "", nil
	case 404:
		return 12, "http-status", "", nil
	case 429, 502, 503, 504:
		return 14, "http-status", "", nil
	}
	return 2, "missing-grpc-status", "", nil
}
func (t *Tracker) record(key h2demux.Connection, id uint32, s *stream, rd h2demux.Direction, status int, source, message string) Call {
	req, resp := &s.sides[rd], &s.sides[1-rd]
	result := Call{PID: key.PID, Conn: key.Conn, StreamID: id, Method: req.headers.first(":path"), RequestDirection: rd.String(), RequestHeaders: req.headers, ResponseHeaders: resp.headers, Trailers: resp.trailers, Request: req.messages, Response: resp.messages, Status: status, StatusSource: source, StatusMessage: message, StartedNS: req.started, FinishedNS: resp.finished}
	result.HTTPStatus, _ = strconv.Atoi(resp.headers.first(":status"))
	for _, m := range req.messages {
		result.RequestBytes += uint64(len(m.Data))
		result.RequestWireBytes += 5 + uint64(m.WireSize)
	}
	for _, m := range resp.messages {
		result.ResponseBytes += uint64(len(m.Data))
		result.ResponseWireBytes += 5 + uint64(m.WireSize)
	}
	if result.StartedNS > 0 && result.FinishedNS >= result.StartedNS {
		result.TimingValid = true
		result.LatencyNS = result.FinishedNS - result.StartedNS
	}
	return result
}
func (t *Tracker) release(c *connection, id uint32, s *stream) {
	delete(c.streams, id)
	s.completed = true
	s.sides = [2]side{}
	s.bytes = 0
}
func (t *Tracker) Finish() error {
	for key, c := range t.connections {
		if c.failure != nil {
			return c.failure
		}
		for _, d := range c.directions {
			if d.block != nil {
				return fmt.Errorf("%w: HPACK block", ErrIncomplete)
			}
		}
		for id, s := range c.streams {
			if !s.completed && (s.sides[0].isGRPC || s.sides[1].isGRPC) {
				return fmt.Errorf("%w: pid=%d conn=%#x stream=%d", ErrIncomplete, key.PID, key.Conn, id)
			}
		}
	}
	return nil
}
func isGRPC(contentType string) bool {
	base := strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0])
	return base == "application/grpc" || strings.HasPrefix(base, "application/grpc+")
}

// RetainedBytes accounts for retained payload capacities, header fields and a
// conservative upper bound for HPACK tables. It is a buffer budget, not RSS.
func (t *Tracker) RetainedBytes() int {
	n := 0
	for _, c := range t.connections {
		n += 2 * int(t.config.MaxDynamicTableBytes)
		for _, d := range c.directions {
			if d.block != nil {
				n += cap(d.block.data)
			}
		}
		for _, s := range c.streams {
			n += s.bytes + 512
			for _, b := range s.sides {
				n += cap(b.parser.pending) + cap(b.messages)*64
				for _, headers := range []Headers{b.headers, b.trailers} {
					for name, values := range headers {
						n += len(name) + 64
						for _, v := range values {
							n += len(v) + 32
						}
					}
				}
			}
		}
	}
	return n
}

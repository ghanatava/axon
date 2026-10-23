package grpc

import (
	"fmt"
	"github.com/ghanatava/axon/internal/h2demux"
	"golang.org/x/net/http2/hpack"
	"strings"
)

func (t *Tracker) headerFragment(c *connection, f h2demux.Frame, payload []byte) ([]Call, error) {
	d := &c.directions[f.Direction]
	block := d.block
	if len(payload) > t.config.MaxHeaderBytes-len(block.data) {
		return nil, fmt.Errorf("%w: compressed headers", ErrLimit)
	}
	block.data = append(block.data, payload...)
	if f.Flags&h2demux.EndHeaders == 0 {
		return nil, nil
	}
	fields, err := t.decode(d.decoder, block.data)
	d.block = nil
	if err != nil {
		return nil, err
	}
	if block.skip {
		return nil, nil
	}
	s, err := t.getStream(c, f.ID)
	if err != nil {
		return nil, err
	}
	if s.completed {
		return nil, protocol("HEADERS after completed call")
	}
	b := &s.sides[f.Direction]
	if b.ended {
		return nil, protocol("HEADERS after END_STREAM")
	}
	if !b.initial {
		b.initial = true
		b.headers = fields
		b.started = block.timestamp
		b.encoding = fields.first("grpc-encoding")
		if b.encoding == "" {
			b.encoding = "identity"
		}
		b.isGRPC = isGRPC(fields.first("content-type"))
		method, status := fields.first(":method"), fields.first(":status")
		if method != "" && status != "" {
			return nil, protocol("mixed request/response pseudo-headers")
		}
		if method != "" {
			if f.ID <= c.highestRequest {
				return nil, protocol("reused/out-of-order request stream")
			}
			c.highestRequest = f.ID
			if err := setRequestDirection(c, f.Direction); err != nil {
				return nil, err
			}
			if b.isGRPC && (method != "POST" || !strings.HasPrefix(fields.first(":path"), "/")) {
				return nil, protocol("invalid gRPC request method/path")
			}
		} else if status != "" {
			if len(status) != 3 || status[0] < '1' || status[0] > '5' || status[1] < '0' || status[1] > '9' || status[2] < '0' || status[2] > '9' {
				return nil, protocol("invalid HTTP status")
			}
			if err := setRequestDirection(c, 1-f.Direction); err != nil {
				return nil, err
			}
		} else {
			return nil, protocol("initial headers lack method/status")
		}
		if len(fields["grpc-status"]) > 0 && !block.end {
			return nil, protocol("grpc-status before response end")
		}
	} else {
		if !block.end {
			return nil, protocol("trailing headers without END_STREAM")
		}
		for name := range fields {
			if strings.HasPrefix(name, ":") {
				return nil, protocol("pseudo-header in trailers")
			}
		}
		b.trailers = fields
	}
	if block.end {
		if b.isGRPC {
			if err := b.parser.finish(); err != nil {
				return nil, err
			}
		}
		b.ended = true
		b.finished = f.FinishedNS
	}
	return t.complete(c, f.Connection, f.ID, s)
}
func setRequestDirection(c *connection, d h2demux.Direction) error {
	if c.requestDirection != nil && *c.requestDirection != d {
		return protocol("inconsistent connection direction")
	}
	if c.requestDirection == nil {
		copy := d
		c.requestDirection = &copy
	}
	return nil
}
func (t *Tracker) decode(decoder *hpack.Decoder, block []byte) (Headers, error) {
	result := make(Headers)
	total := 0
	var failure error
	ordinary := false
	decoder.SetEmitEnabled(true)
	decoder.SetEmitFunc(func(field hpack.HeaderField) {
		if failure != nil {
			return
		}
		total += len(field.Name) + len(field.Value) + 32
		if total > t.config.MaxHeaderBytes {
			failure = fmt.Errorf("%w: expanded headers", ErrLimit)
			decoder.SetEmitEnabled(false)
			return
		}
		if field.Name == "" || strings.ToLower(field.Name) != field.Name {
			failure = protocol("invalid header name")
			return
		}
		switch field.Name {
		case "content-type", "grpc-encoding", "grpc-status":
			if len(result[field.Name]) != 0 {
				failure = protocol("duplicate %s", field.Name)
				return
			}
		}
		if strings.HasPrefix(field.Name, ":") {
			if ordinary || len(result[field.Name]) > 0 {
				failure = protocol("duplicate/out-of-order pseudo-header")
				return
			}
		} else {
			ordinary = true
		}
		result[field.Name] = append(result[field.Name], field.Value)
	})
	_, err := decoder.Write(block)
	if err == nil {
		err = decoder.Close()
	}
	decoder.SetEmitFunc(func(hpack.HeaderField) {})
	if err != nil {
		return nil, protocol("HPACK decode: %v", err)
	}
	if failure != nil {
		return nil, failure
	}
	return result, nil
}

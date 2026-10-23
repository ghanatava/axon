package grpc

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"io"
)

// Message contains the protobuf/JSON bytes, not decoded application fields.
// WireSize excludes the five-byte gRPC envelope; Data is decompressed for gzip.
type Message struct {
	Compressed bool   `json:"compressed"`
	Encoding   string `json:"encoding"`
	WireSize   uint32 `json:"wire_size"`
	Data       []byte `json:"data"`
}
type messageParser struct {
	pending  []byte
	expected int
}

func (p *messageParser) push(data []byte, encoding string, config Config, budget *int, remainingMessages int) ([]Message, error) {
	var messages []Message
	for len(data) > 0 {
		target := 5
		if p.expected != 0 {
			target = p.expected
		}
		n := target - len(p.pending)
		if n > len(data) {
			n = len(data)
		}
		p.pending = append(p.pending, data[:n]...)
		data = data[n:]
		if len(p.pending) < target {
			continue
		}
		if p.expected == 0 {
			if p.pending[0] > 1 {
				return nil, protocol("invalid gRPC compressed flag")
			}
			size := binary.BigEndian.Uint32(p.pending[1:])
			if uint64(size) > uint64(config.MaxMessageBytes) {
				return nil, fmt.Errorf("%w: message wire size", ErrLimit)
			}
			p.expected = 5 + int(size)
			if len(p.pending) < p.expected {
				continue
			}
		}
		if len(messages) >= remainingMessages {
			return nil, fmt.Errorf("%w: message count", ErrLimit)
		}
		wire := p.pending[5:]
		compressed := p.pending[0] == 1
		var decoded []byte
		if compressed {
			if encoding != "gzip" {
				return nil, protocol("unsupported message compression %q", encoding)
			}
			reader, err := gzip.NewReader(bytes.NewReader(wire))
			if err != nil {
				return nil, protocol("invalid gzip: %v", err)
			}
			decoded, err = io.ReadAll(io.LimitReader(reader, int64(config.MaxMessageBytes)+1))
			closeErr := reader.Close()
			if err != nil || closeErr != nil {
				return nil, protocol("invalid gzip body")
			}
			if len(decoded) > config.MaxMessageBytes {
				return nil, fmt.Errorf("%w: decompressed message size", ErrLimit)
			}
		} else {
			decoded = append(make([]byte, 0, len(wire)), wire...)
		}
		if len(decoded) > config.MaxCallBytes-*budget {
			return nil, fmt.Errorf("%w: call payload bytes", ErrLimit)
		}
		*budget += len(decoded)
		messages = append(messages, Message{Compressed: compressed, Encoding: encoding, WireSize: uint32(len(wire)), Data: decoded})
		p.pending = nil
		p.expected = 0
	}
	return messages, nil
}
func (p *messageParser) finish() error {
	if len(p.pending) != 0 {
		return fmt.Errorf("%w: partial gRPC message", ErrIncomplete)
	}
	return nil
}

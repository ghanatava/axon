package h2demux

import (
	"fmt"
	"github.com/ghanatava/axon/internal/tlscapture"
)

// At most 64 x 4096-byte chunks are emitted for one TLS call. Larger calls
// explicitly fail capture rather than silently joining bytes across a gap.
const MaxCallBytes = 64 * tlscapture.MaxData

type callIdentity struct{ started, goid uint64 }

type call struct {
	started, goid, total uint64
	data                 []byte
}

// Push reassembles chunks of a successful TLS call before feeding its plaintext
// into the frame parser. GoID identifies chunks of a call, not a connection.
// Old Phase 2 events are accepted only if the entire call was captured.
func (d *Demux) Push(e tlscapture.Event) ([]Frame, error) {
	key := Connection{PID: e.PID, Conn: e.Conn}
	direction := Direction(e.Direction)
	p, err := d.direction(key, direction)
	if err != nil {
		return nil, err
	}
	if p.failure != nil {
		return nil, p.failure
	}
	reject := func(reason string) ([]Frame, error) {
		err := fmt.Errorf("%w: %s", ErrGap, reason)
		p.fail(err)
		return nil, err
	}
	if e.Total == 0 || len(e.Data) == 0 || e.GoID == 0 {
		return reject("empty or unidentified TLS call")
	}
	if e.Total > MaxCallBytes {
		return reject("TLS call exceeds capture bound")
	}
	if !e.Chunked {
		if e.Offset != 0 || uint64(len(e.Data)) != e.Total {
			return reject("truncated legacy TLS event")
		}
		if p.call != nil {
			return reject("unfinished chunked call")
		}
		return d.FeedAt(key, direction, e.Data, e.Started, e.Finished)
	}
	if len(e.Data) > tlscapture.MaxData {
		return reject("chunk exceeds event capacity")
	}
	if e.Offset > e.Total || uint64(len(e.Data)) > e.Total-e.Offset {
		return reject("chunk exceeds call length")
	}
	if e.Offset == 0 {
		if p.completed != nil && *p.completed == (callIdentity{started: e.Started, goid: e.GoID}) {
			return reject("duplicate completed TLS call")
		}
		if p.call != nil {
			return reject("new call before previous call completed")
		}
		p.call = &call{started: e.Started, goid: e.GoID, total: e.Total}
	}
	c := p.call
	if c == nil || c.started != e.Started || c.goid != e.GoID || c.total != e.Total || uint64(len(c.data)) != e.Offset {
		return reject("missing, reordered or mixed TLS chunk")
	}
	c.data = append(c.data, e.Data...)
	if uint64(len(c.data)) < c.total {
		return nil, nil
	}
	p.call = nil
	p.completed = &callIdentity{started: e.Started, goid: e.GoID}
	return d.FeedAt(key, direction, c.data, c.started, e.Finished)
}

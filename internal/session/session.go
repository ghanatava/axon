// Package session isolates parser state by process and TLS capture incarnation.
// It is owned by one consumer goroutine; different connections never share HPACK.
package session

import (
	"fmt"
	grpcsem "github.com/ghanatava/axon/internal/grpc"
	"github.com/ghanatava/axon/internal/h2demux"
	"github.com/ghanatava/axon/internal/tlscapture"
	"time"
)

type Key struct {
	PID                            uint32
	ProcessStart, Conn, Generation uint64
}

func EventKey(e tlscapture.Event) Key { return Key{e.PID, e.ProcessStart, e.Conn, e.Generation} }

type base struct {
	pid         uint32
	start, conn uint64
}

func (k Key) base() base { return base{k.PID, k.ProcessStart, k.Conn} }

type Config struct {
	Mode             string
	MaxConnections   int
	IdleTimeout      time.Duration
	MaxBufferedBytes int
	Now              func() time.Time
}
type Metrics struct {
	Opened              uint64 `json:"opened"`
	Closed              uint64 `json:"closed"`
	Expired             uint64 `json:"expired"`
	Evicted             uint64 `json:"evicted"`
	Invalidated         uint64 `json:"invalidated"`
	DiscardedEvents     uint64 `json:"discarded_events"`
	Incomplete          uint64 `json:"incomplete"`
	Calls               uint64 `json:"calls"`
	Frames              uint64 `json:"frames"`
	GoroutineMigrations uint64 `json:"goroutine_migrations"`
	Active              int    `json:"active"`
	Peak                int    `json:"peak"`
	RetainedBytes       int    `json:"retained_bytes"`
}
type Result struct {
	Key    Key
	Frames []h2demux.Frame
	Calls  []grpcsem.Call
}
type state struct {
	demux   *h2demux.Demux
	grpc    *grpcsem.Tracker
	next    [2]uint64
	offset  [2]uint64
	last    time.Time
	retired bool
	bytes   int
}
type goroutine struct {
	pid       uint32
	start, id uint64
}
type Runtime struct {
	config  Config
	states  map[Key]*state
	latest  map[base]Key
	threads map[goroutine]uint32
	metrics Metrics
}

func New(c Config) (*Runtime, error) {
	if c.Mode != "h2" && c.Mode != "grpc" {
		return nil, fmt.Errorf("invalid session mode")
	}
	if c.MaxConnections == 0 {
		c.MaxConnections = 1024
	}
	if c.IdleTimeout == 0 {
		c.IdleTimeout = 2 * time.Minute
	}
	if c.MaxBufferedBytes == 0 {
		c.MaxBufferedBytes = 128 << 20
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.MaxConnections < 1 || c.IdleTimeout < 0 || c.MaxBufferedBytes < 1 {
		return nil, fmt.Errorf("invalid session bounds")
	}
	return &Runtime{config: c, states: make(map[Key]*state), latest: make(map[base]Key), threads: make(map[goroutine]uint32)}, nil
}
func (r *Runtime) Metrics() Metrics {
	m := r.metrics
	for _, s := range r.states {
		if !s.retired {
			m.Active++
		}
	}
	return m
}
func (r *Runtime) drop(s *state) {
	r.metrics.RetainedBytes -= s.bytes
	s.bytes = 0
	s.demux = nil
	s.grpc = nil
	s.retired = true
}
func (r *Runtime) invalidate(s *state) {
	if !s.retired {
		r.metrics.Invalidated++
		r.drop(s)
	}
}

// Invalidate handles producer-side loss even when a ring-full failure prevented
// delivery of an explicit loss record. It never resets HPACK at an arbitrary byte.
func (r *Runtime) Invalidate(key Key) {
	if s := r.states[key]; s != nil {
		r.invalidate(s)
	}
}
func (r *Runtime) remove(key Key) {
	s := r.states[key]
	if s == nil {
		return
	}
	r.drop(s)
	delete(r.states, key)
	if r.latest[key.base()] == key {
		delete(r.latest, key.base())
	}
}
func (r *Runtime) Sweep() {
	now := r.config.Now()
	for key, s := range r.states {
		if now.Sub(s.last) >= r.config.IdleTimeout {
			if !s.retired {
				r.metrics.Expired++
			}
			r.remove(key)
		}
	}
}
func (r *Runtime) RemoveProcess(pid uint32, start uint64) {
	for key := range r.states {
		if key.PID == pid && key.ProcessStart == start {
			r.remove(key)
		}
	}
	for g := range r.threads {
		if g.pid == pid && g.start == start {
			delete(r.threads, g)
		}
	}
}
func (r *Runtime) create(key Key) (*state, error) {
	if len(r.states) >= r.config.MaxConnections {
		var oldest Key
		var when time.Time
		for k, s := range r.states {
			if when.IsZero() || s.last.Before(when) {
				oldest = k
				when = s.last
			}
		}
		r.remove(oldest)
		r.metrics.Evicted++
	}
	d, err := h2demux.New(h2demux.Config{MaxConnections: 1})
	if err != nil {
		return nil, err
	}
	s := &state{demux: d, next: [2]uint64{1, 1}, last: r.config.Now()}
	if r.config.Mode == "grpc" {
		s.grpc, err = grpcsem.New(grpcsem.Config{MaxConnections: 1})
		if err != nil {
			return nil, err
		}
	}
	r.states[key] = s
	r.latest[key.base()] = key
	r.metrics.Opened++
	active := r.Metrics().Active
	if active > r.metrics.Peak {
		r.metrics.Peak = active
	}
	return s, nil
}
func (r *Runtime) Push(e tlscapture.Event) (Result, error) {
	key := EventKey(e)
	out := Result{Key: key}
	live := e.Version == tlscapture.LifecycleVersion
	if e.PID == 0 || e.Conn == 0 || e.Direction > 1 || e.Version != 0 && !live {
		return out, fmt.Errorf("invalid capture identity/version")
	}
	if live && (e.ProcessStart == 0 || e.Generation == 0 || e.Kind > tlscapture.Loss) {
		return out, fmt.Errorf("invalid capture incarnation")
	}
	if latest, ok := r.latest[key.base()]; ok && latest != key {
		if latest.Generation > key.Generation {
			r.metrics.DiscardedEvents++
			return out, nil
		}
		r.Invalidate(latest)
	}
	s := r.states[key]
	var err error
	if e.Kind != tlscapture.Data {
		if !live {
			return out, fmt.Errorf("lifecycle record without version3")
		}
		if s == nil {
			s, err = r.create(key)
			if err != nil {
				return out, err
			}
		}
		if s != nil {
			if e.Kind == tlscapture.Loss || e.Failure != 0 {
				r.invalidate(s)
			}
			if e.Kind == tlscapture.Close {
				if !s.retired {
					if s.demux.Finish() != nil || s.grpc != nil && s.grpc.Finish() != nil {
						r.metrics.Incomplete++
					}
					r.drop(s)
				}
				r.metrics.Closed++
			}
			s.last = r.config.Now()
		}
		return out, nil
	}
	if s == nil {
		s, err = r.create(key)
		if err != nil {
			return out, err
		}
	}
	s.last = r.config.Now()
	if s.retired {
		r.metrics.DiscardedEvents++
		return out, nil
	}
	reject := func(err error) (Result, error) {
		if live {
			r.invalidate(s)
			r.metrics.DiscardedEvents++
			return Result{Key: key}, nil
		}
		return out, err
	}
	if live {
		if e.Failure != 0 || e.Sequence != s.next[e.Direction] || e.Offset != s.offset[e.Direction] || e.Total == 0 || e.Offset > e.Total || uint64(len(e.Data)) > e.Total-e.Offset {
			return reject(fmt.Errorf("capture sequence/offset gap"))
		}
		s.offset[e.Direction] += uint64(len(e.Data))
		if s.offset[e.Direction] == e.Total {
			s.next[e.Direction]++
			s.offset[e.Direction] = 0
		}
		if e.Offset == 0 && e.TID != 0 && e.GoID != 0 {
			g := goroutine{e.PID, e.ProcessStart, e.GoID}
			if previous := r.threads[g]; previous != 0 && previous != e.TID {
				r.metrics.GoroutineMigrations++
			}
			if len(r.threads) >= 4096 {
				clear(r.threads)
			}
			r.threads[g] = e.TID
		}
	}
	frames, err := s.demux.Push(e)
	if err != nil {
		return reject(err)
	}
	for _, f := range frames {
		if s.grpc != nil {
			calls, err := s.grpc.Push(f)
			if err != nil {
				return reject(err)
			}
			for i := range calls {
				calls[i].ProcessStart = e.ProcessStart
				calls[i].Generation = e.Generation
			}
			out.Calls = append(out.Calls, calls...)
		} else {
			out.Frames = append(out.Frames, f)
		}
	}
	retained := s.demux.RetainedBytes()
	if s.grpc != nil {
		retained += s.grpc.RetainedBytes()
	}
	r.metrics.RetainedBytes += retained - s.bytes
	s.bytes = retained
	if r.metrics.RetainedBytes > r.config.MaxBufferedBytes {
		return reject(fmt.Errorf("session buffer budget exceeded"))
	}
	r.metrics.Frames += uint64(len(frames))
	r.metrics.Calls += uint64(len(out.Calls))
	return out, nil
}
func (r *Runtime) Finish() error {
	for _, s := range r.states {
		if !s.retired {
			if err := s.demux.Finish(); err != nil {
				return err
			}
			if s.grpc != nil {
				if err := s.grpc.Finish(); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

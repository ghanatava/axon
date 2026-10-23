//go:build linux && amd64

package main

import (
	"errors"
	"fmt"
	"github.com/cilium/ebpf"
	"github.com/ghanatava/axon/internal/session"
	"github.com/ghanatava/axon/internal/tlscapture"
	"os"
	"strconv"
	"strings"
	"time"
)

// Layouts mirror BPF maps. Padding is explicit; binary ABI is tested.
type kernelConnectionKey struct {
	PID, Pad           uint32
	ProcessStart, Conn uint64
}
type kernelConnectionState struct {
	Generation, LastActive uint64
	Active, Sequence       [2]uint64
	Failed, Closed         uint64
}
type kernelCallKey struct {
	PID, Direction            uint32
	ProcessStart, GoID, Frame uint64
}
type kernelCall struct{ Conn, Buf, Length, StackOffset, Started, Generation uint64 }
type lossMonitor struct {
	collection          *ebpf.Collection
	processor           *processor
	lastSweep, lastReap time.Time
	supervised          bool
	verifyProcess       func() error
}

func newLossMonitor(c *ebpf.Collection, p *processor) *lossMonitor {
	return &lossMonitor{collection: c, processor: p}
}
func (m *lossMonitor) Check(event *tlscapture.Event) error {
	// Unknown ownership cannot be recovered safely. Correlated loss, by contrast,
	// retires only that capture incarnation while other connections keep running.
	if event != nil && m.processor.runtime != nil {
		index := uint32(5)
		var count uint64
		if err := m.collection.Maps["stats"].Lookup(&index, &count); err != nil {
			return err
		}
		if count > 0 {
			return fmt.Errorf("unattributed capture loss=%d; refusing ambiguous correlation", count)
		}
		if event.Version == tlscapture.LifecycleVersion && event.Kind == tlscapture.Data {
			key := kernelConnectionKey{PID: event.PID, ProcessStart: event.ProcessStart, Conn: event.Conn}
			var state kernelConnectionState
			err := m.collection.Maps["connections"].Lookup(&key, &state)
			if err != nil && !errors.Is(err, ebpf.ErrKeyNotExist) {
				return err
			}
			if err == nil && state.Generation == event.Generation && state.Failed != 0 {
				lost := *event
				lost.Kind = tlscapture.Loss
				lost.Total = 0
				lost.Offset = 0
				lost.Sequence = 0
				lost.Data = nil
				lost.Failure = uint32(state.Failed)
				if err := m.processor.Process(lost); err != nil {
					return err
				}
			}
		}
	}
	now := time.Now()
	if now.Sub(m.lastSweep) >= 250*time.Millisecond {
		if m.supervised {
			key := uint32(0)
			var stopped uint64
			if err := m.collection.Maps["exec_stop"].Lookup(&key, &stopped); err != nil {
				return err
			}
			if stopped != 0 {
				return fmt.Errorf("target process executed a new image")
			}
		}
		if m.verifyProcess != nil {
			if err := m.verifyProcess(); err != nil {
				return err
			}
		}
		if m.processor.runtime != nil {
			iter := m.collection.Maps["connections"].Iterate()
			var key kernelConnectionKey
			var state kernelConnectionState
			for iter.Next(&key, &state) {
				if state.Failed != 0 {
					m.processor.runtime.Invalidate(session.Key{PID: key.PID, ProcessStart: key.ProcessStart, Conn: key.Conn, Generation: state.Generation})
				}
			}
			if err := iter.Err(); err != nil {
				return err
			}
			m.processor.runtime.Sweep()
		}
		if m.processor.metrics != nil || m.supervised {
			var counts [6]uint64
			for i := range counts {
				key := uint32(i)
				if err := m.collection.Maps["stats"].Lookup(&key, &counts[i]); err != nil {
					return err
				}
			}
			if m.supervised {
				if err := m.processor.json.Encode(struct {
					Telemetry telemetry `json:"axon_telemetry"`
				}{telemetry{Runtime: m.processor.runtime.Metrics(), Capture: counts}}); err != nil {
					return err
				}
			}
			if m.processor.metrics != nil {
				m.processor.metrics.UpdateCapture(counts)
				if m.processor.runtime != nil {
					m.processor.metrics.UpdateRuntime(m.processor.runtime.Metrics())
				}
			}
		}
		m.lastSweep = now
	}
	if now.Sub(m.lastReap) >= 5*time.Second {
		if err := m.reap(); err != nil {
			return err
		}
		m.lastReap = now
	}
	return nil
}

type processIdentity struct {
	pid   uint32
	start uint64
}

func (m *lossMonitor) reap() error {
	alive := make(map[processIdentity]bool)
	isAlive := func(pid uint32, start uint64) bool {
		key := processIdentity{pid, start}
		value, ok := alive[key]
		if !ok {
			value = processAlive(pid, start)
			alive[key] = value
			// Queued ring records outlive a process. Keep user-space state
			// until its FIFO Close event or idle expiry has drained it.
		}
		return value
	}
	iter := m.collection.Maps["connections"].Iterate()
	var key kernelConnectionKey
	var state kernelConnectionState
	var dead []kernelConnectionKey
	for iter.Next(&key, &state) {
		if !isAlive(key.PID, key.ProcessStart) {
			dead = append(dead, key)
		}
	}
	if err := iter.Err(); err != nil {
		return err
	}
	for _, key := range dead {
		if err := m.collection.Maps["connections"].Delete(&key); err != nil && !errors.Is(err, ebpf.ErrKeyNotExist) {
			return err
		}
	}
	calls := m.collection.Maps["calls"].Iterate()
	var ck kernelCallKey
	var call kernelCall
	var deadCalls []kernelCallKey
	for calls.Next(&ck, &call) {
		if !isAlive(ck.PID, ck.ProcessStart) {
			deadCalls = append(deadCalls, ck)
		}
	}
	if err := calls.Err(); err != nil {
		return err
	}
	for _, key := range deadCalls {
		if err := m.collection.Maps["calls"].Delete(&key); err != nil && !errors.Is(err, ebpf.ErrKeyNotExist) {
			return err
		}
	}
	return nil
}

// Linux amd64 /proc uses USER_HZ=100. Compare process birth times, not PID alone.
// Permission/transient parse failures are conservative: never reap a live task.
func processAlive(pid uint32, start uint64) bool {
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if errors.Is(err, os.ErrNotExist) {
		return false
	}
	if err != nil {
		return true
	}
	end := strings.LastIndexByte(string(raw), ')')
	if end < 0 {
		return true
	}
	fields := strings.Fields(string(raw)[end+1:])
	if len(fields) < 20 {
		return true
	}
	ticks, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return true
	}
	return ticks == start/10_000_000
}

//go:build linux && amd64

package main

import (
	"encoding/json"
	"fmt"
	"github.com/ghanatava/axon/internal/h2demux"
	"github.com/ghanatava/axon/internal/metrics"
	"github.com/ghanatava/axon/internal/session"
	"github.com/ghanatava/axon/internal/tlscapture"
	"io"
	"os"
)

type processor struct {
	runtime *session.Runtime
	metrics *metrics.Exporter
	output  io.Writer
	json    *json.Encoder
}

func newProcessor(mode string, output io.Writer) (*processor, error) {
	return newProcessorConfig(mode, output, session.Config{})
}
func newProcessorConfig(mode string, output io.Writer, c session.Config) (*processor, error) {
	p := &processor{output: output, json: json.NewEncoder(output)}
	switch mode {
	case "raw":
	case "h2", "grpc":
		c.Mode = mode
		r, err := session.New(c)
		if err != nil {
			return nil, err
		}
		p.runtime = r
	default:
		return nil, fmt.Errorf("unknown output mode %q (use raw, h2 or grpc)", mode)
	}
	return p, nil
}
func (p *processor) Process(event tlscapture.Event) error {
	if p.runtime == nil {
		if event.Kind != tlscapture.Data {
			return nil
		}
		_, err := fmt.Fprintf(p.output, "pid=%d tid=%d goid=%d conn=%#x %s n=%d captured=%d data=%q\n", event.PID, event.TID, event.GoID, event.Conn, h2demux.Direction(event.Direction), event.Total, len(event.Data), event.Data)
		return err
	}
	result, err := p.runtime.Push(event)
	if p.metrics != nil {
		p.metrics.UpdateRuntime(p.runtime.Metrics())
	}
	for _, call := range result.Calls {
		if p.metrics != nil {
			p.metrics.Observe(call)
		}
		if writeErr := p.json.Encode(call); writeErr != nil {
			return writeErr
		}
	}
	for _, frame := range result.Frames {
		record := struct {
			PID          uint32 `json:"pid"`
			Conn         uint64 `json:"conn"`
			ProcessStart uint64 `json:"process_start,omitempty"`
			Generation   uint64 `json:"generation,omitempty"`
			Direction    string `json:"direction"`
			StreamID     uint32 `json:"stream_id"`
			Sequence     uint64 `json:"sequence"`
			Type         string `json:"type"`
			Flags        uint8  `json:"flags"`
			Length       int    `json:"length"`
			Payload      []byte `json:"payload"`
		}{frame.PID, frame.Conn, result.Key.ProcessStart, result.Key.Generation, frame.Direction.String(), frame.ID, frame.Sequence, frame.Type.String(), frame.Flags, len(frame.Payload), frame.Payload}
		if writeErr := p.json.Encode(record); writeErr != nil {
			return writeErr
		}
	}
	return err
}
func (p *processor) Finish() error {
	if p.runtime != nil {
		return p.runtime.Finish()
	}
	return nil
}
func replayEvents(path string, p *processor) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	for {
		var event tlscapture.Event
		if err := decoder.Decode(&event); err == io.EOF {
			break
		} else if err != nil {
			return fmt.Errorf("replay: %w", err)
		}
		if err := p.Process(event); err != nil {
			return err
		}
	}
	return p.Finish()
}

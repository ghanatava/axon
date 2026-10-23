// Package metrics exports bounded gRPC observations without retaining payloads.
package metrics

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"unicode/utf8"

	grpcsem "github.com/ghanatava/axon/internal/grpc"
	"github.com/ghanatava/axon/internal/session"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/common/expfmt"
	"io"
)

var buckets = []float64{.001, .0025, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10}
var captureReasons = []string{"memory_identity", "map", "ring", "truncated", "overlap", "unattributed"}

type Config struct {
	Service, Namespace, Pod string
	MaxSeries               int
}
type Workload struct{ Service, Namespace, Pod string }
type key struct {
	method, role, status string
	workload             Workload
}
type observation struct {
	count, untimed, requestBytes, responseBytes uint64
	sum                                         float64
	histogram                                   map[float64]uint64
	timed                                       uint64
}

// Exporter is safe for concurrent scrapes. Runtime snapshots must be supplied by
// the capture consumer; the HTTP handler never reads mutable parser state.
type Exporter struct {
	mu                                                                sync.Mutex
	config                                                            Config
	groups                                                            map[key]*observation
	dropped                                                           uint64
	runtime                                                           session.Metrics
	capture                                                           [6]uint64
	registry                                                          *prometheus.Registry
	calls, duration, untimed, requestBytes, responseBytes, overflow   *prometheus.Desc
	active, retained, lifecycle, discarded, incomplete, captureErrors *prometheus.Desc
}

func New(c Config) (*Exporter, error) {
	if c.MaxSeries == 0 {
		c.MaxSeries = 1024
	}
	if c.MaxSeries < 1 {
		return nil, fmt.Errorf("metrics-max-series must be positive")
	}
	for _, v := range []string{c.Service, c.Namespace, c.Pod} {
		if len(v) > 256 || !utf8.ValidString(v) {
			return nil, fmt.Errorf("workload labels must be UTF-8 and at most 256 bytes")
		}
	}
	if c.Service == "" {
		c.Service = "unknown"
	}
	if c.Namespace == "" {
		c.Namespace = "unknown"
	}
	if c.Pod == "" {
		c.Pod = "unknown"
	}
	e := &Exporter{config: c, groups: make(map[key]*observation), registry: prometheus.NewRegistry()}
	labels := []string{"method", "role", "grpc_status", "service", "namespace", "pod"}
	fixed := prometheus.Labels{"service": c.Service, "namespace": c.Namespace, "pod": c.Pod}
	desc := func(name, help string, labels []string) *prometheus.Desc {
		if strings.HasPrefix(name, "grpc_") {
			return prometheus.NewDesc("axon_"+name, help, labels, nil)
		}
		return prometheus.NewDesc("axon_"+name, help, labels, fixed)
	}
	e.calls = desc("grpc_requests_total", "Completed gRPC endpoint observations. Select one role to avoid counting both endpoints.", labels)
	e.duration = desc("grpc_request_duration_seconds", "Observed request-to-response interval; only valid capture timings are included.", labels)
	e.untimed = desc("grpc_requests_without_timing_total", "Completed observations with unavailable timing; excluded from latency histogram.", labels)
	e.requestBytes = desc("grpc_request_bytes_total", "Decoded protobuf request bytes (excluding gRPC envelopes).", labels)
	e.responseBytes = desc("grpc_response_bytes_total", "Decoded protobuf response bytes (excluding gRPC envelopes).", labels)
	e.overflow = desc("metrics_dropped_observations_total", "Observations omitted because the metric label-group limit was reached.", nil)
	e.active = desc("capture_active_connections", "Active parser connection incarnations.", nil)
	e.retained = desc("capture_retained_bytes", "Estimated retained parser buffer bytes, not total RSS.", nil)
	e.lifecycle = desc("capture_connections_total", "Connection lifecycle transitions.", []string{"event"})
	e.discarded = desc("capture_discarded_events_total", "Events discarded from retired or stale sessions.", nil)
	e.incomplete = desc("capture_incomplete_total", "Incomplete sessions encountered during lifecycle cleanup.", nil)
	e.captureErrors = desc("capture_errors_total", "Producer capture errors; missing observations can make RPC metrics incomplete.", []string{"reason"})
	if err := e.registry.Register(e); err != nil {
		return nil, err
	}
	return e, nil
}
func (e *Exporter) Observe(c grpcsem.Call) {
	e.ObserveWorkload(c, Workload{e.config.Service, e.config.Namespace, e.config.Pod})
}
func (e *Exporter) ObserveWorkload(c grpcsem.Call, w Workload) {
	for _, v := range []*string{&w.Service, &w.Namespace, &w.Pod} {
		if *v == "" || len(*v) > 256 || !utf8.ValidString(*v) {
			*v = "unknown"
		}
	}
	role := "unknown"
	if c.RequestDirection == "read" {
		role = "server"
	}
	if c.RequestDirection == "write" {
		role = "client"
	}
	method := c.Method
	if len(method) > 256 || !utf8.ValidString(method) || !strings.HasPrefix(method, "/") || strings.Count(method, "/") != 2 || strings.IndexFunc(method, func(r rune) bool { return r <= 32 || r == 127 }) >= 0 {
		method = "unknown"
	}
	status := "unknown"
	if c.Status >= 0 && c.Status <= 16 {
		status = fmt.Sprint(c.Status)
	}
	k := key{method, role, status, w}
	e.mu.Lock()
	defer e.mu.Unlock()
	o := e.groups[k]
	if o == nil {
		if len(e.groups) >= e.config.MaxSeries {
			e.dropped++
			return
		}
		o = &observation{histogram: make(map[float64]uint64)}
		e.groups[k] = o
	}
	o.count++
	o.requestBytes += c.RequestBytes
	o.responseBytes += c.ResponseBytes
	if !c.TimingValid {
		o.untimed++
		return
	}
	seconds := float64(c.LatencyNS) / 1e9
	o.sum += seconds
	o.timed++
	for _, b := range buckets {
		if seconds <= b {
			o.histogram[b]++
		}
	}
}
func (e *Exporter) UpdateRuntime(m session.Metrics) { e.mu.Lock(); e.runtime = m; e.mu.Unlock() }
func (e *Exporter) UpdateCapture(m [6]uint64)       { e.mu.Lock(); e.capture = m; e.mu.Unlock() }
func (e *Exporter) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{e.calls, e.duration, e.untimed, e.requestBytes, e.responseBytes, e.overflow, e.active, e.retained, e.lifecycle, e.discarded, e.incomplete, e.captureErrors} {
		ch <- d
	}
}
func (e *Exporter) Collect(ch chan<- prometheus.Metric) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for k, o := range e.groups {
		labels := []string{k.method, k.role, k.status, k.workload.Service, k.workload.Namespace, k.workload.Pod}
		for _, v := range []struct {
			d *prometheus.Desc
			n uint64
		}{{e.calls, o.count}, {e.untimed, o.untimed}, {e.requestBytes, o.requestBytes}, {e.responseBytes, o.responseBytes}} {
			ch <- prometheus.MustNewConstMetric(v.d, prometheus.CounterValue, float64(v.n), labels...)
		}
		ch <- prometheus.MustNewConstHistogram(e.duration, o.timed, o.sum, o.histogram, labels...)
	}
	ch <- prometheus.MustNewConstMetric(e.overflow, prometheus.CounterValue, float64(e.dropped))
	ch <- prometheus.MustNewConstMetric(e.active, prometheus.GaugeValue, float64(e.runtime.Active))
	ch <- prometheus.MustNewConstMetric(e.retained, prometheus.GaugeValue, float64(e.runtime.RetainedBytes))
	for _, v := range []struct {
		name string
		n    uint64
	}{{"opened", e.runtime.Opened}, {"closed", e.runtime.Closed}, {"expired", e.runtime.Expired}, {"evicted", e.runtime.Evicted}, {"invalidated", e.runtime.Invalidated}} {
		ch <- prometheus.MustNewConstMetric(e.lifecycle, prometheus.CounterValue, float64(v.n), v.name)
	}
	ch <- prometheus.MustNewConstMetric(e.discarded, prometheus.CounterValue, float64(e.runtime.DiscardedEvents))
	ch <- prometheus.MustNewConstMetric(e.incomplete, prometheus.CounterValue, float64(e.runtime.Incomplete))
	for i, n := range e.capture {
		ch <- prometheus.MustNewConstMetric(e.captureErrors, prometheus.CounterValue, float64(n), captureReasons[i])
	}
}
func (e *Exporter) Register(c prometheus.Collector) error { return e.registry.Register(c) }
func (e *Exporter) Handler() http.Handler {
	return promhttp.HandlerFor(e.registry, promhttp.HandlerOpts{MaxRequestsInFlight: 4})
}
func (e *Exporter) Write(w io.Writer) error {
	families, err := e.registry.Gather()
	if err != nil {
		return err
	}
	enc := expfmt.NewEncoder(w, expfmt.NewFormat(expfmt.TypeTextPlain))
	for _, f := range families {
		if err := enc.Encode(f); err != nil {
			return err
		}
	}
	return nil
}

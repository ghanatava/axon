package metrics

import (
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	grpcsem "github.com/ghanatava/axon/internal/grpc"
	"github.com/ghanatava/axon/internal/session"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
)

func gather(t *testing.T, e *Exporter) map[string]*dto.MetricFamily {
	t.Helper()
	r := httptest.NewRecorder()
	e.Handler().ServeHTTP(r, httptest.NewRequest("GET", "/metrics", nil))
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	p := expfmt.NewTextParser(model.UTF8Validation)
	m, err := p.TextToMetricFamilies(strings.NewReader(r.Body.String()))
	if err != nil {
		t.Fatal(err)
	}
	return m
}
func TestSemantics(t *testing.T) {
	e, err := New(Config{Service: "echo", Namespace: "test", Pod: "pod\"\\\n"})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []grpcsem.Call{
		{Method: "/echo/Unary", RequestDirection: "read", Status: 0, TimingValid: true, LatencyNS: 2_000_000, RequestBytes: 3, ResponseBytes: 4},
		{Method: "/echo/Unary", RequestDirection: "read", Status: 3},
		{Method: "/echo/Unary", RequestDirection: "write", Status: 0, TimingValid: true, LatencyNS: 20_000_000},
	} {
		e.Observe(c)
	}
	e.UpdateRuntime(session.Metrics{Active: 2, RetainedBytes: 100, Invalidated: 1})
	e.UpdateCapture([6]uint64{0, 0, 8})
	m := gather(t, e)
	if len(m["axon_grpc_requests_total"].Metric) != 3 {
		t.Fatal("roles/status merged")
	}
	for _, metric := range m["axon_grpc_request_duration_seconds"].Metric {
		labels := map[string]string{}
		for _, l := range metric.Label {
			labels[l.GetName()] = l.GetValue()
		}
		if labels["pod"] != "pod\"\\\n" {
			t.Fatal("bad escaping")
		}
		h := metric.Histogram
		if labels["grpc_status"] == "3" {
			if h.GetSampleCount() != 0 {
				t.Fatal("unknown timing counted")
			}
			continue
		}
		if h.GetSampleCount() != 1 {
			t.Fatal("histogram count")
		}
		expected := .002
		if labels["role"] == "client" {
			expected = .02
		}
		if h.GetSampleSum() != expected {
			t.Fatal(h)
		}
		for _, b := range h.Bucket {
			want := uint64(0)
			if b.GetUpperBound() >= expected {
				want = 1
			}
			if b.GetCumulativeCount() != want {
				t.Fatal("non-cumulative bucket", b)
			}
		}
	}
	if m["axon_capture_active_connections"].Metric[0].Gauge.GetValue() != 2 {
		t.Fatal("runtime snapshot")
	}
	var dropped float64
	for _, v := range m["axon_capture_errors_total"].Metric {
		for _, l := range v.Label {
			if l.GetName() == "reason" && l.GetValue() == "ring" {
				dropped = v.Counter.GetValue()
			}
		}
	}
	if dropped != 8 {
		t.Fatal("capture loss missing")
	}
}
func TestCardinalityAndConcurrentScrapes(t *testing.T) {
	e, _ := New(Config{MaxSeries: 2})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 100; n++ {
				e.Observe(grpcsem.Call{Method: fmt.Sprintf("/echo/M%d", n%4), RequestDirection: "read"})
				if err := e.Write(io.Discard); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
	m := gather(t, e)
	if len(m["axon_grpc_requests_total"].Metric) != 2 {
		t.Fatal("unbounded labels")
	}
	if m["axon_metrics_dropped_observations_total"].Metric[0].Counter.GetValue() != 400 {
		t.Fatal("overflow must be visible")
	}
	if _, err := New(Config{MaxSeries: -1}); err == nil {
		t.Fatal("invalid bound accepted")
	}
}

func TestUnknownTimingIsNotZeroLatency(t *testing.T) {
	e, _ := New(Config{})
	e.Observe(grpcsem.Call{Method: "/echo/Unary", RequestDirection: "read", Status: 0, TimingValid: false, LatencyNS: 99_000_000})
	e.Observe(grpcsem.Call{Method: "/echo/Unary", RequestDirection: "read", Status: 0, TimingValid: true, LatencyNS: 0})
	m := gather(t, e)
	h := m["axon_grpc_request_duration_seconds"].Metric[0].Histogram
	if h.GetSampleCount() != 1 || h.GetSampleSum() != 0 {
		t.Fatal("unknown and valid zero timing conflated", h)
	}
	if m["axon_grpc_requests_without_timing_total"].Metric[0].Counter.GetValue() != 1 {
		t.Fatal("missing untimed observation")
	}
	for _, b := range h.Bucket {
		if b.GetCumulativeCount() != 1 {
			t.Fatal("valid zero missing from bucket", b)
		}
	}
}

func TestDynamicWorkloadsShareGlobalBound(t *testing.T) {
	e, _ := New(Config{MaxSeries: 2})
	call := grpcsem.Call{Method: "/echo/Unary", RequestDirection: "read", Status: 0}
	a := Workload{Service: "a", Namespace: "demo", Pod: "a-1"}
	b := Workload{Service: "b", Namespace: "demo", Pod: "b-1"}
	e.ObserveWorkload(call, a)
	e.ObserveWorkload(call, b)
	e.ObserveWorkload(call, a)
	e.ObserveWorkload(call, Workload{Service: "c", Namespace: "demo", Pod: "c-1"})
	m := gather(t, e)
	if len(m["axon_grpc_requests_total"].Metric) != 2 {
		t.Fatal("workload isolation failed")
	}
	for _, metric := range m["axon_grpc_requests_total"].Metric {
		labels := map[string]string{}
		for _, l := range metric.Label {
			labels[l.GetName()] = l.GetValue()
		}
		want := float64(1)
		if labels["service"] == "a" {
			want = 2
		}
		if metric.Counter.GetValue() != want {
			t.Fatal("cross-workload contamination", metric)
		}
	}
	if m["axon_metrics_dropped_observations_total"].Metric[0].Counter.GetValue() != 1 {
		t.Fatal("workload cardinality unbounded")
	}
}

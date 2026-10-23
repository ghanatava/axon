//go:build linux && amd64

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/ghanatava/axon/internal/discovery"
	grpcsem "github.com/ghanatava/axon/internal/grpc"
	"github.com/ghanatava/axon/internal/metrics"
	"github.com/ghanatava/axon/internal/session"
	"github.com/ghanatava/axon/internal/symbols"
	"github.com/ghanatava/axon/internal/tlscapture"
	"github.com/prometheus/client_golang/prometheus"
)

type telemetry struct {
	ExecStopped uint64          `json:"exec_stopped"`
	Runtime     session.Metrics `json:"runtime"`
	Capture     [6]uint64       `json:"capture"`
}
type discoverConfig struct {
	Node, Selector, Excluded, Proc, Object, Listen string
	Interval                                       time.Duration
	MaxTargets, MaxSeries                          int
}
type child struct {
	process  discovery.Process
	cmd      *exec.Cmd
	stopping bool
	ready    bool
	last     telemetry
}
type childEvent struct {
	kind      string
	child     *child
	call      grpcsem.Call
	telemetry telemetry
	err       error
	processes []discovery.Process
}
type nodeAgent struct {
	execs                                            prometheus.Counter
	config                                           discoverConfig
	kube                                             *discovery.Client
	exporter                                         *metrics.Exporter
	children                                         map[int]*child
	events                                           chan childEvent
	eligible                                         map[string]bool
	ready                                            atomic.Bool
	lastRefresh                                      time.Time
	totals                                           telemetry
	active, resident, discovered, deferred, apiReady prometheus.Gauge
	attached, detached, failures                     *prometheus.CounterVec
}

func runDiscovery(c discoverConfig) error {
	if c.Interval < 100*time.Millisecond || c.MaxTargets < 1 || c.MaxTargets > 64 {
		return fmt.Errorf("discovery interval must be >=100ms; max-targets must be 1..64")
	}
	kube, err := discovery.InCluster(c.Node, c.Selector, c.Excluded)
	if err != nil {
		return err
	}
	exporter, err := metrics.New(metrics.Config{Service: "axon", Namespace: "axon", Pod: os.Getenv("HOSTNAME"), MaxSeries: c.MaxSeries})
	if err != nil {
		return err
	}
	m := &nodeAgent{config: c, kube: kube, exporter: exporter, children: make(map[int]*child), events: make(chan childEvent, 128), eligible: make(map[string]bool)}
	gauge := func(name, help string) prometheus.Gauge {
		g := prometheus.NewGauge(prometheus.GaugeOpts{Name: "axon_discovery_" + name, Help: help, ConstLabels: prometheus.Labels{"node": c.Node}})
		exporter.Register(g)
		return g
	}
	counter := func(name, help string) *prometheus.CounterVec {
		v := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "axon_discovery_" + name, Help: help, ConstLabels: prometheus.Labels{"node": c.Node}}, []string{"reason"})
		exporter.Register(v)
		return v
	}
	m.active = gauge("active_targets", "Ready per-process capture workers.")
	m.resident = gauge("resident_workers", "All capture workers, including starting and stopping processes.")
	m.discovered = gauge("candidate_processes", "Processes matched to running node-local Kubernetes containers.")
	m.deferred = gauge("deferred_processes", "Candidates deferred because the capture-worker limit is reached (eligibility may be unknown).")
	m.apiReady = gauge("metadata_ready", "1 if the most recent node pod metadata refresh succeeded.")
	m.attached = counter("attachments_total", "Ready capture attachments.")
	m.detached = counter("detachments_total", "Stopped capture workers.")
	m.failures = counter("failures_total", "Discovery or worker failures.")
	m.execs = prometheus.NewCounter(prometheus.CounterOpts{Name: "axon_discovery_exec_transitions_total", Help: "Process exec transitions stopped by the in-kernel capture guard.", ConstLabels: prometheus.Labels{"node": c.Node}})
	exporter.Register(m.execs)
	mux := http.NewServeMux()
	mux.Handle("/metrics", exporter.Handler())
	mux.HandleFunc("/livez", func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, "ok") })
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if !m.ready.Load() {
			http.Error(w, "node metadata unavailable", 503)
			return
		}
		fmt.Fprintln(w, "ok")
	})
	if c.Listen == "" {
		c.Listen = ":9108"
	}
	closeServer, err := serveHandler(c.Listen, mux)
	if err != nil {
		return err
	}
	defer closeServer()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return m.run(ctx)
}
func (m *nodeAgent) run(ctx context.Context) error {
	tick := time.NewTicker(m.config.Interval)
	defer tick.Stop()
	refreshing := false
	refresh := func() {
		if refreshing {
			return
		}
		refreshing = true
		go func() {
			containers, err := m.kube.Containers(ctx)
			var processes []discovery.Process
			if err == nil {
				processes, err = discovery.Scan(m.config.Proc, containers)
			}
			select {
			case m.events <- childEvent{kind: "snapshot", processes: processes, err: err}:
			case <-ctx.Done():
			}
		}()
	}
	refresh()
	log.Printf("node discovery ready node=%s max_targets=%d", m.config.Node, m.config.MaxTargets)
	var runErr error
loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case <-tick.C:
			refresh()
			if !m.lastRefresh.IsZero() && time.Since(m.lastRefresh) > 30*time.Second {
				m.ready.Store(false)
				m.apiReady.Set(0)
				for _, c := range m.children {
					m.stop(c, "stale_metadata")
				}
			}
		case event := <-m.events:
			if event.kind == "snapshot" {
				refreshing = false
				if event.err != nil {
					m.ready.Store(false)
					m.apiReady.Set(0)
					m.failures.WithLabelValues("metadata").Inc()
					log.Printf("discovery refresh: %v", event.err)
					continue
				}
				m.lastRefresh = time.Now()
				m.ready.Store(true)
				m.apiReady.Set(1)
				m.reconcile(event.processes)
				continue
			}
			c := event.child
			if m.children[c.process.PID] != c {
				continue
			}
			switch event.kind {
			case "ready":
				if !c.ready && !c.stopping {
					c.ready = true
					m.attached.WithLabelValues("supported_go_tls").Inc()
					log.Printf("discovery attached pid=%d pod=%s/%s container=%s identity=%s", c.process.PID, c.process.Namespace, c.process.Pod, c.process.Container, c.process.Token())
				}
			case "call":
				if event.call.PID != uint32(c.process.PID) || event.call.ProcessStart/10_000_000 != c.process.StartTicks {
					m.failures.WithLabelValues("identity").Inc()
					m.stop(c, "identity_mismatch")
					continue
				}
				m.exporter.ObserveWorkload(event.call, metrics.Workload{Service: c.process.Service, Namespace: c.process.Namespace, Pod: c.process.Pod})
				record := struct {
					grpcsem.Call
					discovery.Workload
				}{event.call, c.process.Workload}
				if err := json.NewEncoder(os.Stdout).Encode(record); err != nil {
					runErr = err
					break loop
				}
			case "telemetry":
				m.updateTelemetry(c, event.telemetry)
			case "exit":
				identity, identityErr := discovery.ReadIdentity(m.config.Proc, c.process.PID)
				if event.err != nil && !c.stopping && identityErr == nil && identity == c.process.Identity {
					m.failures.WithLabelValues("worker").Inc()
					log.Printf("capture worker pid=%d exited: %v", c.process.PID, event.err)
				}
				m.detached.WithLabelValues("exited").Inc()
				delete(m.children, c.process.PID)
			case "decode_error":
				m.failures.WithLabelValues("worker_output").Inc()
				m.stop(c, "invalid_output")
			}
			m.publish()
		}
	}
	m.ready.Store(false)
	for _, c := range m.children {
		m.stop(c, "shutdown")
	}
	// Drain stdout and final counter snapshots before ending the node process.
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for len(m.children) > 0 {
		select {
		case e := <-m.events:
			if e.child == nil {
				continue
			}
			if e.kind == "telemetry" {
				m.updateTelemetry(e.child, e.telemetry)
			}
			if e.kind == "exit" {
				delete(m.children, e.child.process.PID)
			}
		case <-deadline.C:
			for _, c := range m.children {
				c.cmd.Process.Kill()
			}
			return runErr
		}
	}
	m.publish()
	return runErr
}
func (m *nodeAgent) reconcile(processes []discovery.Process) {
	m.discovered.Set(float64(len(processes)))
	desired := make(map[int]discovery.Process)
	for _, p := range processes {
		if p.PID != os.Getpid() {
			desired[p.PID] = p
		}
	}
	for pid, c := range m.children {
		p, ok := desired[pid]
		if !ok || p != c.process {
			m.stop(c, "process_changed")
		}
	}
	sort.Slice(processes, func(i, j int) bool { return processes[i].PID < processes[j].PID })
	deferred := 0
	for _, p := range processes {
		if p.PID == os.Getpid() || m.children[p.PID] != nil {
			continue
		}
		if len(m.children) >= m.config.MaxTargets {
			deferred++
			continue
		}
		cacheKey := fmt.Sprintf("%d:%d", p.Device, p.Inode)
		eligible, known := m.eligible[cacheKey]
		if !known {
			_, err := tlscapture.Inspect(p.Exe(m.config.Proc))
			eligible = err == nil
			if eligible {
				for _, name := range []string{"crypto/tls.(*Conn).Read", "crypto/tls.(*Conn).Write", "crypto/tls.(*Conn).Close"} {
					if _, err := symbols.Resolve(p.Exe(m.config.Proc), name); err != nil {
						eligible = false
						break
					}
				}
			}
			if len(m.eligible) >= 256 {
				clear(m.eligible)
			}
			m.eligible[cacheKey] = eligible
		}
		if !eligible {
			continue
		}
		if err := m.start(p); err != nil {
			m.failures.WithLabelValues("start").Inc()
			log.Printf("start capture pid=%d: %v", p.PID, err)
		}
	}
	m.deferred.Set(float64(deferred))
	m.publish()
}
func (m *nodeAgent) start(p discovery.Process) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(executable, "-bin", p.Exe(m.config.Proc), "-pid", fmt.Sprint(p.PID), "-expected-process", p.Token(), "-proc-root", m.config.Proc, "-bpf", m.config.Object, "-mode", "grpc", "-supervised", "-ring-bytes", "1048576", "-max-connections", "128", "-buffer-bytes", "8388608", "-idle-timeout", "30s")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		stdout.Close()
		return err
	}
	if err := cmd.Start(); err != nil {
		stdout.Close()
		stderr.Close()
		return err
	}
	c := &child{process: p, cmd: cmd}
	m.children[p.PID] = c
	var readers sync.WaitGroup
	readers.Add(2)
	go func() {
		defer readers.Done()
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 4096), 32<<20)
		for scanner.Scan() {
			var packet struct {
				grpcsem.Call
				Telemetry *telemetry `json:"axon_telemetry"`
			}
			if err := json.Unmarshal(scanner.Bytes(), &packet); err != nil {
				m.events <- childEvent{kind: "decode_error", child: c, err: err}
				return
			}
			if packet.Telemetry != nil {
				m.events <- childEvent{kind: "telemetry", child: c, telemetry: *packet.Telemetry}
			} else {
				m.events <- childEvent{kind: "call", child: c, call: packet.Call}
			}
		}
		if err := scanner.Err(); err != nil {
			m.events <- childEvent{kind: "decode_error", child: c, err: err}
		}
	}()
	go func() {
		defer readers.Done()
		scanner := bufio.NewScanner(stderr)
		scanner.Buffer(make([]byte, 4096), 64<<10)
		for scanner.Scan() {
			text := scanner.Text()
			log.Printf("capture pid=%d %s", p.PID, text)
			if strings.Contains(text, "capture ready") {
				m.events <- childEvent{kind: "ready", child: c}
			}
		}
	}()
	go func() { readers.Wait(); m.events <- childEvent{kind: "exit", child: c, err: cmd.Wait()} }()
	return nil
}
func (m *nodeAgent) stop(c *child, reason string) {
	if c.stopping {
		return
	}
	c.stopping = true
	c.cmd.Process.Signal(syscall.SIGTERM)
	log.Printf("discovery detaching pid=%d reason=%s", c.process.PID, reason)
	go func() { time.Sleep(3 * time.Second); c.cmd.Process.Kill() }()
}
func delta(n, old uint64) uint64 {
	if n < old {
		return n
	}
	return n - old
}
func (m *nodeAgent) updateTelemetry(c *child, n telemetry) {
	old := c.last
	if n.ExecStopped > 0 && old.ExecStopped == 0 {
		if m.execs != nil {
			m.execs.Inc()
		}
		log.Printf("discovery kernel exec guard stopped pid=%d", c.process.PID)
	}
	m.totals.Runtime.Opened += delta(n.Runtime.Opened, old.Runtime.Opened)
	m.totals.Runtime.Closed += delta(n.Runtime.Closed, old.Runtime.Closed)
	m.totals.Runtime.Expired += delta(n.Runtime.Expired, old.Runtime.Expired)
	m.totals.Runtime.Evicted += delta(n.Runtime.Evicted, old.Runtime.Evicted)
	m.totals.Runtime.Invalidated += delta(n.Runtime.Invalidated, old.Runtime.Invalidated)
	m.totals.Runtime.DiscardedEvents += delta(n.Runtime.DiscardedEvents, old.Runtime.DiscardedEvents)
	m.totals.Runtime.Incomplete += delta(n.Runtime.Incomplete, old.Runtime.Incomplete)
	for i := range n.Capture {
		m.totals.Capture[i] += delta(n.Capture[i], old.Capture[i])
	}
	c.last = n
}
func (m *nodeAgent) publish() {
	snapshot := m.totals.Runtime
	ready := 0
	for _, c := range m.children {
		snapshot.Active += c.last.Runtime.Active
		snapshot.RetainedBytes += c.last.Runtime.RetainedBytes
		if c.ready && !c.stopping {
			ready++
		}
	}
	m.active.Set(float64(ready))
	m.resident.Set(float64(len(m.children)))
	m.exporter.UpdateRuntime(snapshot)
	m.exporter.UpdateCapture(m.totals.Capture)
}

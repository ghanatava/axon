//go:build linux && amd64

package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/ringbuf"
	"github.com/cilium/ebpf/rlimit"
	"github.com/ghanatava/axon/internal/discovery"
	"github.com/ghanatava/axon/internal/metrics"
	"github.com/ghanatava/axon/internal/session"
	"github.com/ghanatava/axon/internal/symbols"
	"github.com/ghanatava/axon/internal/tlscapture"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
func run() (runErr error) {
	bin := flag.String("bin", "testtargets/client/client", "target Go ELF binary (supports -s -w)")
	object := flag.String("bpf", "build/tls_capture.bpf.o", "compiled TLS capture BPF object")
	pid := flag.Int("pid", 0, "target PID (0 attaches to all processes using binary)")
	mode := flag.String("mode", "raw", "output mode: raw, h2 (JSON frames), grpc (JSON calls)")
	replay := flag.String("replay", "", "read JSONL TLS events without BPF/root")
	idle := flag.Duration("idle-timeout", 2*time.Minute, "expire inactive capture sessions (later bytes need a new connection)")
	maxConnections := flag.Int("max-connections", 1024, "maximum retained connection incarnations")
	bufferBytes := flag.Int("buffer-bytes", 128<<20, "retained parser buffer budget")
	ringBytes := flag.Uint("ring-bytes", 1<<24, "BPF ring size in bytes (power of two, page aligned)")
	metricsListen := flag.String("metrics-listen", "", "enable Prometheus HTTP endpoint, e.g. 127.0.0.1:9108 (grpc mode)")
	metricsOutput := flag.String("metrics-output", "", "write final Prometheus snapshot to file (grpc mode)")
	metricsService := flag.String("service", os.Getenv("AXON_SERVICE"), "target service label (AXON_SERVICE)")
	metricsNamespace := flag.String("namespace", os.Getenv("AXON_NAMESPACE"), "target namespace label (AXON_NAMESPACE)")
	metricsPod := flag.String("pod", os.Getenv("AXON_POD"), "target pod label (AXON_POD)")
	metricsMaxSeries := flag.Int("metrics-max-series", 1024, "maximum metric method/role/status groups")
	discover := flag.Bool("discover", false, "discover Kubernetes Go TLS processes on this node")
	node := flag.String("node-name", os.Getenv("NODE_NAME"), "Kubernetes node name")
	selector := flag.String("pod-selector", "", "optional Kubernetes label selector")
	excluded := flag.String("exclude-namespaces", "kube-system,kube-public,kube-node-lease,axon", "namespaces excluded from discovery")
	procRoot := flag.String("proc-root", "/proc", "node proc filesystem")
	interval := flag.Duration("discovery-interval", 2*time.Second, "process and pod refresh interval")
	maxTargets := flag.Int("max-targets", 4, "maximum simultaneous per-process capture workers")
	expected := flag.String("expected-process", "", "internal: expected process start/stack/device/inode identity")
	supervised := flag.Bool("supervised", false, "internal: emit periodic capture telemetry on stdout")
	flag.Parse()
	if *supervised && (*mode != "grpc" || *expected == "") {
		return fmt.Errorf("supervised capture requires grpc mode and expected-process identity")
	}
	if *discover {
		if *replay != "" || *pid != 0 || *metricsOutput != "" {
			return fmt.Errorf("discovery cannot combine with replay, pid or metrics-output")
		}
		return runDiscovery(discoverConfig{Node: *node, Selector: *selector, Excluded: *excluded, Proc: *procRoot, Object: *object, Listen: *metricsListen, Interval: *interval, MaxTargets: *maxTargets, MaxSeries: *metricsMaxSeries})
	}
	processor, err := newProcessorConfig(*mode, os.Stdout, session.Config{IdleTimeout: *idle, MaxConnections: *maxConnections, MaxBufferedBytes: *bufferBytes})
	if err != nil {
		return err
	}
	if *metricsListen != "" || *metricsOutput != "" {
		if *mode != "grpc" {
			return fmt.Errorf("metrics requires -mode grpc")
		}
		exporter, err := metrics.New(metrics.Config{Service: *metricsService, Namespace: *metricsNamespace, Pod: *metricsPod, MaxSeries: *metricsMaxSeries})
		if err != nil {
			return err
		}
		processor.metrics = exporter
		if *metricsOutput != "" {
			// Open early so an unwritable destination fails before capture begins.
			file, err := os.Create(*metricsOutput)
			if err != nil {
				return err
			}
			defer file.Close()
			defer func() {
				if err := exporter.Write(file); err != nil {
					if runErr == nil {
						runErr = fmt.Errorf("metrics snapshot: %w", err)
					} else {
						log.Printf("metrics snapshot: %v", err)
					}
				}
			}()
		}
		if *metricsListen != "" {
			closeServer, err := serveMetrics(*metricsListen, exporter)
			if err != nil {
				return err
			}
			defer closeServer()
		}
	}
	defer func() {
		if processor.runtime != nil {
			data, _ := json.Marshal(processor.runtime.Metrics())
			log.Printf("runtime_stats %s", data)
		}
	}()
	if *replay != "" {
		return replayEvents(*replay, processor)
	}
	if *pid < 0 {
		return fmt.Errorf("pid must be nonnegative")
	}
	var verifyProcess func() error
	if *expected != "" {
		if *pid <= 0 {
			return fmt.Errorf("expected-process requires a PID")
		}
		verifyProcess = func() error {
			i, err := discovery.ReadIdentity(*procRoot, *pid)
			if err != nil {
				return err
			}
			if i.Token() != *expected {
				return fmt.Errorf("target process identity changed")
			}
			return nil
		}
		if err := verifyProcess(); err != nil {
			return err
		}
		pinned, err := os.Open(*bin)
		if err != nil {
			return err
		}
		defer pinned.Close()
		info, err := pinned.Stat()
		if err != nil {
			return err
		}
		st := info.Sys().(*syscall.Stat_t)
		parts := strings.Split(*expected, ":")
		if len(parts) != 4 || fmt.Sprintf("%d:%d", st.Dev, st.Ino) != strings.Join(parts[2:], ":") {
			return fmt.Errorf("pinned executable identity changed")
		}
		*bin = fmt.Sprintf("/proc/self/fd/%d", pinned.Fd())
		if err := verifyProcess(); err != nil {
			return err
		}
	}
	target, err := tlscapture.Inspect(*bin)
	if err != nil {
		return err
	}
	log.Printf("target=%s register_abi=%v goid_offset=%d layout=%s", target.Version, target.RegisterABI, target.GoIDOffset, target.LayoutSource)
	if err := rlimit.RemoveMemlock(); err != nil {
		return fmt.Errorf("memlock: %w", err)
	}
	spec, err := ebpf.LoadCollectionSpec(*object)
	if err != nil {
		return err
	}
	abi := uint32(0)
	if target.RegisterABI {
		abi = 1
	}
	var pidNamespace syscall.Stat_t
	if err := syscall.Stat("/proc/self/ns/pid", &pidNamespace); err != nil {
		return err
	}
	for name, value := range map[string]interface{}{"register_abi": abi, "goid_offset": target.GoIDOffset, "capture_chunks": uint32(boolInt(*mode != "raw")), "target_pid": uint32(boolInt(*supervised) * (*pid)), "pidns_ino": pidNamespace.Ino} {
		variable := spec.Variables[name]
		if variable == nil {
			return fmt.Errorf("BPF variable %s missing", name)
		}
		if err := variable.Set(value); err != nil {
			return err
		}
	}
	if *ringBytes < uint(os.Getpagesize()) || *ringBytes > 1<<30 || *ringBytes&(*ringBytes-1) != 0 {
		return fmt.Errorf("ring-bytes must be a page-aligned power of two between one page and 1GiB")
	}
	spec.Maps["events"].MaxEntries = uint32(*ringBytes)
	if *supervised {
		spec.Maps["connections"].MaxEntries = 4096
		spec.Maps["calls"].MaxEntries = 4096
	}
	collection, err := ebpf.NewCollection(spec)
	if err != nil {
		return fmt.Errorf("load BPF (requires root/CAP_BPF and CAP_PERFMON): %w", err)
	}
	defer collection.Close()
	ex, err := link.OpenExecutable(*bin)
	if err != nil {
		return err
	}
	var links []link.Link
	defer func() {
		for _, l := range links {
			l.Close()
		}
	}()
	if *supervised {
		execLink, err := link.Tracepoint("sched", "sched_process_exec", collection.Programs["target_exec"], nil)
		if err != nil {
			return fmt.Errorf("exec guard: %w", err)
		}
		links = append(links, execLink)
	}
	for direction, name := range []string{"crypto/tls.(*Conn).Read", "crypto/tls.(*Conn).Write"} {
		sym, err := symbols.Resolve(*bin, name)
		if err != nil {
			return err
		}
		rets, err := symbols.RetSites(*bin, name)
		if err != nil {
			return err
		}
		offsets := append([]uint64{0}, rets...)
		for i, off := range offsets {
			program := collection.Programs["tls_exit"]
			if i == 0 {
				program = collection.Programs["tls_enter"]
			}
			l, err := ex.Uprobe("", program, &link.UprobeOptions{Address: sym.FileOffset, Offset: off, PID: *pid, Cookie: uint64(direction)})
			if err != nil {
				return fmt.Errorf("attach %s+%#x: %w", name, off, err)
			}
			links = append(links, l)
		}
		log.Printf("attached %s entry + %d RET sites", name, len(rets))
	}
	closeSymbol, err := symbols.Resolve(*bin, "crypto/tls.(*Conn).Close")
	if err != nil {
		return err
	}
	closeLink, err := ex.Uprobe("", collection.Programs["tls_close"], &link.UprobeOptions{Address: closeSymbol.FileOffset, PID: *pid})
	if err != nil {
		return fmt.Errorf("attach TLS Close: %w", err)
	}
	links = append(links, closeLink)
	log.Print("attached crypto/tls.(*Conn).Close entry")
	monitor := newLossMonitor(collection, processor)
	monitor.supervised = *supervised
	monitor.verifyProcess = verifyProcess
	if *supervised {
		defer func() {
			var counts [6]uint64
			for i := range counts {
				key := uint32(i)
				if err := collection.Maps["stats"].Lookup(&key, &counts[i]); err != nil {
					log.Printf("final telemetry: %v", err)
					return
				}
			}
			var execStopped uint64
			key := uint32(0)
			if err := collection.Maps["exec_stop"].Lookup(&key, &execStopped); err != nil {
				log.Printf("final exec telemetry: %v", err)
				return
			}
			if err := processor.json.Encode(struct {
				Telemetry telemetry `json:"axon_telemetry"`
			}{telemetry{Runtime: processor.runtime.Metrics(), Capture: counts, ExecStopped: execStopped}}); err != nil {
				log.Printf("final telemetry: %v", err)
			}
		}()
	}
	reader, err := ringbuf.NewReader(collection.Maps["events"])
	if err != nil {
		return err
	}
	defer reader.Close()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(stop)
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-stop:
			reader.Close()
		case <-done:
		}
	}()
	if verifyProcess != nil {
		if err := verifyProcess(); err != nil {
			return err
		}
	}
	log.Print("capture ready")
	for {
		reader.SetDeadline(time.Now().Add(250 * time.Millisecond))
		record, err := reader.Read()
		if checkErr := monitor.Check(nil); checkErr != nil {
			return checkErr
		}
		if errors.Is(err, os.ErrDeadlineExceeded) {
			continue
		}
		if errors.Is(err, ringbuf.ErrClosed) {
			break
		}
		if err != nil {
			return err
		}
		event, err := tlscapture.Decode(record.RawSample)
		if err != nil {
			return err
		}
		if checkErr := monitor.Check(&event); checkErr != nil {
			return checkErr
		}
		if err := processor.Process(event); err != nil {
			return err
		}
	}
	var finalCapture [6]uint64
	for index, label := range []string{"memory/identity errors", "map errors", "ring drops", "truncated calls", "overlapping calls", "unattributed errors"} {
		key := uint32(index)
		var value uint64
		if err := collection.Maps["stats"].Lookup(&key, &value); err != nil {
			return err
		}
		finalCapture[index] = value
		log.Printf("%s: %d", label, value)
	}
	if processor.metrics != nil {
		processor.metrics.UpdateCapture(finalCapture)
		processor.metrics.UpdateRuntime(processor.runtime.Metrics())
	}
	if *supervised {
		if err := processor.json.Encode(struct {
			Telemetry telemetry `json:"axon_telemetry"`
		}{telemetry{Runtime: processor.runtime.Metrics(), Capture: finalCapture}}); err != nil {
			return err
		}
	}
	return processor.Finish()
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

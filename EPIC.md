# Axon - Implementation Epic (Rust Edition)

**Project Goal:** Build a production-quality eBPF-based L7 HTTP/QUIC observability and network policy engine for Kubernetes using Rust + C

**Timeline:** 10-14 weeks (part-time, ~15-20 hours/week)

**Current Phase:** 🚀 Phase 1 - Core eBPF Development

**Architecture:** Polyglot approach - C for eBPF programs, Rust for userspace (agent + operator)

---

## Table of Contents
- [Overview](#overview)
- [Why Rust + C?](#why-rust--c)
- [Success Criteria](#success-criteria)
- [Phase 1: Core eBPF (C)](#phase-1-core-ebpf-development-c)
- [Phase 2: Rust Agent + quiche](#phase-2-rust-agent-development--quiche-integration)
- [Phase 3: Kubernetes Integration](#phase-3-kubernetes-integration-kube-rs)
- [Phase 4: Policy Enforcement](#phase-4-policy-enforcement)
- [Phase 5: Metrics & Visualization](#phase-5-metrics--visualization)
- [Phase 6: Production Hardening](#phase-6-production-hardening)
- [Progress Tracking](#progress-tracking)

---

## Overview

This epic tracks the complete development of Axon from initial eBPF "hello world" to a production-ready Kubernetes operator. Each phase builds on the previous, with clear milestones and deliverables.

### Development Philosophy
1. **Polyglot by design** - C for kernel (eBPF), Rust for userspace (safety + performance)
2. **Start minimal** - Get something working end-to-end quickly
3. **Iterate and expand** - Add features incrementally
4. **Memory safety first** - Leverage Rust's guarantees to avoid leaks
5. **Test continuously** - Don't skip validation
6. **Document as you go** - Future you will thank present you

---

## Why Rust + C?

### The Polyglot Advantage

**C for eBPF Programs:**
- ✅ Kernel verifier expects C (mature, stable)
- ✅ libbpf ecosystem is C-native
- ✅ All eBPF examples and docs use C
- ✅ Direct kernel structure access
- ⚠️ Memory safety guaranteed by verifier (sandboxed)

**Rust for Userspace:**
- ✅ Memory safety without GC (no leaks, no use-after-free)
- ✅ Fearless concurrency (data race prevention)
- ✅ Zero-cost abstractions
- ✅ quiche integration (native Rust QUIC/HTTP3)
- ✅ Modern error handling (Result types)
- ✅ No runtime overhead vs C/C++
- ✅ Growing ecosystem: Aya, libbpf-rs, kube-rs

**Best of Both Worlds:**
```
┌────────────────────────────────────┐
│     Kernel Space (C + eBPF)        │
│  • Verified by kernel              │
│  • Memory safe by design           │
│  • High performance                │
└────────────────┬───────────────────┘
                 │ Ring Buffer
┌────────────────▼───────────────────┐
│   User Space (Rust)                │
│  • Memory safe (ownership)         │
│  • No GC pauses                    │
│  • Fearless concurrency            │
│  • quiche for QUIC                 │
└────────────────────────────────────┘
```

### Key Libraries

**eBPF (C):**
- libbpf (kernel loading, CO-RE)
- vmlinux.h (kernel types)

**Rust:**
- `aya` or `libbpf-rs` (eBPF program loading)
- `quiche` (Cloudflare QUIC/HTTP3)
- `kube` (Kubernetes client)
- `prometheus` (metrics)
- `tokio` (async runtime)
- `tracing` (structured logging)

---

## Success Criteria

**Minimum Viable Product (MVP):**
- ✅ eBPF programs successfully parse HTTP/1.1, HTTP/2, and HTTP/3 (QUIC)
- ✅ Rust agent loads eBPF and processes events
- ✅ quiche integration for QUIC parsing
- ✅ Kubernetes operator manages deployment (kube-rs)
- ✅ Prometheus metrics exported
- ✅ Basic Grafana dashboard showing HTTP/QUIC metrics
- ✅ L7 network policies can be created and enforced
- ✅ Works on 3+ different Linux kernel versions (CO-RE)
- ✅ Zero memory leaks (validated with valgrind/miri)

**Stretch Goals:**
- Service dependency graph visualization
- Distributed tracing correlation (OpenTelemetry)
- HTTP/3 connection migration tracking
- Advanced anomaly detection with ML

---

## Phase 1: Core eBPF Development (C)

**Duration:** Week 1-3 (3 weeks)  
**Goal:** Build and validate eBPF programs that can hook into socket operations and parse HTTP/QUIC traffic

### 1.1: Development Environment Setup
**Duration:** 2-3 days  
**Status:** ⬜ Not Started

**Tasks:**
- [ ] Set up Linux development VM (Ubuntu 22.04 or Fedora 38+)
  - Verify kernel version (5.10+): `uname -r`
  - Check BTF availability: `ls /sys/kernel/btf/vmlinux`
- [ ] Install C/eBPF dependencies
  ```bash
  # Ubuntu/Debian
  sudo apt-get install -y \
      clang-14 llvm-14 libbpf-dev \
      linux-headers-$(uname -r) \
      build-essential git \
      pkg-config libssl-dev
  ```
- [ ] Install Rust toolchain
  ```bash
  curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh
  rustup default stable
  rustup component add rustfmt clippy
  ```
- [ ] Install bpftool for debugging
  ```bash
  sudo apt-get install linux-tools-common linux-tools-$(uname -r)
  ```
- [ ] Set up project directory structure
  ```
  axon/
  ├── Cargo.toml          # Workspace
  ├── ebpf/               # C eBPF programs
  ├── axon-agent/         # Rust agent
  ├── axon-operator/      # Rust operator
  ├── examples/           # Sample configs
  └── docs/               # Documentation
  ```
- [ ] Initialize Cargo workspace
  ```bash
  cargo init --name axon
  cargo new --lib axon-agent
  cargo new --lib axon-operator
  ```
- [ ] Create Makefile for eBPF builds

**Deliverable:** Working build environment with all dependencies

---

### 1.2: eBPF "Hello World" - TCP Connection Tracing
**Duration:** 2-3 days  
**Status:** ⬜ Not Started

**Goal:** Prove we can load eBPF programs and capture kernel events

**Tasks:**
- [ ] Create `ebpf/` directory structure
- [ ] Write basic eBPF program: `tcp_connect_trace.bpf.c`
  ```c
  #include <vmlinux.h>
  #include <bpf/bpf_helpers.h>
  #include <bpf/bpf_tracing.h>
  
  SEC("kprobe/tcp_connect")
  int trace_tcp_connect(struct pt_regs *ctx) {
      // Capture connection events
      bpf_printk("TCP connect traced!");
      return 0;
  }
  
  char LICENSE[] SEC("license") = "GPL";
  ```
- [ ] Create corresponding header file: `tcp_connect_trace.h`
- [ ] Write Makefile for eBPF compilation
  ```makefile
  clang -O2 -g -target bpf -D__TARGET_ARCH_x86_64 \
        -c tcp_connect_trace.bpf.c -o tcp_connect_trace.bpf.o
  ```
- [ ] Generate vmlinux.h: `bpftool btf dump file /sys/kernel/btf/vmlinux format c > vmlinux.h`
- [ ] Load and test with bpftool
- [ ] Verify events: `sudo cat /sys/kernel/debug/tracing/trace_pipe`

**Success Criteria:**
- eBPF program loads without verifier errors
- Can see TCP connect events when running `curl google.com`
- Program survives basic stress testing

**Deliverable:** Working TCP connection tracer

---

### 1.3: Socket-Level HTTP Request Capture
**Duration:** 4-5 days  
**Status:** ⬜ Not Started

**Goal:** Capture raw HTTP data at socket layer

**Tasks:**
- [ ] Research best socket hooks for HTTP:
  - `socket_filter` (recommended)
  - `kprobe/tcp_sendmsg` and `kprobe/tcp_recvmsg`
  - `sockops` (socket operations)
- [ ] Implement `http_socket_filter.bpf.c`
  - Hook into socket operations
  - Filter for HTTP ports (80, 8080, 3000, 8000)
  - Capture first 512 bytes of payload
- [ ] Set up per-CPU ring buffer
  ```c
  struct {
      __uint(type, BPF_MAP_TYPE_RINGBUF);
      __uint(max_entries, 256 * 1024);
  } events SEC(".maps");
  ```
- [ ] Handle packet fragmentation strategy
- [ ] Test with simple HTTP server
  ```bash
  python3 -m http.server 8080
  curl http://localhost:8080/
  ```

**Success Criteria:**
- Captures HTTP request line: `GET /path HTTP/1.1`
- No kernel panics
- <5% CPU overhead

**Deliverable:** eBPF program capturing raw HTTP socket data

---

### 1.4: HTTP/1.1 Protocol Parsing in eBPF
**Duration:** 5-7 days  
**Status:** ⬜ Not Started

**Goal:** Parse HTTP/1.1 requests/responses in kernel space

**Tasks:**
- [ ] Design HTTP parser with eBPF constraints:
  - Bounded loops only (verifier requirement)
  - Max instruction limit (~1 million)
  - Keep complexity under 512 instructions per branch
- [ ] Implement HTTP/1.1 request parser:
  ```c
  struct http_request {
      u64 timestamp_ns;
      u32 pid;
      u32 src_ip;
      u32 dst_ip;
      u16 src_port;
      u16 dst_port;
      u8 method;        // GET=1, POST=2, etc.
      u16 status_code;  // Response only
      u32 latency_ns;
      char path[128];
      char host[64];
  };
  ```
- [ ] Parse essential fields:
  - [ ] HTTP method (GET, POST, PUT, DELETE, PATCH, HEAD, OPTIONS)
  - [ ] Path/URI
  - [ ] HTTP version
  - [ ] Host header
  - [ ] Content-Length
- [ ] Implement HTTP response parser:
  - [ ] Status code (200, 404, 500, etc.)
  - [ ] Content-Length
  - [ ] Calculate latency (request-response match)
- [ ] Add bounds checking for verifier
- [ ] Write to ring buffer

**Testing:**
- [ ] Test various HTTP clients (curl, wget, httpie)
- [ ] Test different HTTP methods
- [ ] Test long URLs (>128 chars, truncation)
- [ ] Test with chunked encoding
- [ ] Profile with `bpftool prog profile`

**Success Criteria:**
- Parses 95%+ of standard HTTP/1.1 requests
- Handles edge cases without crashes
- <1% CPU overhead per connection
- Passes eBPF verifier

**Deliverable:** Fully functional HTTP/1.1 parser in eBPF

---

### 1.5: QUIC/HTTP3 Preliminary Support
**Duration:** 4-5 days  
**Status:** ⬜ Not Started

**Goal:** Basic QUIC packet detection and metadata extraction

**Note:** Full QUIC parsing happens in Rust userspace with quiche. eBPF extracts minimal metadata.

**Tasks:**
- [ ] Research QUIC packet structure
  - UDP port 443 (typically)
  - QUIC header format (version, connection ID, packet number)
- [ ] Implement `quic_detector.bpf.c`
  - Hook: `kprobe/udp_sendmsg` or socket filter
  - Detect QUIC packets (header flags)
  - Extract connection ID (for tracking)
  - Mark packets for userspace processing
- [ ] Create QUIC event structure:
  ```c
  struct quic_event {
      u64 timestamp_ns;
      u32 src_ip;
      u32 dst_ip;
      u16 src_port;
      u16 dst_port;
      u64 connection_id;
      u8 packet_type;
      char payload[512];  // Pass to quiche
  };
  ```
- [ ] Test with QUIC server (nginx-quic or curl --http3)

**Success Criteria:**
- Detects QUIC traffic on UDP port 443
- Extracts connection ID correctly
- Minimal overhead (<1%)

**Deliverable:** QUIC packet detector that forwards to userspace

---

### 1.6: CO-RE (Compile Once, Run Everywhere) Support
**Duration:** 3-4 days  
**Status:** ⬜ Not Started

**Goal:** Make eBPF programs portable across kernel versions

**Tasks:**
- [ ] Convert programs to CO-RE style
  - [ ] Use `vmlinux.h` instead of kernel headers
  - [ ] Use `BPF_CORE_READ()` macros for field access
  - [ ] Add CO-RE relocations with `__builtin_preserve_access_index`
- [ ] Update Makefile for CO-RE compilation
  ```makefile
  clang -g -O2 -target bpf -D__TARGET_ARCH_x86 \
        -D__BPF_TRACING__ \
        -c http_parser.bpf.c -o http_parser.bpf.o
  ```
- [ ] Test on multiple kernel versions:
  - [ ] Kernel 5.10 (LTS)
  - [ ] Kernel 5.15 (LTS)
  - [ ] Kernel 6.1+ (latest LTS)

**Success Criteria:**
- Single compiled object works on 3+ kernel versions
- No runtime recompilation needed

**Deliverable:** CO-RE-enabled eBPF programs

---

### 1.7: Performance Benchmarking
**Duration:** 2-3 days  
**Status:** ⬜ Not Started

**Tasks:**
- [ ] Set up benchmark environment (nginx)
- [ ] Baseline without eBPF: `wrk -t4 -c100 -d30s http://localhost`
- [ ] Measure with eBPF attached
- [ ] Profile with `perf` and `bpftool prog profile`
- [ ] Optimize hot paths
- [ ] Target: <1% CPU overhead, <100ns per request

**Deliverable:** Performance report and optimizations

---

### Phase 1 Completion Checklist

- [ ] eBPF programs compile successfully with CO-RE
- [ ] Can hook into TCP/socket layer
- [ ] HTTP/1.1 requests and responses parsed correctly
- [ ] QUIC packets detected and forwarded
- [ ] CO-RE support verified on 3+ kernels
- [ ] Performance overhead <1%
- [ ] Code documented and clean
- [ ] eBPF verifier accepts all programs

**Next:** Move to Phase 2 (Rust Agent + quiche)

---

## Phase 2: Rust Agent Development + quiche Integration

**Duration:** Week 4-6 (3 weeks)  
**Goal:** Build Rust userspace agent that loads eBPF programs and processes events with quiche

### 2.1: Rust Agent Scaffolding
**Duration:** 2-3 days

**Tasks:**
- [ ] Create `axon-agent` crate
  ```bash
  cargo new --bin axon-agent
  cd axon-agent
  ```
- [ ] Add dependencies to `Cargo.toml`:
  ```toml
  [dependencies]
  aya = "0.12"              # or libbpf-rs = "0.22"
  tokio = { version = "1", features = ["full"] }
  quiche = "0.20"
  kube = { version = "0.87", features = ["runtime", "derive"] }
  k8s-openapi = { version = "0.20", features = ["v1_28"] }
  prometheus = "0.13"
  tracing = "0.1"
  tracing-subscriber = "0.3"
  anyhow = "1.0"
  thiserror = "1.0"
  ```
- [ ] Set up project structure:
  ```
  axon-agent/
  ├── src/
  │   ├── main.rs
  │   ├── ebpf/           # eBPF loading
  │   │   └── loader.rs
  │   ├── quic/           # quiche integration
  │   │   ├── parser.rs
  │   │   └── connection.rs
  │   ├── http/           # HTTP processing
  │   │   └── processor.rs
  │   ├── k8s/            # Kubernetes client
  │   │   └── metadata.rs
  │   ├── metrics/        # Prometheus
  │   │   └── exporter.rs
  │   └── lib.rs
  └── Cargo.toml
  ```
- [ ] Initialize tracing
- [ ] Basic CLI with `clap`

**Deliverable:** Rust agent skeleton compiles

---

### 2.2: eBPF Program Loading (Aya or libbpf-rs)
**Duration:** 3-4 days

**Decision:** Choose between Aya (pure Rust) or libbpf-rs (Rust bindings)

**Recommendation:** Start with **libbpf-rs** (more mature, better C interop)

**Tasks:**
- [ ] Implement eBPF loader in `src/ebpf/loader.rs`
- [ ] Load compiled .bpf.o files
- [ ] Attach to hooks (kprobes, socket filters)
- [ ] Open ring buffers
- [ ] Poll for events
- [ ] Graceful shutdown and cleanup

**Example:**
```rust
use libbpf_rs::{RingBufferBuilder, MapCore};

pub struct EbpfLoader {
    _skel: HttpParserSkel<'static>,
    rb: RingBuffer<'static>,
}

impl EbpfLoader {
    pub fn new() -> Result<Self> {
        let skel = HttpParserSkel::open()?;
        let skel = skel.load()?;
        let skel = skel.attach()?;
        
        let mut rb_builder = RingBufferBuilder::new();
        rb_builder.add(skel.maps().events(), handle_event)?;
        let rb = rb_builder.build()?;
        
        Ok(Self { _skel: skel, rb })
    }
    
    pub fn poll(&self) -> Result<()> {
        self.rb.poll(std::time::Duration::from_millis(100))
    }
}
```

**Deliverable:** Rust agent successfully loads eBPF programs

---

### 2.3: Ring Buffer Event Processing
**Duration:** 2-3 days

**Tasks:**
- [ ] Parse events from ring buffer
- [ ] Deserialize C structs to Rust structs
  ```rust
  #[repr(C)]
  struct HttpEvent {
      timestamp_ns: u64,
      pid: u32,
      src_ip: u32,
      dst_ip: u32,
      src_port: u16,
      dst_port: u16,
      method: u8,
      status_code: u16,
      latency_ns: u32,
      path: [u8; 128],
      host: [u8; 64],
  }
  ```
- [ ] Convert to Rust-native types
- [ ] Correlate requests with responses (by connection tuple)
- [ ] Calculate additional metrics
- [ ] Buffer and batch events for efficiency

**Deliverable:** Event processing pipeline

---

### 2.4: quiche Integration for QUIC/HTTP3
**Duration:** 5-7 days

**Goal:** Parse QUIC packets using Cloudflare's quiche library

**Tasks:**
- [ ] Set up quiche in `src/quic/`
- [ ] Receive QUIC events from eBPF
- [ ] Parse QUIC packets with quiche:
  ```rust
  use quiche;
  
  pub struct QuicHandler {
      config: quiche::Config,
      connections: HashMap<u64, quiche::Connection>,
  }
  
  impl QuicHandler {
      pub fn handle_packet(&mut self, payload: &[u8]) -> Result<()> {
          // Parse QUIC packet
          let hdr = quiche::Header::from_slice(payload, quiche::MAX_CONN_ID_LEN)?;
          
          // Get or create connection
          let conn = self.connections.entry(hdr.dcid)
              .or_insert_with(|| {
                  quiche::connect(None, &hdr.dcid, &self.config).unwrap()
              });
          
          // Process packet
          conn.recv(payload)?;
          
          // Extract HTTP/3 streams
          self.process_streams(conn)?;
          
          Ok(())
      }
      
      fn process_streams(&self, conn: &mut quiche::Connection) -> Result<()> {
          // Iterate over readable streams
          for stream_id in conn.readable() {
              let mut buf = [0; 65535];
              let (len, fin) = conn.stream_recv(stream_id, &mut buf)?;
              
              // Parse HTTP/3 frames
              self.parse_http3(&buf[..len])?;
          }
          Ok(())
      }
  }
  ```
- [ ] Extract HTTP/3 requests/responses
- [ ] Handle QUIC connection migration
- [ ] Track connection state

**Challenges:**
- QUIC connections are stateful
- Need to track connection IDs
- Handle packet loss and reordering

**Success Criteria:**
- Can parse HTTP/3 requests over QUIC
- Tracks connection state correctly
- <2% CPU overhead

**Deliverable:** Working QUIC/HTTP3 parser with quiche

---

### 2.5: Kubernetes Metadata Enrichment
**Duration:** 3-4 days

**Tasks:**
- [ ] Use `kube-rs` to watch pods
- [ ] Build IP → Pod mapping cache
  ```rust
  use kube::{Api, Client};
  use k8s_openapi::api::core::v1::Pod;
  
  pub struct K8sMetadata {
      client: Client,
      pod_cache: HashMap<IpAddr, PodInfo>,
  }
  
  impl K8sMetadata {
      pub async fn watch_pods(&mut self) -> Result<()> {
          let pods: Api<Pod> = Api::all(self.client.clone());
          let mut watcher = watcher(pods, Default::default());
          
          while let Some(event) = watcher.try_next().await? {
              match event {
                  Event::Applied(pod) => self.add_pod(pod),
                  Event::Deleted(pod) => self.remove_pod(pod),
                  _ => {}
              }
          }
          Ok(())
      }
      
      pub fn enrich_event(&self, event: &mut HttpEvent) {
          if let Some(pod) = self.pod_cache.get(&event.src_ip) {
              event.pod_name = Some(pod.name.clone());
              event.namespace = Some(pod.namespace.clone());
              event.labels = Some(pod.labels.clone());
          }
      }
  }
  ```
- [ ] Enrich events with:
  - Pod name
  - Namespace
  - Labels (for policy matching)
  - Service name
- [ ] Handle pod churn (updates, deletes)

**Deliverable:** K8s metadata enrichment working

---

### 2.6: Prometheus Metrics Export
**Duration:** 2-3 days

**Tasks:**
- [ ] Set up Prometheus exporter
  ```rust
  use prometheus::{Counter, Histogram, Registry};
  use warp::Filter;
  
  pub struct MetricsExporter {
      registry: Registry,
      http_requests_total: Counter,
      http_request_duration: Histogram,
  }
  
  impl MetricsExporter {
      pub fn record_request(&self, event: &HttpEvent) {
          self.http_requests_total
              .with_label_values(&[&event.method, &event.path, &event.status])
              .inc();
          
          self.http_request_duration
              .observe(event.latency_ns as f64 / 1_000_000.0);
      }
      
      pub async fn serve_metrics(self) {
          let metrics_route = warp::path("metrics")
              .map(move || {
                  let encoder = prometheus::TextEncoder::new();
                  let metrics = encoder.encode_to_string(&self.registry.gather()).unwrap();
                  warp::reply::html(metrics)
              });
          
          warp::serve(metrics_route).run(([0, 0, 0, 0], 9090)).await;
      }
  }
  ```
- [ ] Export basic metrics:
  - `http_requests_total{method, path, status, protocol}`
  - `http_request_duration_seconds{method, path, protocol}`
  - `http_request_size_bytes{protocol}`
  - `quic_connections_active`
  - `quic_migrations_total`

**Deliverable:** Metrics endpoint at `:9090/metrics`

---

### Phase 2 Completion Checklist

- [ ] Rust agent loads eBPF programs
- [ ] Events processed from ring buffers
- [ ] quiche integration working for QUIC/HTTP3
- [ ] K8s metadata enrichment functional
- [ ] Prometheus metrics exported
- [ ] No memory leaks (checked with valgrind)
- [ ] Agent runs as DaemonSet (tested in Kind)

**Next:** Move to Phase 3 (Kubernetes Integration)

---

## Phase 3: Kubernetes Integration (kube-rs)

**Duration:** Week 7-8 (2 weeks)  
**Goal:** Build Rust operator with CRDs for declarative configuration

### 3.1: Operator Scaffolding
**Duration:** 2-3 days

**Tasks:**
- [ ] Create `axon-operator` crate
- [ ] Add dependencies:
  ```toml
  [dependencies]
  kube = { version = "0.87", features = ["runtime", "derive", "client"] }
  k8s-openapi = { version = "0.20", features = ["v1_28"] }
  tokio = { version = "1", features = ["full"] }
  serde = { version = "1.0", features = ["derive"] }
  serde_json = "1.0"
  tracing = "0.1"
  futures = "0.3"
  ```
- [ ] Set up CRD definitions using `kube::CustomResource`

---

### 3.2: HTTPMonitor CRD
**Duration:** 2-3 days

**Tasks:**
- [ ] Define HTTPMonitor CRD:
  ```rust
  use kube::CustomResource;
  use schemars::JsonSchema;
  use serde::{Deserialize, Serialize};
  
  #[derive(CustomResource, Clone, Debug, Deserialize, Serialize, JsonSchema)]
  #[kube(
      group = "axon.dev",
      version = "v1alpha1",
      kind = "HTTPMonitor",
      namespaced
  )]
  pub struct HTTPMonitorSpec {
      pub namespaces: Vec<String>,
      pub protocols: Vec<Protocol>,
      pub sampling: f32,
      pub metrics: Vec<String>,
  }
  
  #[derive(Clone, Debug, Deserialize, Serialize, JsonSchema)]
  pub enum Protocol {
      Http1,
      Http2,
      Quic,
  }
  ```
- [ ] Generate CRD YAML: `cargo run --bin crd-gen`
- [ ] Apply to cluster: `kubectl apply -f crds/httpmonitor.yaml`

---

### 3.3: HTTPMonitor Controller
**Duration:** 4-5 days

**Tasks:**
- [ ] Implement controller with reconciliation loop
  ```rust
  use kube::runtime::controller::{Action, Controller};
  
  async fn reconcile(monitor: Arc<HTTPMonitor>, ctx: Arc<Context>) -> Result<Action> {
      // Generate DaemonSet spec from HTTPMonitor
      let daemonset = generate_daemonset(&monitor)?;
      
      // Apply DaemonSet to cluster
      let ds_api: Api<DaemonSet> = Api::namespaced(ctx.client.clone(), &monitor.namespace());
      ds_api.patch(&daemonset.name, &PatchParams::apply("axon"), &Patch::Apply(&daemonset)).await?;
      
      // Update status
      update_status(&monitor, ctx).await?;
      
      Ok(Action::requeue(Duration::from_secs(300)))
  }
  ```
- [ ] Watch HTTPMonitor resources
- [ ] Generate agent DaemonSet configuration
- [ ] Deploy/update agent DaemonSet
- [ ] Update CR status

**Deliverable:** HTTPMonitor CRD and controller

---

### 3.4: L7NetworkPolicy CRD (Stub)
**Duration:** 1-2 days

**Tasks:**
- [ ] Define L7NetworkPolicy CRD structure
- [ ] Basic controller scaffolding (full implementation in Phase 4)

---

### Phase 3 Completion Checklist

- [ ] Operator deploys to cluster
- [ ] HTTPMonitor CRD functional
- [ ] Controller watches and reconciles resources
- [ ] Agent DaemonSet deployed via operator
- [ ] Status updates working

**Next:** Move to Phase 4 (Policy Enforcement)

---

## Phase 4: Policy Enforcement

**Duration:** Week 9-10 (2 weeks)  
**Goal:** Implement L7 network policies enforced in eBPF

### 4.1: Policy eBPF Programs
**Duration:** 5-6 days

**Tasks:**
- [ ] Write `policy_engine.bpf.c`
- [ ] Use BPF hash maps for policy rules
- [ ] Match requests against policies
- [ ] Block/allow based on rules
- [ ] Return verdict to kernel

---

### 4.2: L7NetworkPolicy Controller
**Duration:** 4-5 days

**Tasks:**
- [ ] Implement full L7NetworkPolicy controller
- [ ] Compile policies into BPF map entries
- [ ] Update eBPF maps dynamically (from Rust)
- [ ] Test policy enforcement

---

### Phase 4 Completion Checklist

- [ ] L7 policies enforceable via CRDs
- [ ] Policies enforced in kernel
- [ ] Blocked requests don't reach app
- [ ] Dynamic policy updates work

---

## Phase 5: Metrics & Visualization

**Duration:** Week 11 (1 week)  
**Goal:** Production-quality dashboards

### 5.1: Enhanced Metrics
**Duration:** 2-3 days

**Tasks:**
- [ ] Add histograms for latency percentiles
- [ ] Service-level aggregations
- [ ] Cardinality limiting

---

### 5.2: Grafana Dashboards
**Duration:** 2-3 days

**Tasks:**
- [ ] Create dashboard JSON templates
- [ ] HTTP/QUIC overview panels
- [ ] Service map (optional)

---

## Phase 6: Production Hardening

**Duration:** Week 12-14 (2-3 weeks)

### 6.1: Testing
**Duration:** 4-5 days

**Tasks:**
- [ ] Unit tests with `cargo test`
- [ ] Integration tests in Kind
- [ ] Load testing (wrk)
- [ ] Memory leak checks (valgrind, miri)

---

### 6.2: Documentation
**Duration:** 3-4 days

**Tasks:**
- [ ] Installation guide
- [ ] Configuration reference
- [ ] Troubleshooting
- [ ] Architecture doc

---

### 6.3: CI/CD
**Duration:** 3-4 days

**Tasks:**
- [ ] GitHub Actions for Rust
- [ ] Cross-compilation for eBPF
- [ ] Container builds
- [ ] Release automation

---

## Progress Tracking

### Current Sprint: Phase 1.1 - Environment Setup
**Start Date:** [To be filled]  
**Target Completion:** [To be filled]

### Completed Milestones
- [x] Project planning and architecture design
- [x] README.md created (Rust edition)
- [x] EPIC.md created (Rust edition)

### Next Steps
1. Set up Rust + C development environment (Phase 1.1)
2. Write first eBPF program (Phase 1.2)
3. Begin HTTP parsing (Phase 1.4)

---

## Resources

**Rust + eBPF:**
- [Aya Book](https://aya-rs.dev/book/)
- [libbpf-rs Documentation](https://github.com/libbpf/libbpf-rs)
- [Rust for Linux](https://rust-for-linux.com/)

**quiche:**
- [quiche Documentation](https://docs.rs/quiche/)
- [Cloudflare Blog on QUIC](https://blog.cloudflare.com/tag/quic/)

**eBPF:**
- [BPF and XDP Reference](https://docs.cilium.io/en/stable/bpf/)
- [Andrii Nakryiko's Blog](https://nakryiko.com/)
- [eBPF.io](https://ebpf.io/)

**Kubernetes Operators (Rust):**
- [kube-rs Documentation](https://kube.rs/)
- [kube-rs Examples](https://github.com/kube-rs/kube/tree/main/examples)

---

**Last Updated:** [Auto-update as you progress]

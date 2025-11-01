# Axon - Implementation Epic

**Project Goal:** Build a production-quality eBPF-based L7 HTTP observability and network policy engine for Kubernetes

**Timeline:** 8-12 weeks (part-time, ~15-20 hours/week)

**Current Phase:** 🚀 Phase 1 - Core eBPF Development

---

## Table of Contents
- [Overview](#overview)
- [Success Criteria](#success-criteria)
- [Phase 1: Core eBPF](#phase-1-core-ebpf-development)
- [Phase 2: Agent Development](#phase-2-agent-development)
- [Phase 3: Kubernetes Integration](#phase-3-kubernetes-integration)
- [Phase 4: Policy Enforcement](#phase-4-policy-enforcement)
- [Phase 5: Metrics & Visualization](#phase-5-metrics--visualization)
- [Phase 6: Production Hardening](#phase-6-production-hardening)
- [Progress Tracking](#progress-tracking)

---

## Overview

This epic tracks the complete development of Axon from initial eBPF "hello world" to a production-ready Kubernetes operator. Each phase builds on the previous, with clear milestones and deliverables.

### Development Philosophy
1. **Start minimal** - Get something working end-to-end quickly
2. **Iterate and expand** - Add features incrementally
3. **Test continuously** - Don't skip validation
4. **Document as you go** - Future you will thank present you

---

## Success Criteria

**Minimum Viable Product (MVP):**
- ✅ eBPF programs successfully parse HTTP requests/responses
- ✅ Agent collects and processes eBPF data
- ✅ Kubernetes operator manages deployment
- ✅ Prometheus metrics exported
- ✅ Basic Grafana dashboard showing HTTP metrics
- ✅ L7 network policies can be created and enforced
- ✅ Works on 3+ different Linux kernel versions (CO-RE)

**Stretch Goals:**
- Service dependency graph visualization
- Distributed tracing correlation
- Advanced anomaly detection
- Multi-protocol support (gRPC, etc.)

---

## Phase 1: Core eBPF Development

**Duration:** Week 1-3 (3 weeks)  
**Goal:** Build and validate eBPF programs that can hook into socket operations and parse HTTP traffic

### 1.1: Development Environment Setup
**Duration:** 2-3 days  
**Status:** ⬜ Not Started

**Tasks:**
- [ ] Set up Linux development VM (Ubuntu 22.04 or Fedora 38+)
  - Verify kernel version (5.10+)
  - Check BTF availability: `ls /sys/kernel/btf/vmlinux`
- [ ] Install build dependencies
  ```bash
  sudo apt-get install -y clang-14 llvm-14 libbpf-dev \
      linux-headers-$(uname -r) build-essential git
  ```
- [ ] Install bpftool for debugging
- [ ] Set up project directory structure
  ```
  axon/
  ├── ebpf/           # eBPF C programs
  ├── agent/          # Go agent
  ├── operator/       # Kubernetes operator
  ├── examples/       # Sample configs
  └── docs/           # Documentation
  ```
- [ ] Initialize Git repository
- [ ] Create basic Makefile

**Deliverable:** Working build environment with all dependencies

---

### 1.2: eBPF "Hello World" - TCP Connection Tracing
**Duration:** 2-3 days  
**Status:** ⬜ Not Started

**Goal:** Prove we can load eBPF programs and capture kernel events

**Tasks:**
- [ ] Write basic eBPF program: `tcp_connect_trace.bpf.c`
  - Hook: `kprobe/tcp_connect`
  - Capture: source IP, destination IP, destination port
  - Store in BPF map or ring buffer
- [ ] Create corresponding header file: `tcp_connect_trace.h`
- [ ] Write minimal userspace loader in C
- [ ] Compile using clang
  ```bash
  clang -O2 -g -target bpf -c tcp_connect_trace.bpf.c -o tcp_connect_trace.bpf.o
  ```
- [ ] Load and test with bpftool
- [ ] Verify events are captured: `curl google.com` should trigger event

**Success Criteria:**
- eBPF program loads without errors
- Can see TCP connect events in real-time
- Program survives basic stress testing (100+ connections)

**Deliverable:** Working TCP connection tracer with proof-of-concept output

---

### 1.3: Socket-Level HTTP Request Capture
**Duration:** 4-5 days  
**Status:** ⬜ Not Started

**Goal:** Capture raw HTTP data at socket layer

**Tasks:**
- [ ] Research socket hooks:
  - `sockops` (socket operations)
  - `sock_sendmsg` / `sock_recvmsg`
  - `socket_filter` (best for HTTP)
- [ ] Implement `http_socket_filter.bpf.c`
  - Hook: `socket_filter` or `kprobe/tcp_sendmsg`
  - Capture first N bytes of socket buffer
  - Filter for ports 80, 8080, 3000 (HTTP)
- [ ] Store captured data in per-CPU ring buffer
- [ ] Handle multi-packet HTTP requests (fragmentation)
- [ ] Test with simple HTTP server:
  ```bash
  python3 -m http.server 8080
  curl http://localhost:8080/
  ```

**Challenges to Solve:**
- HTTP spans multiple packets - need reassembly strategy
- Ring buffer sizing (start with 256KB per CPU)
- Performance impact measurement

**Success Criteria:**
- Can capture HTTP request line: `GET /path HTTP/1.1`
- No kernel panics or crashes
- <5% CPU overhead on test workload

**Deliverable:** eBPF program capturing raw HTTP socket data

---

### 1.4: HTTP Protocol Parsing in eBPF
**Duration:** 5-7 days  
**Status:** ⬜ Not Started

**Goal:** Parse HTTP requests/responses in kernel space

**Tasks:**
- [ ] Design HTTP parsing logic (eBPF limitations):
  - Max 512 byte verifier complexity (need to keep it simple)
  - No unbounded loops (use bounded loops with verified limits)
  - Parse only essential fields
- [ ] Implement HTTP request parser:
  - [ ] Extract HTTP method (GET, POST, PUT, DELETE, etc.)
  - [ ] Extract path/URL (e.g., `/api/users`)
  - [ ] Extract HTTP version
  - [ ] Extract Host header (for virtual hosts)
  - [ ] Extract Content-Length
- [ ] Implement HTTP response parser:
  - [ ] Extract status code (200, 404, 500, etc.)
  - [ ] Extract Content-Length
  - [ ] Calculate response time (timestamp diff)
- [ ] Create structured event format:
  ```c
  struct http_event {
      u64 timestamp_ns;
      u32 pid;
      u32 tid;
      u32 src_ip;
      u32 dst_ip;
      u16 src_port;
      u16 dst_port;
      u8 method;  // enum: GET=1, POST=2, etc.
      u16 status_code;
      u32 latency_ns;
      char path[128];
      char host[64];
  };
  ```
- [ ] Write to ring buffer efficiently
- [ ] Add bounds checking (eBPF verifier compliance)

**Key Decisions:**
- Use ring buffer vs BPF map? → **Ring buffer** (better for streaming)
- Parse in kernel vs userspace? → **Kernel** (filtering at source)
- How much to parse? → **Minimum for filtering** (offload complex parsing to userspace)

**Testing:**
- [ ] Test with various HTTP clients (curl, wget, python requests)
- [ ] Test with different HTTP methods
- [ ] Test with long URLs (truncation handling)
- [ ] Test with HTTP/1.0, HTTP/1.1
- [ ] Measure parsing overhead with `bpftool prog profile`

**Success Criteria:**
- Can parse 95%+ of standard HTTP requests
- Handles edge cases gracefully (no crashes)
- <1% CPU overhead per connection
- Verifier accepts the program (no rejections)

**Deliverable:** Fully functional HTTP parser in eBPF with structured events

---

### 1.5: CO-RE (Compile Once, Run Everywhere) Support
**Duration:** 3-4 days  
**Status:** ⬜ Not Started

**Goal:** Make eBPF programs portable across kernel versions

**Tasks:**
- [ ] Learn BTF (BPF Type Format) basics
  - Read: https://nakryiko.com/posts/bpf-portability-and-co-re/
- [ ] Convert programs to CO-RE style:
  - [ ] Use `vmlinux.h` instead of kernel headers
  - [ ] Use `BPF_CORE_READ()` macros
  - [ ] Add CO-RE relocations
- [ ] Generate `vmlinux.h`:
  ```bash
  bpftool btf dump file /sys/kernel/btf/vmlinux format c > vmlinux.h
  ```
- [ ] Update Makefile for CO-RE compilation
- [ ] Test on multiple kernel versions:
  - [ ] Kernel 5.10
  - [ ] Kernel 5.15
  - [ ] Kernel 6.1+

**Success Criteria:**
- Single compiled eBPF object works on 3+ kernel versions
- No runtime compilation required

**Deliverable:** CO-RE-enabled eBPF programs with cross-version compatibility

---

### 1.6: Performance Benchmarking & Optimization
**Duration:** 2-3 days  
**Status:** ⬜ Not Started

**Goal:** Measure and optimize overhead

**Tasks:**
- [ ] Set up benchmark environment
  - nginx or similar web server
  - `wrk` or `ab` for load testing
- [ ] Baseline without eBPF:
  ```bash
  wrk -t4 -c100 -d30s http://localhost:8080/
  ```
- [ ] Measure with eBPF attached
- [ ] Profile with `perf` and `bpftool prog profile`
- [ ] Optimize hot paths:
  - Reduce ring buffer writes
  - Minimize string operations
  - Use per-CPU data structures
- [ ] Document overhead:
  - Target: <1% CPU, <100ns per request

**Deliverable:** Performance report showing <1% overhead

---

### Phase 1 Completion Checklist

- [ ] eBPF programs compile successfully
- [ ] Can hook into TCP/socket layer
- [ ] HTTP requests and responses parsed correctly
- [ ] CO-RE support verified on multiple kernels
- [ ] Performance overhead measured and acceptable
- [ ] Code is clean and documented
- [ ] Unit tests written (where applicable)

**Next:** Move to Phase 2 (Agent Development)

---

## Phase 2: Agent Development

**Duration:** Week 4-5 (2 weeks)  
**Goal:** Build Go userspace agent that loads eBPF programs and processes events

### 2.1: Go Agent Scaffolding
**Duration:** 1-2 days

**Tasks:**
- [ ] Initialize Go module: `go mod init github.com/yourname/axon`
- [ ] Set up project structure:
  ```
  agent/
  ├── cmd/
  │   └── agent/
  │       └── main.go
  ├── pkg/
  │   ├── ebpf/       # eBPF loading logic
  │   ├── k8s/        # Kubernetes client
  │   ├── metrics/    # Prometheus metrics
  │   └── processor/  # Event processing
  └── go.mod
  ```
- [ ] Install dependencies:
  ```bash
  go get github.com/cilium/ebpf
  go get github.com/prometheus/client_golang
  go get k8s.io/client-go
  ```

---

### 2.2: eBPF Program Loading
**Duration:** 2-3 days

**Tasks:**
- [ ] Write Go loader using `cilium/ebpf` library
- [ ] Load compiled eBPF objects
- [ ] Attach to hooks (socket_filter, kprobes)
- [ ] Read from ring buffers
- [ ] Graceful shutdown and cleanup

---

### 2.3: Event Processing Pipeline
**Duration:** 3-4 days

**Tasks:**
- [ ] Parse events from ring buffer
- [ ] Correlate requests with responses (matching by connection tuple)
- [ ] Calculate latency metrics
- [ ] Buffer and batch events
- [ ] Handle high-cardinality data

---

### 2.4: Kubernetes Metadata Enrichment
**Duration:** 2-3 days

**Tasks:**
- [ ] Use K8s client-go to watch pods
- [ ] Build IP → Pod mapping cache
- [ ] Enrich events with:
  - Pod name
  - Namespace
  - Labels
  - Service name

---

### 2.5: Basic Prometheus Metrics
**Duration:** 1-2 days

**Tasks:**
- [ ] Expose `/metrics` endpoint
- [ ] Export basic metrics:
  - `http_requests_total{method, path, status}`
  - `http_request_duration_seconds{method, path}`
  - `http_request_size_bytes`

---

### Phase 2 Completion Checklist

- [ ] Agent loads eBPF programs successfully
- [ ] Events processed and enriched with K8s metadata
- [ ] Metrics exported to Prometheus
- [ ] Agent runs as DaemonSet (basic version)
- [ ] Tested in Kind/Minikube cluster

---

## Phase 3: Kubernetes Integration

**Duration:** Week 6-7 (2 weeks)  
**Goal:** Build operator with CRDs for declarative configuration

### 3.1: Operator Scaffolding
**Duration:** 1-2 days

**Tasks:**
- [ ] Initialize with Kubebuilder:
  ```bash
  kubebuilder init --domain axon.dev --repo github.com/yourname/axon
  ```
- [ ] Create HTTPMonitor CRD
- [ ] Create L7NetworkPolicy CRD (stub)

---

### 3.2: HTTPMonitor Controller
**Duration:** 3-4 days

**Tasks:**
- [ ] Watch HTTPMonitor resources
- [ ] Generate DaemonSet spec from CR
- [ ] Deploy/update agent DaemonSet
- [ ] Status updates on CR

---

### 3.3: Multi-Node Aggregation
**Duration:** 2-3 days

**Tasks:**
- [ ] Collect metrics from all agents
- [ ] Aggregate cluster-wide statistics
- [ ] Build service dependency graph

---

### Phase 3 Completion Checklist

- [ ] Operator deploys successfully
- [ ] HTTPMonitor CRD functional
- [ ] Agent DaemonSet managed by operator
- [ ] Cluster-wide metrics aggregated

---

## Phase 4: Policy Enforcement

**Duration:** Week 8-9 (2 weeks)  
**Goal:** Implement L7 network policies enforced in eBPF

### 4.1: Policy eBPF Programs
**Duration:** 4-5 days

**Tasks:**
- [ ] Write eBPF program for policy enforcement
- [ ] Use BPF maps for policy rules
- [ ] Block requests based on:
  - HTTP method
  - URL path patterns
  - Source pod identity

---

### 4.2: L7NetworkPolicy Controller
**Duration:** 3-4 days

**Tasks:**
- [ ] Watch L7NetworkPolicy CRs
- [ ] Compile policies into BPF map entries
- [ ] Update eBPF maps dynamically
- [ ] Test policy enforcement

---

### Phase 4 Completion Checklist

- [ ] L7 policies can be created via CRDs
- [ ] Policies enforced in kernel space
- [ ] Blocked requests don't reach application
- [ ] Policy changes applied in real-time

---

## Phase 5: Metrics & Visualization

**Duration:** Week 10 (1 week)  
**Goal:** Production-quality dashboards and alerting

### 5.1: Enhanced Prometheus Metrics
**Duration:** 2-3 days

**Tasks:**
- [ ] Add histograms for latency
- [ ] Add cardinality limiting
- [ ] Add service-level metrics

---

### 5.2: Grafana Dashboards
**Duration:** 2-3 days

**Tasks:**
- [ ] Create dashboard templates
- [ ] Service overview panel
- [ ] HTTP metrics panels
- [ ] Service map visualization (optional)

---

### Phase 5 Completion Checklist

- [ ] Comprehensive Grafana dashboards
- [ ] Alerting rules defined
- [ ] Documentation for metrics

---

## Phase 6: Production Hardening

**Duration:** Week 11-12 (2 weeks)  
**Goal:** Testing, documentation, and productionization

### 6.1: Testing Suite
**Duration:** 3-4 days

**Tasks:**
- [ ] Unit tests for agent
- [ ] Integration tests with Kind
- [ ] Load testing (performance validation)
- [ ] Chaos testing (node failures, etc.)

---

### 6.2: Documentation
**Duration:** 2-3 days

**Tasks:**
- [ ] Installation guide
- [ ] Configuration reference
- [ ] Troubleshooting guide
- [ ] Architecture deep-dive

---

### 6.3: CI/CD Pipeline
**Duration:** 2-3 days

**Tasks:**
- [ ] GitHub Actions workflows
- [ ] Automated builds
- [ ] Container image publishing
- [ ] Release automation

---

### Phase 6 Completion Checklist

- [ ] Full test coverage
- [ ] Documentation complete
- [ ] CI/CD pipeline operational
- [ ] Ready for public release

---

## Progress Tracking

### Current Sprint: Phase 1.1 - Environment Setup
**Start Date:** [To be filled]  
**Target Completion:** [To be filled]

### Completed Milestones
- [x] Project planning and architecture design
- [x] README.md created
- [x] EPIC.md created

### Next Steps
1. Set up development environment (Phase 1.1)
2. Write first eBPF program (Phase 1.2)
3. Begin HTTP parsing research (Phase 1.3)

---

## Notes & Learnings

**Add your learnings here as you progress:**

### Week 1
- [Date] - [Learning/Note]

### Week 2
- [Date] - [Learning/Note]

---

## Resources

**eBPF Learning:**
- [BPF and XDP Reference Guide](https://docs.cilium.io/en/stable/bpf/)
- [Andrii Nakryiko's Blog](https://nakryiko.com/)
- [eBPF.io Documentation](https://ebpf.io/)

**CO-RE:**
- [BPF Portability and CO-RE](https://nakryiko.com/posts/bpf-portability-and-co-re/)
- [libbpf-bootstrap](https://github.com/libbpf/libbpf-bootstrap)

**Kubernetes Operators:**
- [Kubebuilder Book](https://book.kubebuilder.io/)
- [Operator SDK](https://sdk.operatorframework.io/)

---

**Last Updated:** [Auto-update this as you progress]

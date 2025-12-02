# Axon - Implementation Epic

**Project Goal:** Build a production-quality eBPF-based L7 HTTP observability and network policy engine for Kubernetes using:

- **C** for eBPF programs (kernel side)
- **Rust** for userspace agent and Kubernetes operator

**Timeline:** 8–12 weeks (part-time, ~15–20 hours/week)

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
- [Notes & Learnings](#notes--learnings)
- [Resources](#resources)

---

## Overview

This epic tracks the complete development of Axon from initial eBPF "hello world" to a production-ready Kubernetes operator. Each phase builds on the previous, with clear milestones and deliverables.

### Development Philosophy

1. **Start minimal** – Get something working end-to-end quickly.  
2. **Iterate and expand** – Add features incrementally with feedback from your own usage.  
3. **Test continuously** – Don’t skip validation; regressions in kernel space suck.  
4. **Document as you go** – Future you will not remember why you chose that BPF map layout.  

---

## Success Criteria

### Minimum Viable Product (MVP)

- ✅ eBPF programs successfully parse HTTP requests/responses (at least HTTP/1.x)
- ✅ Rust agent loads eBPF programs and consumes events via ring/perf buffer
- ✅ Rust agent enriches events with Kubernetes metadata
- ✅ Rust-based Kubernetes operator manages Axon DaemonSet and CRDs
- ✅ Prometheus metrics exported and consumable by Grafana
- ✅ Basic Grafana dashboard showing HTTP metrics (latency, status codes, throughput)
- ✅ L7 network policies can be authored as CRDs and enforced via eBPF maps
- ✅ eBPF programs work across at least 3 different Linux kernel versions using CO-RE

### Stretch Goals

- Service dependency graph visualization (who talks to whom, on which endpoints)
- Distributed tracing correlation (span/trace IDs from headers)
- Basic anomaly/attack detection (spikes, 5xx, unusual paths)
- Multi-protocol support: gRPC, HTTP/2, (later) HTTP/3/QUIC via Rust `quiche`
- Multi-cluster story (not full mesh, but at least conceptual design)

---

## Phase 1: Core eBPF Development

**Duration:** Week 1–3 (3 weeks)  
**Goal:** Build and validate eBPF programs that hook into TCP/socket operations and parse HTTP traffic.

---

### 1.1: Development Environment Setup

**Duration:** 2–3 days  
**Status:** ⬜ Not Started  

**Tasks:**

- [ ] Set up Linux development VM (e.g. Ubuntu 22.04 or Fedora 38+)
  - Verify kernel version (5.10+):
    ```bash
    uname -r
    ```
  - Check BTF availability:
    ```bash
    ls /sys/kernel/btf/vmlinux
    ```
- [ ] Install build dependencies:
  ```bash
  sudo apt-get update
  sudo apt-get install -y \
    clang-14 llvm-14 libbpf-dev \
    linux-headers-$(uname -r) \
    build-essential git pkg-config \
    make
 Install bpftool for debugging:

sudo apt-get install -y bpftool
# or build from source if version is old
 Install Rust toolchain (stable):

curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh
source "$HOME/.cargo/env"
rustup default stable
 Set up project directory structure:

axon/
├── ebpf/             # eBPF C programs (.c, .h, Makefile)
├── agent/            # Rust agent (userspace)
├── operator/         # Rust Kubernetes operator
├── manifests/        # CRDs, RBAC, DaemonSet, etc.
├── examples/         # Sample configs / demos
└── docs/             # Documentation (arch, design, notes)
 Initialize Git repository:

cd axon
git init
 Create basic Makefile in ebpf/ to:

Build .bpf.o with clang/LLVM

Clean targets

Optionally run basic verifier checks

Deliverable:
Working local environment with toolchain + repo skeleton and initial Makefile.

1.2: eBPF "Hello World" – TCP Connection Tracing
Duration: 2–3 days
Status: ⬜ Not Started

Goal: Prove you can load eBPF programs into the kernel and see real events.

Tasks:

 Write basic eBPF program: ebpf/tcp_connect_trace.bpf.c

Attach to kprobe/tcp_connect or tracepoint/sock/inet_sock_set_state

Capture:

Source IP

Destination IP

Destination port

Write a small struct into ring buffer or a perf event.

 Create ebpf/tcp_connect_trace.h defining event struct and map definitions.

 Compile with clang:

cd ebpf
clang -O2 -g -target bpf \
  -c tcp_connect_trace.bpf.c \
  -o tcp_connect_trace.bpf.o
 Load and attach using:

Temporary minimal loader:

Option A: small C loader linked with libbpf

Option B: quick Rust loader using aya or libbpf-rs (if you already decided)

 Verify events:

Run curl google.com:80 or curl http://example.com/

Confirm that events are printed in userspace logger.

Success Criteria:

eBPF program loads successfully (no verifier errors).

Events are visible on each TCP connect from test process.

Program survives basic stress test (100+ concurrent connections) without crash.

Deliverable:
Working TCP connect tracer from kernel → userspace logs.

1.3: Socket-Level HTTP Request Capture
Duration: 4–5 days
Status: ⬜ Not Started

Goal: Capture raw HTTP data at socket layer (at least first packet / first bytes of request).

Tasks:

 Evaluate hooking options:

socket_filter (classic BSD-like filter at socket level)

kprobe/tcp_sendmsg + kprobe/tcp_recvmsg

sk_msg or tc ingress/egress if necessary

 Choose initial approach (likely socket_filter for simplicity).

 Implement ebpf/http_socket_filter.bpf.c:

Attach to socket for TCP/IPv4 (SO_ATTACH_BPF style filter).

Filter by destination ports:

80, 8080, 3000, (optionally configurable later).

Capture first N bytes (e.g., 512 or 1024) of the payload.

 Define ring buffer event struct that carries:

5-tuple (src/dst IP/port + protocol)

Direction (request vs response, if determinable)

Truncated payload bytes.

 Implement per-CPU ring buffer for streaming events.

 Testing:

Run a simple HTTP server:

python3 -m http.server 8080
Send requests via curl:

curl http://localhost:8080/
Verify that payloads containing GET /... HTTP/1.1 are captured.

Challenges to Solve:

HTTP can span multiple packets:

Decide: support only small requests for now or implement basic reassembly keyed by 5-tuple.

Ring buffer size trade-offs:

Too small → overflow / drops

Too large → memory overhead

Ensuring minimal overhead under load (use wrk later).

Success Criteria:

You can see the first line of HTTP requests in captured data.

No kernel panics, no random RCU / verifier issues.

Overhead stays <5% in simple benchmark.

Deliverable:
eBPF program that captures HTTP request bytes via socket filter and sends them up via ring buffer.

1.4: HTTP Protocol Parsing in eBPF
Duration: 5–7 days
Status: ⬜ Not Started

Goal: Parse just enough HTTP in the kernel to power L7 policies and reduce noise before userspace.

Tasks:

 Design the parsing strategy:

Only parse request line + a few headers.

No unbounded loops. Use bounded loops + length checks.

Avoid heavy string operations; treat data as bytes + simple scanning.

 For requests, extract:

HTTP method: GET, POST, PUT, DELETE, etc.

Path: e.g., /api/users

HTTP version: HTTP/1.0, HTTP/1.1

Host header (for virtual hosting).

 For responses, extract:

Status code: 200, 404, 500, etc.

Optional: Content-Length (for metrics).

 Decide where to compute latency:

Option A: store timestamps in BPF maps and compute diff in kernel.

Option B: compute latency in userspace once matching request/response events.

 Define struct http_event in ebpf/http_events.h:

struct http_event {
    u64 timestamp_ns;
    u32 pid;
    u32 src_ip;
    u32 dst_ip;
    u16 src_port;
    u16 dst_port;
    u8  direction;    // 0=request, 1=response
    u8  method;       // enum: GET=1, POST=2, ...
    u16 status_code;  // 0 if not set yet
    u32 latency_ns;   // optional, computed later
    char path[128];
    char host[64];
};
 Emit http_event into ring buffer.

 Add strict bounds checking for all memory accesses to satisfy verifier.

 Consider simple path truncation rules (if >128 bytes, truncate).

Testing:

 Use curl, wget, Python requests to generate traffic:

Long paths

Different methods

Missing Host header

 Validate:

Path + method + status code are correctly extracted.

No verifier failures.

 Use bpftool prog profile or perf to ensure hot paths are not too expensive.

Success Criteria:

~95%+ of typical HTTP/1.x traffic is parsed correctly for method + path + status.

Program is verifier-clean and stable under various traffic patterns.

Overhead per request remains within target (aim ~<1% CPU per node).

Deliverable:
Fully functional HTTP event parser in eBPF, generating http_event records.

1.5: CO-RE (Compile Once, Run Everywhere) Support
Duration: 3–4 days
Status: ⬜ Not Started

Goal: Make eBPF programs portable across multiple kernels via CO-RE.

Tasks:

 Read:

Andrii Nakryiko blog posts on BPF portability & CO-RE.

libbpf-bootstrap examples.

 Convert existing programs:

Replace direct struct access with BPF_CORE_READ() macros.

Rely on vmlinux.h instead of distro kernel headers.

 Generate vmlinux.h:

bpftool btf dump file /sys/kernel/btf/vmlinux format c > vmlinux.h
 Update Makefile to compile with CO-RE flags.

 Test on multiple kernel versions:

5.10 (e.g. older Debian/Ubuntu LTS)

5.15 (Ubuntu 22.04)

6.1+ (newer distros)

 Confirm that:

Same .bpf.o file loads fine on each kernel.

No runtime compilation is needed.

Success Criteria:

Single compiled eBPF object works on at least 3 different kernel versions.

No build-time dependencies on specific kernel source trees (only BTF + headers).

Deliverable:
CO-RE-enabled eBPF builds plus short doc on supported kernel versions.

1.6: Performance Benchmarking & Optimization
Duration: 2–3 days
Status: ⬜ Not Started

Goal: Quantify Axon’s overhead and optimize hot paths.

Tasks:

 Set up benchmark workload:

nginx or another simple HTTP server.

Use wrk / hey / ab for load generation.

 Baseline run (no eBPF):

wrk -t4 -c100 -d30s http://localhost:8080/
Record: RPS, latency distribution, CPU usage.

 With eBPF attached:

Repeat the same test.

Record new metrics, overhead delta.

 Use tools:

perf top, perf record to see kernel hotspots.

bpftool prog profile to inspect BPF instruction hotness.

 Optimize:

Reduce string parsing in kernel.

Avoid unnecessary copies of large payloads.

Use per-CPU maps and ring buffers efficiently.

 Document:

Target overhead: <1% CPU at typical load.

Target added latency: ~O(100ns) per request (order of magnitude).

Deliverable:
Performance report showing baseline vs with Axon eBPF, plus notes on optimizations.

Phase 1 Completion Checklist
 eBPF programs compile and load successfully.

 Hooks on TCP/socket layer working.

 HTTP requests/responses parsed and exported as structured events.

 CO-RE implementation verified across multiple kernels.

 Overhead measured and within acceptable bounds.

 Code is reasonably documented and organized.

 Basic sanity tests exist (even if manual / scripts).

Next: Move to Phase 2 – Rust Agent Development.

Phase 2: Agent Development
Duration: Week 4–5 (2 weeks)
Goal: Build a Rust userspace agent that loads eBPF programs, reads events, enriches them with Kubernetes metadata, and exposes Prometheus metrics.

2.1: Rust Agent Scaffolding
Duration: 1–2 days
Status: ⬜ Not Started

Tasks:

 Initialize Rust workspace:

cargo new --workspace axon
cd axon
cargo new agent
cargo new common
 Directory structure:

agent/
  src/
    main.rs
common/
  src/
    lib.rs          # shared types (http_event, config, etc.)
 Define shared HTTP event struct in common/src/lib.rs mirroring struct http_event:

#[repr(C)]
#[derive(Clone, Copy)]
pub struct HttpEvent {
    pub timestamp_ns: u64,
    pub pid: u32,
    pub src_ip: u32,
    pub dst_ip: u32,
    pub src_port: u16,
    pub dst_port: u16,
    pub direction: u8,
    pub method: u8,
    pub status_code: u16,
    pub latency_ns: u32,
    pub path: [u8; 128],
    pub host: [u8; 64],
}
 Add dependencies in agent/Cargo.toml:

eBPF integration (choose one stack and commit):

aya = "..."
or

libbpf-rs = "..." and libbpf-sys = "...".

Async runtime:

tokio = { version = "...", features = ["full"] }

HTTP server:

axum or hyper

Prometheus client:

prometheus or prometheus-client

Logging:

tracing

tracing-subscriber

Kubernetes client:

kube = { version = "...", features = ["runtime", "derive"] }

Serialization:

serde, serde_json (for config, debug)

 Implement main.rs skeleton:

Argument parsing (using clap or simple env/flags).

Init logging (tracing_subscriber).

Spawn top-level async runtime.

2.2: eBPF Program Loading from Rust
Duration: 2–3 days
Status: ⬜ Not Started

Tasks:

 Implement BPF loader in Rust (using chosen framework):

Load .bpf.o file(s) from ./ebpf directory.

Attach programs to required hooks:

socket_filter

kprobes/tracepoints as needed.

 Setup ring buffer / perf buffer read loop:

Register callback for ring buffer events.

Convert raw bytes into HttpEvent instances using std::mem::transmute or safe wrapper.

 Handle graceful shutdown:

On SIGINT/SIGTERM:

Stop consuming events.

Detach programs cleanly.

Flush metrics if needed.

Deliverable:
Rust agent can start, attach eBPF, receive raw HTTP events, and print them to logs.

2.3: Event Processing Pipeline
Duration: 3–4 days
Status: ⬜ Not Started

Goal: Build a pipeline that turns raw events into aggregated metrics.

Tasks:

 Convert HttpEvent byte arrays (path, host) to Rust Strings (truncate at \0 or use length heuristics).

 Derive a “metric key”:

Method

Path (optionally normalized)

Status code

src/dst IPs (later enriched with pod/service).

 Implement aggregation layer:

Use concurrent map (e.g., DashMap or sharded Mutex<HashMap<...>>) to store:

Counters: requests_total

Latency histograms (manual or Prometheus histograms).

 Optionally correlate request/response pairs:

Keyed by 5-tuple + maybe PID.

On request: store timestamp in an in-memory map.

On response: compute latency = now - stored ts.

 Handle backpressure:

Ensure ring buffer consumer is fast enough, or drop events gracefully with counters.

Deliverable:
Agent maintains in-memory metrics structures updated by eBPF events.

2.4: Kubernetes Metadata Enrichment (in Agent)
Duration: 2–3 days
Status: ⬜ Not Started

Goal: Map IPs to pods/namespaces/services and enrich metrics.

Tasks:

 Use kube crate:

Create an in-cluster client (using service account).

Watch Pod and/or Endpoints objects.

 Maintain cache:

Map: IpAddr -> PodMetadata { namespace, name, labels }

Possibly also: Pod -> Service relationships via Endpoints.

 Periodically refresh or rely on watch events for updates.

 Extend metrics:

Add labels: namespace, pod, app (from labels), maybe service.

 Implement fallback for unknown IPs (label them as "unknown").

Deliverable:
HTTP metrics labeled by Kubernetes identity, not just IPs.

2.5: Prometheus Metrics Endpoint
Duration: 1–2 days
Status: ⬜ Not Started

Tasks:

 Add HTTP server using axum or hyper in agent:

Listen on 0.0.0.0:9090 (configurable).

 Expose /metrics endpoint:

Register Prometheus counters/histograms.

On scrape, serialize metrics in Prometheus text format.

 Implement basic metrics:

axon_http_requests_total{method, path, status, namespace, pod, app}

axon_http_request_duration_seconds_bucket{...}

(Optionally) axon_http_request_bytes, axon_http_response_bytes.

Deliverable:
Prometheus can scrape the agent; Grafana can graph basic HTTP metrics.

Phase 2 Completion Checklist
 Agent loads eBPF programs from Rust.

 Events are processed, aggregated, and enriched with K8s metadata.

 /metrics endpoint exposes Prometheus-compatible metrics.

 Agent runs as a DaemonSet (even if manifests are manual for now).

 Tested on kind/Minikube with a sample app.

Next: Move to Phase 3 – Rust Operator & CRDs.

Phase 3: Kubernetes Integration
Duration: Week 6–7 (2 weeks)
Goal: Build a Rust operator using kube-rs with CRDs for declarative configuration of Axon.

3.1: Operator Scaffolding (Rust + kube-rs)
Duration: 1–2 days
Status: ⬜ Not Started

Tasks:

 Create operator crate in workspace:

cargo new operator
 Add dependencies to operator/Cargo.toml:

kube = { version = "...", features = ["runtime", "derive"] }

serde, serde_json, schemars

tokio

tracing, tracing-subscriber

 Define CRD Rust structs using kube::CustomResource derive:

HTTPMonitor

L7NetworkPolicy (stub initially)

 Generate CRD YAMLs (either via codegen or kube examples).

 Create basic operator main.rs:

Initialize logging.

Run controller for HTTPMonitor.

3.2: HTTPMonitor Controller
Duration: 3–4 days
Status: ⬜ Not Started

Goal: Use HTTPMonitor CRD to drive Axon agent DaemonSet configuration.

Tasks:

 Design HTTPMonitor spec:

spec.namespaces: list of namespaces to monitor.

spec.sampling: sampling rate (0.0–1.0).

spec.metrics: which metric groups to enable.

 Implement controller:

Watch HTTPMonitor objects.

Reconcile into:

DaemonSet spec for axon-agent.

ConfigMap or env vars for agent settings (sampling, ports, etc.).

 Implement .status updates:

Number of nodes targeted.

Number of ready agents.

 Ensure idempotent reconciliation:

Multiple reconciles should converge cleanly.

Handle deletion: clean up DaemonSet + ConfigMaps.

Deliverable:
Creating/updating an HTTPMonitor CRD configures/deploys the Axon agent DaemonSet automatically.

3.3: Multi-Node Aggregation Strategy
Duration: 2–3 days
Status: ⬜ Not Started

Goal: Decide how metrics across nodes are viewed.

Tasks:

 Choose strategy (likely Prometheus scrapes each node agent):

Each agent exposes metrics with node label.

Cluster-wide aggregation is done by Prometheus + Grafana.

 Ensure metrics include:

node

namespace

pod

app

service (if available)

 Provide example Prometheus config for scraping:

Scrape all axon-agent pods via Service or PodMonitor.

Deliverable:
Prometheus can see metrics from all node agents, and dashboards can do cluster-wide views.

Phase 3 Completion Checklist
 Operator deploys successfully into a cluster.

 HTTPMonitor CRD is functional and reconciles into DaemonSet + config.

 Agents are fully managed by operator (no manual DaemonSet).

 Cluster-wide metrics visible through Prometheus + Grafana.

Next: Move to Phase 4 – L7 Policy Enforcement.

Phase 4: Policy Enforcement
Duration: Week 8–9 (2 weeks)
Goal: Implement L7 HTTP policies enforced in kernel using eBPF maps, configured via CRDs.

4.1: Policy eBPF Programs & Maps
Duration: 4–5 days
Status: ⬜ Not Started

Tasks:

 Design BPF map layout for policies:

Key candidates:

Src identity (pod ID / label hash).

Dst identity.

HTTP method.

Path prefix or hash.

Value:

Allow / deny flag.

Optional counters (hits, last hit).

 Extend HTTP eBPF program:

On request event:

Compute identity info (from map keyed by IP, populated by userspace or operator).

Look up policy map.

Decide allow/deny.

If deny: drop packet / reset connection early.

 Provide safe default:

If no policy → allow traffic (or configurable default).

 Expose policy hit/miss counters via events or stats maps.

Deliverable:
Kernel-level policy decision path for HTTP requests with BPF maps backing.

4.2: L7NetworkPolicy Controller (Rust)
Duration: 3–4 days
Status: ⬜ Not Started

Tasks:

 Define L7NetworkPolicy CRD spec:

apiVersion: axon.dev/v1alpha1
kind: L7NetworkPolicy
spec:
  podSelector:
    matchLabels:
      app: backend
  ingress:
    - from:
        - podSelector:
            matchLabels:
              app: frontend
      httpRules:
        - method: GET
          path: /api/users
        - method: POST
          path: /api/users
 Implement controller logic:

Convert CRD into an internal policy model (Rust structs).

Map from podSelector → concrete pod identities (pod IPs, label hashes).

Generate entries for BPF policy maps.

Reconcile:

On policy create/update/delete, push changes to agents:

Option A: Use ConfigMap watched by agents.

Option B: Operator calls agent API (HTTP/gRPC).

Agents then program the BPF maps via eBPF API.

 Validate:

From allowed pods: requests succeed.

From disallowed pods: requests are blocked at kernel.

Deliverable:
L7 policies defined as CRDs, translated into BPF map entries, enforced by kernel.

Phase 4 Completion Checklist
 L7NetworkPolicy CRD is functional.

 Policies are mapped to BPF maps on each node.

 Blocked traffic never hits target app container.

 Policy updates apply without restarting agents or operator.

Next: Move to Phase 5 – Metrics & Visualization.

Phase 5: Metrics & Visualization
Duration: Week 10 (1 week)
Goal: Ship usable dashboards and basic alerting on top of Axon.

5.1: Enhanced Prometheus Metrics
Duration: 2–3 days
Status: ⬜ Not Started

Tasks:

 Add more detailed metrics in agent:

Latency histograms with decent buckets.

Error rate metrics (4xx/5xx breakdown).

Policy-related metrics:

axon_l7_policy_allowed_total

axon_l7_policy_denied_total

 Avoid cardinality explosion:

Path normalization (e.g., /users/:id).

Label whitelisting for K8s labels.

Optional sampling at agent level.

 Document recommended Prometheus scrape config.

5.2: Grafana Dashboards
Duration: 2–3 days
Status: ⬜ Not Started

Tasks:

 Create Grafana dashboard JSONs and store under examples/dashboards/:

Cluster overview:

Total RPS, P50/P95/P99 latency.

Error rate (4xx/5xx).

Service-level view:

Per service/per path metrics.

Policy view:

Allowed vs denied requests.

Top denied paths.

 Optional: service dependency map visualization using:

Prometheus metrics (source/destination labels).

Or additional metadata logs.

Deliverable:
Dashboards that make Axon “feel real” and showcase its capabilities.

Phase 5 Completion Checklist
 Grafana dashboards included in repo.

 Example Prometheus + Grafana configuration provided.

 Basic alerting rules for latency and error spikes documented.

Next: Move to Phase 6 – Production Hardening.

Phase 6: Production Hardening
Duration: Week 11–12 (2 weeks)
Goal: Add tests, docs, CI, and make Axon shippable as an alpha/beta.

6.1: Testing Suite
Duration: 3–4 days
Status: ⬜ Not Started

Tasks:

 Rust unit tests:

Event processing logic.

IP → pod mapping cache.

Policy translation (CRD → internal model).

 Integration tests:

Use kind / Minikube in CI (if feasible).

Deploy Axon operator + agents + sample app.

Run test traffic and assert metrics + policies.

 Load testing:

Use wrk against sample app with Axon enabled.

Ensure no severe degradation.

 Chaos testing:

Node restart.

Agent pod kill/restart.

Operator restart.

Ensure reconciliation restores state.

6.2: Documentation
Duration: 2–3 days
Status: ⬜ Not Started

Tasks:

 Write docs/ content:

architecture.md – high-level design, C + Rust split, data flow.

getting-started.md – quickstart on kind/Minikube.

configuration.md – CRD fields, flags, env vars.

troubleshooting.md – common issues (verifier failures, missing BTF, etc.).

 Add inline code comments and docstrings where missing.

6.3: CI/CD Pipeline
Duration: 2–3 days
Status: ⬜ Not Started

Tasks:

 GitHub Actions:

Lint + test Rust (agent + operator).

Build eBPF objects (clang).

Optionally run basic integration tests.

 Container images:

axon-agent image.

axon-operator image.

Push to GitHub Container Registry (or Docker Hub).

 Release flow:

Tag-based releases.

Attach manifests (CRDs, DaemonSet, operator Deployment).

Changelog.

Phase 6 Completion Checklist
 Meaningful test coverage for core logic.

 Documentation ready for external users to try Axon.

 CI builds + pushes images + runs basic tests.

 Public release (even if alpha) is possible without manual heroics.

Progress Tracking
Current Sprint: Phase 1.1 – Environment Setup
Start Date: [To be filled]
Target Completion: [To be filled]

Completed Milestones
 Project planning and architecture design

 README.md created

 EPIC.md created

Next Steps
Set up development environment (Phase 1.1).

Implement first eBPF program (Phase 1.2).

Start HTTP parsing design (Phase 1.3).

Notes & Learnings
Use this section as a scratch pad of things you discover and decisions you make.

Week 1
[Date] – [Learning/Decision]

Week 2
[Date] – [Learning/Decision]

Week 3
[Date] – [Learning/Decision]

Week 4+
[Date] – [Learning/Decision]

Resources
eBPF Learning:

Cilium BPF and XDP Reference Guide

Andrii Nakryiko’s blog (deep dives on BPF, CO-RE, libbpf)

ebpf.io Documentation and tutorials

libbpf-bootstrap GitHub repo

CO-RE:

Articles on BPF portability and CO-RE

Examples of CO-RE programs in Cilium / libbpf-bootstrap

Rust + eBPF:

aya-rs GitHub repo and examples

libbpf-rs and libbpf-sys examples

Blog posts on integrating Rust with eBPF

Kubernetes Operators in Rust:

kube-rs GitHub repo

kube-rs controller examples

Talks/blogs about Rust operators in production

Last Updated: [Update this when you change the plan]


If you want another pass where we **literally** keep your original text and just do surgical find/replace (Go → Rust, Kubebuilder → kube-rs) without touching anything else, say so and I’ll do that too.
::contentReference[oaicite:0]{index=0}

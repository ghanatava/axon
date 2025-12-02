# Axon

**Kernel-space HTTP/QUIC observability for Kubernetes without instrumentation**

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Rust Version](https://img.shields.io/badge/Rust-1.75+-orange?logo=rust)](https://www.rust-lang.org/)
[![eBPF](https://img.shields.io/badge/eBPF-Powered-orange)](https://ebpf.io/)

## What is Axon?

Axon is an eBPF-powered L7 network observability and policy enforcement platform for Kubernetes built with Rust and C. It provides zero-instrumentation HTTP/QUIC monitoring and identity-aware network policies enforced directly in the Linux kernel.

Unlike traditional monitoring solutions that require sidecars, service mesh proxies, or application instrumentation, Axon operates at the kernel level using eBPF technology - giving you complete visibility with near-zero overhead.

## Why Axon?

**The Problem:**
- Traditional observability requires instrumentation (code changes)
- Service meshes add latency and complexity (sidecar proxies)
- Network policies are IP-based, not identity-aware
- HTTP/3 and QUIC adoption requires new monitoring approaches
- Debugging microservice latency is painful without L7 visibility

**The Axon Solution:**
- ✅ Zero application changes - works with any HTTP/QUIC service
- ✅ Sub-millisecond overhead using eBPF
- ✅ L7-aware network policies (e.g., "only allow GET /api/users")
- ✅ Real-time HTTP/1.1, HTTP/2, and HTTP/3 (QUIC) metrics
- ✅ Service dependency mapping from actual traffic
- ✅ Memory-safe Rust userspace (no leaks, no unsafe pitfalls)
- ✅ Kubernetes-native with custom CRDs

## Key Features

### 🔍 Zero-Instrumentation Observability
- HTTP/1.1, HTTP/2, and HTTP/3 (QUIC) request/response tracking
- Per-endpoint latency metrics (P50, P95, P99)
- Status code distribution and error rates
- Payload size and throughput monitoring
- QUIC connection migration tracking
- No code changes or agent injection required

### 🛡️ L7 Network Policy Enforcement
- Identity-aware policies based on K8s labels
- HTTP method and path filtering
- QUIC stream-level policies
- Block malicious requests before they reach the application
- Policy enforcement in kernel space (faster than iptables)

### 📊 Service Dependency Graph
- Automatic discovery of service-to-service communication
- Real-time topology visualization
- Endpoint-level dependency tracking
- Protocol-aware (HTTP/1.1, HTTP/2, HTTP/3)
- Integration with Prometheus and Grafana

### ⚡ Performance
- <1% CPU overhead per node
- Sub-microsecond latency impact
- Memory-safe Rust prevents leaks and crashes
- CO-RE (Compile Once, Run Everywhere) eBPF programs
- Efficient ring buffer for kernel-to-userspace communication

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                    Axon Operator (Rust)                      │
│  • Watches HTTPMonitor & L7NetworkPolicy CRDs               │
│  • Manages DaemonSet lifecycle                              │
│  • Aggregates metrics across cluster                        │
└─────────────────────────────────────────────────────────────┘
                            │
              ┌─────────────┴─────────────┐
              ▼                           ▼
    ┌──────────────────┐        ┌──────────────────┐
    │   Node Agent     │        │   Node Agent     │
    │   (Rust)         │        │   (Rust)         │
    │                  │        │                  │
    │ ┌──────────────┐ │        │ ┌──────────────┐ │
    │ │ eBPF Programs│ │        │ │ eBPF Programs│ │
    │ │    (C)       │ │        │ │    (C)       │ │
    │ │              │ │        │ │              │ │
    │ │ • socket_filter│        │ │ • socket_filter│
    │ │ • http_parser│ │        │ │ • http_parser│ │
    │ │ • quic_parser│ │        │ │ • quic_parser│ │
    │ │ • policy_enforce│        │ │ • policy_enforce│
    │ └──────────────┘ │        │ └──────────────┘ │
    │                  │        │                  │
    │  Ring Buffer ↕   │        │  Ring Buffer ↕   │
    │                  │        │                  │
    │  Rust Userspace  │        │  Rust Userspace  │
    │  • quiche (QUIC) │        │  • quiche (QUIC) │
    │  • K8s metadata  │        │  • K8s metadata  │
    │  • Prometheus    │        │  • Prometheus    │
    └──────────────────┘        └──────────────────┘
              │                           │
              └───────────┬───────────────┘
                          ▼
                  ┌───────────────┐
                  │  Prometheus   │
                  │   + Grafana   │
                  └───────────────┘
```

## Tech Stack

| Component | Technology | Purpose |
|-----------|-----------|---------|
| **eBPF Programs** | C + libbpf + CO-RE | Kernel-space HTTP/QUIC parsing and policy enforcement |
| **Agent** | Rust + Aya/libbpf-rs | Memory-safe userspace processing |
| **QUIC/HTTP3** | quiche (Cloudflare) | QUIC protocol parsing and handling |
| **Operator** | Rust + kube-rs | CRD management and cluster orchestration |
| **Metrics** | Prometheus | Time-series metrics storage |
| **Visualization** | Grafana | Dashboards and service maps |
| **CI/CD** | GitHub Actions | Automated builds and testing |

### Why Rust + C?

**Polyglot Approach Benefits:**
- **C for eBPF**: Mature tooling, kernel-verified, libbpf ecosystem
- **Rust for Userspace**: Memory safety, no leaks, modern concurrency
- **Best of Both Worlds**: Performance + Safety
- **quiche Integration**: Native Rust QUIC/HTTP3 support from Cloudflare

**Memory Safety Matters:**
- C eBPF programs are kernel-verified (safe by design)
- Rust userspace prevents common bugs: use-after-free, data races, memory leaks
- Production-grade reliability without GC overhead

## Quick Start

> **Note:** Axon is currently in active development. See [EPIC.md](EPIC.md) for the implementation timeline.

### Prerequisites
- Kubernetes cluster (1.24+)
- Linux kernel 5.10+ with BTF support
- kubectl configured

### Installation (Coming Soon)

```bash
# Install Axon operator
kubectl apply -f https://github.com/yourname/axon/releases/latest/axon-operator.yaml

# Create an HTTP monitor
kubectl apply -f - <<EOF
apiVersion: axon.dev/v1alpha1
kind: HTTPMonitor
metadata:
  name: production-monitor
  namespace: default
spec:
  namespaces: ["default", "production"]
  protocols: ["http1", "http2", "quic"]
  sampling: 1.0  # 100% for now
  metrics:
    - requestLatency
    - statusCodes
    - throughput
    - quicMigrations
EOF

# View metrics in Grafana
kubectl port-forward -n axon-system svc/grafana 3000:3000
```

### Example: L7 Network Policy

```yaml
apiVersion: axon.dev/v1alpha1
kind: L7NetworkPolicy
metadata:
  name: backend-api-policy
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
          protocols: ["http1", "http2", "quic"]
        - method: POST
          path: /api/users
          protocols: ["http1", "http2"]
    - from:
        - podSelector:
            matchLabels:
              app: admin
      httpRules:
        - method: "*"
          path: /api/*
          protocols: ["http1", "http2", "quic"]
```

## Development

See [EPIC.md](EPIC.md) for the detailed development roadmap and current progress.

### Development Environment Setup

```bash
# Clone repository
git clone https://github.com/yourname/axon
cd axon

# Install Rust
curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh
rustup default stable

# Install C/eBPF dependencies (Ubuntu/Debian)
sudo apt-get update
sudo apt-get install -y \
    clang llvm libbpf-dev linux-headers-$(uname -r) \
    make gcc pkg-config libssl-dev

# Install cargo-bpf (optional, if using Aya)
cargo install bpf-linker

# Build eBPF programs
make build-ebpf

# Build Rust agent
cargo build --release

# Run tests
cargo test
```

## Roadmap

- [x] Project planning and architecture design
- [ ] **Phase 1: Core eBPF** - HTTP/QUIC parsing and tracking
- [ ] **Phase 2: Rust Agent** - Userspace processing with quiche
- [ ] **Phase 3: Kubernetes Integration** - Operator with kube-rs
- [ ] **Phase 4: Policy Enforcement** - L7 network policies
- [ ] **Phase 5: Metrics & Visualization** - Prometheus + Grafana
- [ ] **Phase 6: Production Hardening** - Testing and optimization

See [EPIC.md](EPIC.md) for detailed milestones and tasks.

## Project Structure

```
axon/
├── ebpf/                  # C eBPF programs
│   ├── http_parser.bpf.c
│   ├── quic_parser.bpf.c
│   └── policy_engine.bpf.c
├── axon-agent/            # Rust agent (DaemonSet)
│   ├── src/
│   │   ├── ebpf/         # eBPF loading
│   │   ├── quic/         # quiche integration
│   │   ├── k8s/          # Kubernetes client
│   │   └── metrics/      # Prometheus
│   └── Cargo.toml
├── axon-operator/         # Rust operator
│   ├── src/
│   │   ├── controllers/
│   │   └── crds/
│   └── Cargo.toml
├── examples/              # Sample configs
├── docs/                  # Documentation
├── Makefile
└── Cargo.toml            # Workspace
```

## Contributing

Contributions are welcome! This is currently a learning project, but feel free to:
- Open issues for bugs or feature requests
- Submit PRs for improvements
- Share feedback on architecture decisions

## Inspiration

Axon is inspired by production-grade projects like:
- [Cilium](https://cilium.io/) - eBPF-based networking and security
- [Hubble](https://github.com/cilium/hubble) - Network observability
- [quiche](https://github.com/cloudflare/quiche) - Cloudflare's QUIC implementation
- [Aya](https://aya-rs.dev/) - Rust eBPF library
- [Pixie](https://px.dev/) - Kubernetes observability with eBPF

## License

MIT License - See [LICENSE](LICENSE) file for details

## Contact

Built with ❤️ as a learning project to understand eBPF, QUIC, Linux kernel internals, Rust systems programming, and modern observability patterns.

---

**⚠️ Development Status:** This project is in active development. Not ready for production use.

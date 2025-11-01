# Axon

**Kernel-space HTTP observability for Kubernetes without instrumentation**

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Go Version](https://img.shields.io/badge/Go-1.21+-00ADD8?logo=go)](https://go.dev/)
[![eBPF](https://img.shields.io/badge/eBPF-Powered-orange)](https://ebpf.io/)

## What is Axon?

Axon is an eBPF-powered L7 network observability and policy enforcement platform for Kubernetes. It provides zero-instrumentation HTTP monitoring and identity-aware network policies enforced directly in the Linux kernel.

Unlike traditional monitoring solutions that require sidecars, service mesh proxies, or application instrumentation, Axon operates at the kernel level using eBPF technology - giving you complete visibility with near-zero overhead.

## Why Axon?

**The Problem:**
- Traditional observability requires instrumentation (code changes)
- Service meshes add latency and complexity (sidecar proxies)
- Network policies are IP-based, not identity-aware
- Debugging microservice latency is painful without L7 visibility

**The Axon Solution:**
- ✅ Zero application changes - works with any HTTP service
- ✅ Sub-millisecond overhead using eBPF
- ✅ L7-aware network policies (e.g., "only allow GET /api/users")
- ✅ Real-time HTTP metrics (latency, status codes, throughput)
- ✅ Service dependency mapping from actual traffic
- ✅ Kubernetes-native with custom CRDs

## Key Features

### 🔍 Zero-Instrumentation Observability
- HTTP request/response tracking at kernel level
- Per-endpoint latency metrics (P50, P95, P99)
- Status code distribution and error rates
- Payload size and throughput monitoring
- No code changes or agent injection required

### 🛡️ L7 Network Policy Enforcement
- Identity-aware policies based on K8s labels
- HTTP method and path filtering
- Block malicious requests before they reach the application
- Policy enforcement in kernel space (faster than iptables)

### 📊 Service Dependency Graph
- Automatic discovery of service-to-service communication
- Real-time topology visualization
- Endpoint-level dependency tracking
- Integration with Prometheus and Grafana

### ⚡ Performance
- <1% CPU overhead per node
- Sub-microsecond latency impact
- CO-RE (Compile Once, Run Everywhere) eBPF programs
- Efficient ring buffer for kernel-to-userspace communication

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                    Axon Operator                             │
│  • Watches HTTPMonitor & L7NetworkPolicy CRDs               │
│  • Manages DaemonSet lifecycle                              │
│  • Aggregates metrics across cluster                        │
└─────────────────────────────────────────────────────────────┘
                            │
              ┌─────────────┴─────────────┐
              ▼                           ▼
    ┌──────────────────┐        ┌──────────────────┐
    │   Node Agent     │        │   Node Agent     │
    │   (DaemonSet)    │        │   (DaemonSet)    │
    │                  │        │                  │
    │ ┌──────────────┐ │        │ ┌──────────────┐ │
    │ │ eBPF Programs│ │        │ │ eBPF Programs│ │
    │ │              │ │        │ │              │ │
    │ │ • socket_filter│        │ │ • socket_filter│
    │ │ • http_parser│ │        │ │ • http_parser│ │
    │ │ • policy_enforce│        │ │ • policy_enforce│
    │ └──────────────┘ │        │ └──────────────┘ │
    │                  │        │                  │
    │  Ring Buffer ↕   │        │  Ring Buffer ↕   │
    │                  │        │                  │
    │  Go Userspace    │        │  Go Userspace    │
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
| **eBPF Programs** | C + libbpf + CO-RE | Kernel-space HTTP parsing and policy enforcement |
| **Agent** | Go + cilium/ebpf | Userspace processing and K8s integration |
| **Operator** | Kubebuilder | CRD management and cluster orchestration |
| **Metrics** | Prometheus | Time-series metrics storage |
| **Visualization** | Grafana | Dashboards and service maps |
| **CI/CD** | GitHub Actions | Automated builds and testing |

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
  sampling: 1.0  # 100% for now
  metrics:
    - requestLatency
    - statusCodes
    - throughput
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
        - method: POST
          path: /api/users
    - from:
        - podSelector:
            matchLabels:
              app: admin
      httpRules:
        - method: "*"
          path: /api/*
```

## Development

See [EPIC.md](EPIC.md) for the detailed development roadmap and current progress.

### Development Environment Setup

```bash
# Clone repository
git clone https://github.com/yourname/axon
cd axon

# Install dependencies (Ubuntu/Debian)
sudo apt-get update
sudo apt-get install -y \
    clang llvm libbpf-dev linux-headers-$(uname -r) \
    make gcc

# Install Go 1.21+
wget https://go.dev/dl/go1.21.0.linux-amd64.tar.gz
sudo tar -C /usr/local -xzf go1.21.0.linux-amd64.tar.gz

# Build eBPF programs
make build-ebpf

# Run tests
make test
```

## Roadmap

- [x] Project planning and architecture design
- [ ] **Phase 1: Core eBPF** - HTTP parsing and basic tracking
- [ ] **Phase 2: Agent Development** - Userspace processing
- [ ] **Phase 3: Kubernetes Integration** - Operator and CRDs
- [ ] **Phase 4: Policy Enforcement** - L7 network policies
- [ ] **Phase 5: Metrics & Visualization** - Prometheus + Grafana
- [ ] **Phase 6: Production Hardening** - Testing and optimization

See [EPIC.md](EPIC.md) for detailed milestones and tasks.

## Contributing

Contributions are welcome! This is currently a learning project, but feel free to:
- Open issues for bugs or feature requests
- Submit PRs for improvements
- Share feedback on architecture decisions

## Inspiration

Axon is inspired by production-grade projects like:
- [Cilium](https://cilium.io/) - eBPF-based networking and security
- [Hubble](https://github.com/cilium/hubble) - Network observability
- [Pixie](https://px.dev/) - Kubernetes observability with eBPF
- [Falco](https://falco.org/) - Runtime security

## License

MIT License - See [LICENSE](LICENSE) file for details

## Contact

Built with ❤️ as a learning project to understand eBPF, Linux kernel internals, and modern observability patterns.

---

**⚠️ Development Status:** This project is in active development. Not ready for production use.

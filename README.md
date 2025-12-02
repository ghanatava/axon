# Axon

**Kernel-space HTTP observability for Kubernetes without instrumentation**

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Rust](https://img.shields.io/badge/Rust-stable-b7410e?logo=rust)](https://www.rust-lang.org/)
[![eBPF](https://img.shields.io/badge/eBPF-Powered-orange)](https://ebpf.io/)

## What is Axon?

Axon is an eBPF-powered L7 network observability and policy enforcement platform for Kubernetes.  
It provides zero-instrumentation HTTP monitoring and identity-aware network policies enforced directly in the Linux kernel.

Unlike traditional monitoring solutions that require sidecars, service mesh proxies, or application instrumentation, Axon operates at the kernel level using eBPF – listening to what the kernel already knows and giving you visibility with minimal overhead.

> Axon is first and foremost a **learning project**: the best way to learn low-level systems is to build with them.

## Why Axon?

**The Problem:**
- Traditional observability requires instrumentation (code changes)
- Service meshes add latency and complexity (sidecar proxies)
- Network policies are IP-based, not identity-aware
- Debugging microservice latency is painful without L7 visibility

**The Axon Solution:**
- ✅ Zero application changes – works with any HTTP service
- ✅ eBPF-based datapath: minimal overhead, no sidecars
- ✅ L7-aware network policies (e.g., “only allow `GET /api/users`”)
- ✅ Real-time HTTP metrics (latency, status codes, throughput)
- ✅ Service dependency mapping from actual traffic
- ✅ Kubernetes-native via custom CRDs and a Rust operator

Planned protocol coverage:

- HTTP/1.x and HTTP/2
- QUIC + HTTP/3 via Rust’s [`quiche`](https://github.com/cloudflare/quiche)

## Design Principles

- **Two-language stack:**  
  - C for eBPF programs (kernel side, CO-RE, strict & minimal)
  - Rust for all userspace components (agent + operator)

- **Kernel-first, not kernel-only:**  
  Use eBPF for what it’s good at (fast hooks, filtering, simple state), and push complex logic (parsing, correlation, policy evaluation) into Rust.

- **Kubernetes-native:**  
  Configuration lives in CRDs (`HTTPMonitor`, `L7NetworkPolicy`, etc.). A Rust operator reconciles desired state into DaemonSets, ConfigMaps, and BPF map contents.

- **Learning > polish (for now):**  
  The initial goal is to understand eBPF, Linux networking, and Rust deeply. Production hardening comes later.

## Key Features

### 🔍 Zero-Instrumentation Observability

- HTTP request/response tracking at kernel level
- Per-endpoint latency metrics (P50, P95, P99)
- Status code distribution and error rates
- Payload size and throughput monitoring
- No code changes, no sidecars, no language-specific SDKs

### 🛡️ L7 Network Policy Enforcement

- Identity-aware policies based on Kubernetes labels
- HTTP method + path filtering
- Block unwanted/malicious requests before they reach the application
- Enforcement via eBPF at socket / skb level using BPF maps as policy tables

### 📊 Service Dependency Graph

- Automatic discovery of service-to-service communication
- Real-time topology view (who talks to whom, on which paths)
- Endpoint-level dependency tracking
- Metrics export compatible with Prometheus (Grafana dashboards on top)

### ⚡ Performance Aspirations

- Keep per-node CPU overhead minimal
- Keep latency impact below microseconds per request where possible
- Use CO-RE eBPF and ring buffer / perf buffer for kernel → userspace transfer
- Avoid unnecessary copies and allocations in the Rust agent

## Architecture

```text
┌──────────────────────────────────────────────────────────────┐
│                        Axon Operator (Rust)                 │
│  • Watches HTTPMonitor & L7NetworkPolicy CRDs               │
│  • Manages Axon DaemonSet / Config                          │
│  • Programs policy into eBPF maps                           │
└──────────────────────────────────────────────────────────────┘
                             │
                 ┌───────────┴───────────┐
                 ▼                       ▼
       ┌───────────────────┐    ┌───────────────────┐
       │   Axon Agent      │    │   Axon Agent      │
       │   (DaemonSet Pod) │    │   (DaemonSet Pod) │
       │   Rust userspace  │    │   Rust userspace  │
       │                   │    │                   │
       │  ┌──────────────┐ │    │  ┌──────────────┐ │
       │  │ eBPF (C, CO-RE│ │    │  │ eBPF (C, CO-RE│ │
       │  │               │ │    │  │               │ │
       │  │ • socket hooks│ │    │  │ • socket hooks│ │
       │  │ • HTTP/L7 tap │ │    │  │ • HTTP/L7 tap │ │
       │  │ • policy check│ │    │  │ • policy check│ │
       │  └──────────────┘ │    │  └──────────────┘ │
       │        ▲          │    │        ▲          │
       │  ring/perf buffer │    │  ring/perf buffer │
       │        │          │    │        │          │
       │   Rust Agent      │    │   Rust Agent      │
       │   • decode events │    │   • decode events │
       │   • enrich with   │    │   • enrich with   │
       │     K8s metadata  │    │     K8s metadata  │
       │   • metrics (HTTP │    │   • metrics (HTTP │
       │     endpoint)     │    │     endpoint)     │
       └───────────────────┘    └───────────────────┘
                 │                       │
                 └───────────┬───────────┘
                             ▼
                     ┌───────────────┐
                     │  Prometheus   │
                     │   + Grafana   │
                     └───────────────┘

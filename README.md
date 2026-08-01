# axon

**Zero-instrumentation gRPC tracing for Go services, including TLS — via eBPF.**

axon attaches to a running Go binary with no code changes, no sidecar, and no
service mesh, and reconstructs gRPC request/response pairs — even when the
traffic is encrypted with `crypto/tls`. It works by hooking Go's TLS
read/write functions directly in the process's memory, before encryption
happens (on write) and after decryption happens (on read), then
demultiplexing the resulting HTTP/2 byte stream back into gRPC calls.

## Why this exists

Most eBPF-based L7 tracers (Pixie, Cilium/Hubble, Datadog's agent) get
TLS-transparent HTTP visibility by hooking `SSL_write`/`SSL_read` in OpenSSL.
That approach is blind to Go services, because Go's `crypto/tls` is a pure-Go
TLS implementation — it never calls OpenSSL. A large fraction of
cloud-native infra (most of Kubernetes itself, most CNCF projects, a huge
share of internal microservices) is written in Go. axon exists to close that
specific, well-known blind spot.

See [EPIC.md](./EPIC.md) for the full implementation plan, the hard
technical problems this project is built around, and a running log of
what's been learned.

## Scope

**In scope:**
- gRPC-over-HTTP/2 tracing for Go services
- TLS transparency via `crypto/tls` uprobes (no OpenSSL dependency)
- Correct behavior across Go's stack-copying goroutine model
- Kubernetes deployment as a DaemonSet, Prometheus metrics output

**Explicitly out of scope (for now):**
- Non-Go runtimes (no Node/Python/Java support)
- Plain HTTP/1.1 (HTTP/2 demux is the hard problem worth solving; HTTP/1.1
  parsing is comparatively trivial and not the point of this project)
- HTTP/3 / QUIC (interesting follow-on, deliberately deferred)
- Network policy enforcement / packet dropping (a different project)
- A "complete network suite" — axon does one thing

## Why it's hard

1. **Go doesn't call OpenSSL.** The uprobe target has to be
   `crypto/tls.(*Conn).Write` / `.Read` inside the Go binary itself, resolved
   from that binary's own symbol table — there's no shared library to hook.
2. **uretprobes corrupt Go binaries.** Go copies and moves goroutine stacks
   at runtime; a uretprobe's return-address patch can be invalidated or land
   in the wrong place. Returns have to be caught by disassembling the target
   function and placing a uprobe on every `RET` instruction instead.
3. **Go's calling convention is version- and register-dependent.** Go 1.17+
   uses a register-based ABI (ABIInternal); earlier versions pass arguments
   on the stack. Goroutines also migrate between OS threads mid-call, so
   `pid`/`tid` isn't a valid correlation key — the goroutine ID has to be
   read from the `g` struct via the `r14` register.

## Architecture

```
┌─────────────────────────────────────────────────────────┐
│                      Target Go Process                   │
│                                                            │
│   crypto/tls.(*Conn).Write() ──┐                          │
│   crypto/tls.(*Conn).Read()  ──┤── uprobes (per RET site) │
└─────────────────────────────────┼─────────────────────────┘
                                   │ plaintext bytes + goroutine id
                                   ▼
                        ┌─────────────────────┐
                        │   eBPF programs      │
                        │  (C, libbpf, CO-RE)  │
                        └──────────┬────────────┘
                                   │ ring buffer
                                   ▼
                        ┌─────────────────────┐
                        │   axon-agent (Go)    │
                        │  HTTP/2 frame demux  │
                        │  HPACK decode        │
                        │  stream correlation  │
                        │  gRPC method extract │
                        └──────────┬────────────┘
                                   │
                                   ▼
                     Prometheus metrics · Grafana dashboards
```

## Tech stack

- **eBPF programs:** C, libbpf, CO-RE (`vmlinux.h`, BTF relocations)
- **Agent:** Go — ring buffer consumer, HTTP/2/HPACK/gRPC demux, ELF symbol
  resolution for uprobe placement
- **Kubernetes packaging:** DaemonSet, Prometheus metrics endpoint, Grafana
  dashboard
- **Test targets:** small Go gRPC client/server pair, built across a matrix
  of Go versions to exercise the ABI differences

## Repo layout (planned)

```
axon/
├── bpf/            # eBPF C programs (uprobes, ring buffer)
├── cmd/agent/       # Go agent entrypoint
├── internal/
│   ├── symbols/     # ELF parsing, RET-site disassembly, symbol resolution
│   ├── h2demux/      # HTTP/2 frame parsing + HPACK
│   ├── grpc/          # gRPC method/stream correlation
│   └── metrics/        # Prometheus exporters
├── deploy/          # DaemonSet manifests, RBAC
├── testtargets/     # Go gRPC client/server fixtures per Go version
├── EPIC.md
└── README.md
```

## Status

Pre-implementation. See [EPIC.md](./EPIC.md) Phase 0.

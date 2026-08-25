# axon

**Zero-instrumentation gRPC tracing for Go services, including TLS, via eBPF.**

Attaches to a running Go binary, no code changes, no sidecar, no service
mesh, and reconstructs gRPC request/response pairs even through
`crypto/tls`. Hooks Go's TLS read/write functions in-process, before
encryption on write and after decryption on read, then demuxes the
resulting HTTP/2 byte stream back into gRPC calls.

## Why this exists

Most eBPF L7 tracers (Pixie, Cilium/Hubble, Datadog) hook
`SSL_write`/`SSL_read` in OpenSSL. Blind to Go: `crypto/tls` is pure Go,
never touches OpenSSL. And most of cloud-native infra is written in Go.
axon closes that blind spot.

See [EPIC.md](./EPIC.md) for the implementation plan, the hard problems,
and the running log.

## Scope

**In scope:**
- gRPC-over-HTTP/2 tracing for Go services
- TLS transparency via `crypto/tls` uprobes, no OpenSSL dependency
- Correct behavior across Go's stack-copying goroutine model
- Kubernetes DaemonSet deployment, Prometheus metrics output

**Explicitly out of scope (for now):**
- Non-Go runtimes (no Node/Python/Java)
- Plain HTTP/1.1, trivial parsing, not the point here
- HTTP/3 / QUIC, deferred follow-on
- Network policy enforcement / packet dropping, different project
- A "complete network suite." axon does one thing

## Why it's hard

1. **Go doesn't call OpenSSL.** Uprobe target is `crypto/tls.(*Conn).Write`
   / `.Read` inside the binary itself, resolved from its own symbol table.
   No shared library to hook.
2. **uretprobes corrupt Go binaries.** Goroutine stacks move at runtime,
   can invalidate a return-address patch mid-flight. Disassemble instead,
   uprobe every `RET` instruction directly.
3. **Go's ABI is version- and register-dependent.** 1.17+ is register-based
   (ABIInternal), earlier is stack-based. Goroutines migrate OS threads
   mid-call too, so `pid`/`tid` is useless as a key: goroutine ID comes off
   the `g` struct via `r14`.

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
- **Agent:** Go, ring buffer consumer, HTTP/2/HPACK/gRPC demux, ELF symbol
  resolution for uprobe placement
- **Kubernetes packaging:** DaemonSet, Prometheus metrics endpoint, Grafana
  dashboard
- **Test targets:** small Go gRPC client/server pair, built across a Go
  version matrix to exercise ABI differences

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

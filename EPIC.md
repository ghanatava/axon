# axon — EPIC

This is the working reference for axon: the locked scope, the concepts that
have to be understood to build each piece, the phase-by-phase plan, and a
running learning log. Update this file as you go — it's the artifact you
bring back to a chat six weeks from now to resume with full context.

## Locked scope statement

> axon traces gRPC calls in Go services with zero instrumentation —
> plaintext, over TLS, via uprobes on Go's `crypto/tls`, with HTTP/2 stream
> demultiplexing.

If a task doesn't serve that sentence, it doesn't belong in this version of
axon. See README.md "Non-goals" before adding scope.

## Why this project, not the alternatives considered

- Rejected: broadening into a general network observability suite (breadth
  without depth reads as tutorial-following, not engineering judgment).
- Rejected (for now): AF_XDP / kernel-bypass-style fast packet path — a
  legitimate, harder project, but a different one, and less aligned with
  the observability-focused companies on the target list.
- Rejected (for now): HTTP/3-over-QUIC extension — genuinely the hardest
  and most novel version of this idea, deliberately deferred until the
  TCP/HTTP/2 foundation is solid, since QUIC breaks even more assumptions
  (no kernel TCP stack to hook at all — QUIC is UDP + userspace framing).

---

## Concepts & problems register

Reference glossary — read the relevant entry before starting the phase that
needs it. Expand this section as new problems get solved; a good entry
explains the problem, why the obvious approach fails, and the fix.

### `bpf()` syscall & `bpf_attr`
Single syscall, `cmd` + tagged union `attr`. One command family's fields
are ever valid per call — union, not struct, because the interpretations
are mutually exclusive, and the union only costs as much memory as its
largest member.

### BTF (BPF Type Format) & CO-RE (Compile Once — Run Everywhere)
BTF is a compact encoding of kernel type layout (struct fields, offsets,
sizes) that modern kernels expose at `/sys/kernel/btf/vmlinux`. CO-RE lets
a single compiled eBPF binary run correctly across kernel versions: the
compiler emits relocation records for struct field accesses, and libbpf
patches the actual byte offsets at load time against whatever kernel BTF is
present, instead of baking in offsets from compile-time headers. This is
what makes shipping one binary across nodes/kernel versions possible.
**Not directly needed for kernel struct access in this project** (axon
reads userspace Go binary memory, not kernel structs) but still required
for CO-RE-portable BPF program *type* definitions and for anything that
touches `task_struct` (e.g., PID/cgroup lookups for k8s attribution).

### uprobes vs kprobes
kprobes attach to kernel functions; uprobes attach to a **file offset**
inside a userspace binary or shared library. axon is uprobe-only — the
whole point is intercepting the Go process's own TLS code, not kernel
socket functions (which would only see ciphertext).

### Why uretprobes don't work reliably on Go binaries
A uretprobe works by overwriting the return address on the stack with a
trampoline, then restoring control after the real function returns. Go's
runtime **copies and relocates goroutine stacks** as they grow (goroutines
start with tiny stacks, ~2-8KB, and grow by copying to a larger
allocation). If a stack copy happens between the uretprobe's entry patch
and the function's actual return, the patched return address may be stale,
corrupted, or pointing at freed memory — this can crash the traced
process. **Fix:** disassemble the target function ahead of time, find every
`RET` instruction's offset, and place ordinary (entry-style) uprobes at
each of those offsets instead of relying on the kernel's return-probe
mechanism.

### Go's calling convention (ABI0 vs ABIInternal)
Go ≤1.16 (ABI0) passes function arguments on the stack. Go 1.17+
(ABIInternal) passes the first several arguments/return values in
registers, stack only as overflow — a real performance-motivated ABI
change that breaks any uprobe argument-extraction code written for the old
convention. axon's argument extraction has to branch on the Go version the
target binary was built with (recoverable from the binary's `.go.buildinfo`
section or the `runtime.buildVersion` symbol).

### Goroutine identity via the `g` pointer
`pid`/`tid` (OS thread) is not a valid correlation key for a single logical
Go call, because the Go scheduler can migrate a goroutine to a different OS
thread mid-execution (at any function call that might block/preempt). The
stable identity is the **goroutine ID**, reachable at runtime via the
current `g` (goroutine) struct pointer, conventionally held in register
`r14` on amd64 in modern Go. This has to be read directly out of the
traced process's register state at uprobe-entry time.

### HTTP/2 framing
HTTP/2 multiplexes many logical request/response streams over one TCP
connection, split into typed frames (`HEADERS`, `DATA`, `SETTINGS`,
`WINDOW_UPDATE`, etc.), each tagged with a stream ID. Byte stream from a
`crypto/tls` read/write hook has to be reassembled per-connection first
(TLS record boundaries don't align with HTTP/2 frame boundaries), then
demultiplexed per stream ID before it means anything.

### HPACK
HTTP/2's header compression scheme. Headers are encoded against a
**dynamic table** that evolves per-connection based on prior frames on that
same connection — meaning a HEADERS frame is not self-contained; decoding
one requires connection-scoped state carried across frames. This state has
to live in the userspace agent (not the eBPF program) keyed per traced
connection.

### gRPC-over-HTTP/2 framing
gRPC messages ride inside HTTP/2 `DATA` frames with a 5-byte length-prefix
header (1 compression flag byte + 4-byte big-endian length) per message,
and the method name arrives via the HTTP/2 `:path` pseudo-header in the
stream's `HEADERS` frame (HPACK-decoded). Correlating a request stream to
its response stream is "same stream ID, same connection" — but the
response may arrive frames-interleaved with unrelated streams' data.

### Ring buffers (`BPF_MAP_TYPE_RINGBUF`)
The mechanism for getting captured bytes from the eBPF program (kernel
context) to the Go agent (userspace) efficiently — single-producer/
multi-consumer lock-free ring, avoids the polling and per-CPU-buffer
overhead of the older perf buffer approach.

---

## Phase plan

Each phase has a concrete, demoable exit criterion — don't move on until
you can show the thing working, not just "the code compiles."

### Phase 0 — Environment & toolchain validation
**Goal:** prove the whole toolchain works end to end before touching any
hard problem.
- [ ] Confirm kernel has BTF: `ls /sys/kernel/btf/vmlinux`
- [ ] Install clang/llvm/libbpf-dev/bpftool
- [ ] Generate `vmlinux.h` via `bpftool btf dump file /sys/kernel/btf/vmlinux format c`
- [ ] Write and load a trivial uprobe (any C binary, e.g. glibc's `read`)
  that just counts calls into a `BPF_MAP_TYPE_ARRAY`
- [ ] Confirm it fires: read the counter from userspace
- **Exit:** a uprobe fires on a real process and you can read data back.

### Phase 1 — Go symbol resolution & RET-site discovery
**Goal:** given a compiled Go binary, programmatically find the file offset
to attach a uprobe to for a named function, and find all its `RET` sites.
- [ ] Build a minimal Go test binary with a function you control
- [ ] Parse its ELF symbol table to resolve `runtime.newobject` or your own
  function's file offset (Go symbols are not stripped by default; note
  what changes if `-ldflags="-s -w"` is used)
- [ ] Disassemble the function body (objdump or a Go disassembly library)
  and enumerate `RET` instruction offsets
- [ ] Place a uprobe at entry and at every `RET` site; log each firing with
  a tag distinguishing entry vs. which return site
- **Exit:** entry and every return of a chosen Go function reliably logged,
  including for a function with multiple return statements.

### Phase 2 — TLS interception
**Goal:** capture plaintext bytes from `crypto/tls.(*Conn).Write` and
`.Read` in a real Go TLS client/server pair.
- [ ] Apply Phase 1's technique to `crypto/tls.(*Conn).Write` and `.Read`
- [ ] Extract arguments correctly for both ABI0 and ABIInternal targets
  (build test binaries with two Go versions to force both)
- [ ] Read the goroutine ID from `r14`/`g` at entry, propagate it through
  to the matching return-site event so entry/exit pairs are correlatable
- [ ] Copy the plaintext buffer contents into the ring buffer event
- **Exit:** a plaintext HTTP/2 preface (`PRI * HTTP/2.0...`) is visible in
  captured `Write` data from a real TLS-wrapped Go gRPC client, across both
  a pre-1.17 and post-1.17 test build.

### Phase 3 — HTTP/2 frame capture & stream demultiplexing
**Goal:** turn a stream of captured byte chunks (which don't align to
frame boundaries) into a clean per-stream sequence of typed frames.
- [ ] Implement a connection-scoped byte reassembly buffer in the Go agent,
  keyed by (pid, goroutine id, fd or conn identifier)
- [ ] Parse the HTTP/2 frame header (9 bytes: length, type, flags, stream
  id) and split the buffer into frames
- [ ] Demultiplex frames by stream ID into per-stream frame sequences
- **Exit:** given a captured byte stream from Phase 2, correctly reconstruct
  the ordered frame sequence for at least 2 concurrent streams on one
  connection.

### Phase 4 — HPACK & gRPC semantics
**Goal:** extract the actual gRPC method name and message payloads.
- [ ] Implement or integrate an HPACK decoder with per-connection dynamic
  table state
- [ ] Decode `HEADERS` frames to recover `:path` (the gRPC method) and
  other pseudo-headers
- [ ] Parse gRPC's 5-byte length-prefixed message framing inside `DATA`
  frames
- [ ] Correlate request stream → response stream → full gRPC call record
  (method, request size, response size, status, latency)
- **Exit:** a traced gRPC call between your test client/server produces a
  complete, correct call record with method name, without any code changes
  to the client/server.

### Phase 5 — Agent hardening & correlation at scale
**Goal:** survive real conditions — multiple concurrent connections,
multiple processes, goroutines migrating threads, connection churn.
- [ ] Multi-connection, multi-process test: several gRPC client/server
  pairs running simultaneously, verify no cross-contamination between
  streams/connections
- [ ] Handle connection close / cleanup of stale per-connection state
- [ ] Basic backpressure / drop handling if the ring buffer fills faster
  than the agent drains it
- **Exit:** sustained load test (some concurrency target you set) with
  zero misattributed calls.

### Phase 6 — Observability output
**Goal:** make the captured data useful, not just correct.
- [ ] Prometheus metrics: request count, latency histogram, error rate —
  labeled by method, and by pod/service if running in k8s
- [ ] Grafana dashboard
- **Exit:** a dashboard showing live gRPC traffic from the test workload.

### Phase 7 — Kubernetes packaging
**Goal:** deployable the way a real cluster tool would be.
- [ ] DaemonSet manifest, privileged/CAP_BPF requirements documented
- [ ] Discover target Go processes on a node (not just one hardcoded test
  binary) — process discovery, binary path resolution, per-process
  attach/detach lifecycle
- [ ] RBAC, resource limits
- **Exit:** deployed to a real (or kind/minikube) cluster, tracing a real
  Go gRPC workload without manual per-pod setup.

### Phase 8 — Stretch goals (explicitly not required for v1)
- [ ] HTTP/3 / QUIC support (separate, harder — no kernel TCP stack to
  lean on at all)
- [ ] Identity-aware policy hook (would fold in a `SOCK_OPS`/cgroup plane)
- [ ] Multi-runtime support beyond Go

---

## Testing strategy

- **Per-phase unit tests** for the Go-side parsing logic (HTTP/2 framing,
  HPACK, gRPC message framing) — these don't need a kernel at all and
  should be tested against captured/fixture byte sequences, not live eBPF.
- **Integration tests** against real Go test binaries built across a Go
  version matrix (at minimum: one pre-1.17 ABI0 build, one current
  ABIInternal build) to catch ABI-specific bugs early.
- **Concurrency/load tests** in Phase 5 — this is where goroutine-migration
  and stack-copy edge cases actually surface; they will not show up in a
  single-request smoke test.

## Success criteria (v1)

- Traces real gRPC calls between two unmodified Go binaries, over TLS, with
  correct method names and message boundaries.
- Works across at least two Go versions spanning the ABI0/ABIInternal
  boundary.
- Runs as a Kubernetes DaemonSet against workloads it wasn't specifically
  built for (not just the test fixtures).
- Every one of the three "why it's hard" problems in README.md has a
  written explanation in this file's concepts register, in your own words,
  detailed enough to defend in an interview.

---

## Progress tracking

| Phase | Status | Notes |
|---|---|---|
| 0 — Environment | Not started | |
| 1 — Go symbol resolution & RET sites | Not started | |
| 2 — TLS interception | Not started | |
| 3 — HTTP/2 demux | Not started | |
| 4 — HPACK & gRPC semantics | Not started | |
| 5 — Correlation at scale | Not started | |
| 6 — Observability output | Not started | |
| 7 — Kubernetes packaging | Not started | |
| 8 — Stretch | Deferred | |

## Notes & learnings

Log dated entries here as you go — what broke, what the fix was, what you'd
do differently. This is the raw material for interview stories later.

- _(empty — fill in as Phase 0 starts)_

## Resources

- [Andrii Nakryiko's blog](https://nakryiko.com/) — CO-RE, BTF, libbpf
  internals, generally the best primary source for modern eBPF
- [BPF and XDP Reference Guide (Cilium docs)](https://docs.cilium.io/en/stable/bpf/)
- [libbpf-bootstrap](https://github.com/libbpf/libbpf-bootstrap) — skeleton
  generation patterns
- [Go internal ABI spec](https://go.googlesource.com/go/+/refs/heads/master/src/cmd/compile/abi-internal.md)
  — required reading before Phase 2
- Pixie's OSS source (`px.dev`) — a real-world reference for uprobe-based
  Go/TLS tracing; useful to compare approaches against, not to copy from
- [HTTP/2 RFC 9113](https://www.rfc-editor.org/rfc/rfc9113.html)
- [HPACK RFC 7541](https://www.rfc-editor.org/rfc/rfc7541.html)
- [gRPC over HTTP/2 spec](https://github.com/grpc/grpc/blob/master/doc/PROTOCOL-HTTP2.md)

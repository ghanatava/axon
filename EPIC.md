# axon: EPIC

Locked scope, concepts needed per phase, the phase plan, running log.
Update as you go, this is what you come back to in six weeks.

## Locked scope statement

> axon traces gRPC calls in Go services with zero instrumentation:
> plaintext, over TLS, via uprobes on Go's `crypto/tls`, with HTTP/2 stream
> demultiplexing.

If it doesn't serve that sentence, it doesn't belong in this version.
See README.md "Non-goals" before adding scope.

## Why this, not the alternatives

- Rejected: general network observability suite. Breadth without depth
  reads as tutorial-following.
- Rejected for now: AF_XDP / kernel-bypass fast path. Real project, wrong
  one, doesn't match the target companies.
- Rejected for now: HTTP/3-over-QUIC. Hardest, most novel version of this
  idea, but QUIC is UDP + userspace framing, no kernel TCP stack to hook.
  Deferred until the TCP/HTTP2 foundation is solid.

---

## Concepts & problems register

Read the relevant entry before starting that phase. Add entries as new
problems get solved.

### `bpf()` syscall & `bpf_attr`
Single syscall, `cmd` + tagged union `attr`. Union not struct: only one
command's fields are ever live, no reason to pay for all of them at once.

### BTF (BPF Type Format) & CO-RE (Compile Once, Run Everywhere)
BTF: kernel type layout, exposed at `/sys/kernel/btf/vmlinux`. CO-RE:
libbpf patches struct offsets at load time against that, instead of
baking in compile-time header offsets.
Not needed for kernel struct access here (axon reads userspace Go memory),
but still required for portable BPF type defs and any `task_struct` work
(PID/cgroup lookups for k8s attribution).

### Virtual address vs file offset for uprobes
perf_event_open wants a file offset, not a virtual address. Raw
st_value/gopclntab addresses fail attachment with EINVAL every time.
Symbol-name lookup (cilium/ebpf's internal path) does this conversion;
bypassing it with a raw Address is what broke.

### uprobes vs kprobes
kprobes hook kernel functions. uprobes hook a file offset in a userspace
binary. axon is uprobe-only: a kernel socket hook only sees ciphertext.

### Why uretprobes don't work reliably on Go binaries
uretprobe patches the return address on the stack. Go relocates goroutine
stacks as they grow, so a stack copy between the patch and the real return
can leave it pointing at stale or freed memory. Crashes the traced process.
Fix: disassemble the function ahead of time, find every `RET` offset,
place ordinary entry-style uprobes there instead.

### Go's calling convention (ABI0 vs ABIInternal)
Go ≤1.16 (ABI0): args on the stack. 1.17+ (ABIInternal): register-based,
stack only for overflow. Breaks any arg-extraction code written for ABI0.
Argument extraction has to branch on the target binary's Go version
(`.go.buildinfo` section or `runtime.buildVersion`).

### Goroutine identity via the `g` pointer
`pid`/`tid` isn't a valid correlation key, the scheduler migrates
goroutines across OS threads mid-execution. Stable identity is the
goroutine ID off the current `g` pointer, held in `r14` on amd64.
Read directly from register state at uprobe-entry time.

### HTTP/2 framing
TLS read/write hooks hand over raw bytes, not frame-aligned (TLS record
boundaries don't match HTTP/2 frame boundaries). Reassemble per-connection
first, then split into typed frames, then demux by stream ID.

### HPACK
Dynamic table evolves per-connection, so a `HEADERS` frame doesn't decode
standalone. Table state has to live in the userspace agent, keyed per
traced connection, not in the eBPF program.

### gRPC-over-HTTP/2 framing
Message = 5-byte length prefix (1 flag byte + 4-byte BE length) inside a
`DATA` frame. Method name is the `:path` pseudo-header in the stream's
`HEADERS` frame, HPACK-decoded. Response frames can arrive interleaved
with other streams' data on the same connection.

### Ring buffers (`BPF_MAP_TYPE_RINGBUF`)
Gets captured bytes from the eBPF program to the Go agent. Lock-free
SPMC ring, no polling or per-CPU buffer overhead like perf buffers.

---

## Phase plan

Concrete demoable exit criterion per phase. Don't move on until it works,
not just compiles.

### Phase 0: Environment & toolchain validation
**Goal:** toolchain works end to end before the hard problems start.
- [ x ] Confirm kernel has BTF: `ls /sys/kernel/btf/vmlinux`
- [ x ] Install clang/llvm/libbpf-dev/bpftool
- [ x ] Generate `vmlinux.h` via `bpftool btf dump file /sys/kernel/btf/vmlinux format c`
- [ x ] Write and load a trivial uprobe (any C binary, e.g. glibc's `read`)
  that just counts calls into a `BPF_MAP_TYPE_ARRAY`
- [ x ] Confirm it fires: read the counter from userspace
- **Exit:** uprobe fires on a real process, data readable back.

### Phase 1: Go symbol resolution & RET-site discovery
**Goal:** find a named function's uprobe offset and every RET site from
the compiled binary.
- [ x ] Build a minimal Go test binary with a function you control
- [ x ] Parse ELF symbol table for the function's file offset (Go symbols
  unstripped by default, note what `-ldflags="-s -w"` breaks)
- [ x ] Disassemble the function body, enumerate RET instruction offsets
- [ x ] Uprobe at entry and every RET site, tag each firing
- **Exit:** entry and every return of a multi-return function reliably
  logged.

### Phase 2: TLS interception
**Goal:** capture plaintext from `crypto/tls.(*Conn).Write`/`.Read` in a
real Go TLS client/server.
- [ ] Apply Phase 1's technique to Write and Read
- [ ] Extract args correctly for both ABI0 and ABIInternal (two Go version
  test builds)
- [ ] Read goroutine ID from `r14`/`g` at entry, propagate to the matching
  return-site event
- [ ] Copy plaintext buffer into the ring buffer event
- **Exit:** plaintext HTTP/2 preface (`PRI * HTTP/2.0...`) visible in
  captured Write data, on both pre- and post-1.17 builds.

### Phase 3: HTTP/2 frame capture & stream demux
**Goal:** byte chunks that don't align to frame boundaries, turned into a
clean per-stream frame sequence.
- [ ] Connection-scoped reassembly buffer, keyed by (pid, goroutine id,
  fd/conn identifier)
- [ ] Parse the 9-byte frame header, split buffer into frames
- [ ] Demux frames by stream ID
- **Exit:** correct ordered frame sequence for 2+ concurrent streams on one
  connection.

### Phase 4: HPACK & gRPC semantics
**Goal:** method name and message payloads out of the wire.
- [ ] HPACK decoder with per-connection dynamic table state
- [ ] Decode HEADERS frames for `:path` and other pseudo-headers
- [ ] Parse gRPC's 5-byte length-prefixed message framing in DATA frames
- [ ] Correlate request stream -> response stream -> full call record
  (method, sizes, status, latency)
- **Exit:** complete correct call record with method name, zero client/
  server code changes.

### Phase 5: Agent hardening & correlation at scale
**Goal:** survive concurrent connections, multiple processes, thread
migration, connection churn.
- [ ] Multi-connection, multi-process test, verify no cross-contamination
- [ ] Handle connection close, cleanup stale per-connection state
- [ ] Backpressure/drop handling if the ring buffer outpaces the agent
- **Exit:** sustained load test, zero misattributed calls.

### Phase 6: Observability output
**Goal:** useful, not just correct.
- [ ] Prometheus metrics: request count, latency histogram, error rate,
  labeled by method and pod/service
- [ ] Grafana dashboard
- **Exit:** dashboard showing live gRPC traffic from the test workload.

### Phase 7: Kubernetes packaging
**Goal:** deployable like a real cluster tool, not a hardcoded demo.
- [ ] DaemonSet manifest, privileged/CAP_BPF requirements documented
- [ ] Discover target Go processes on a node: process discovery, binary
  path resolution, per-process attach/detach lifecycle
- [ ] RBAC, resource limits
- **Exit:** deployed to a real (or kind/minikube) cluster, no manual
  per-pod setup.

### Phase 8: Stretch goals (not required for v1)
- [ ] HTTP/3 / QUIC support (no kernel TCP stack to lean on)
- [ ] Identity-aware policy hook (`SOCK_OPS`/cgroup plane)
- [ ] Multi-runtime support beyond Go

---

## Testing strategy

- **Per-phase unit tests** for the Go-side parsing (HTTP/2, HPACK, gRPC
  framing), against fixture byte sequences, no kernel needed.
- **Integration tests** across a Go version matrix, at least one pre-1.17
  ABI0 build and one ABIInternal build, to catch ABI bugs early.
- **Concurrency/load tests** in Phase 5. Goroutine-migration and
  stack-copy bugs won't show up in a single-request smoke test.

## Success criteria (v1)

- Traces real gRPC calls between two unmodified Go binaries, over TLS,
  correct method names and message boundaries.
- Works across at least two Go versions spanning the ABI0/ABIInternal
  boundary.
- Runs as a DaemonSet against workloads it wasn't built for, not just the
  test fixtures.
- All three "why it's hard" problems from README.md written up in the
  concepts register, detailed enough to defend in an interview.

---

## Progress tracking

| Phase | Status | Notes |
|---|---|---|
| 0: Environment | Not started | |
| 1: Go symbol resolution & RET sites | Done | See Notes & learnings below; stripped-binary case verified after the fact (FileOffset fix) |
| 2: TLS interception | Not started | |
| 3: HTTP/2 demux | Not started | |
| 4: HPACK & gRPC semantics | Not started | |
| 5: Correlation at scale | Not started | |
| 6: Observability output | Not started | |
| 7: Kubernetes packaging | Not started | |
| 8: Stretch | Deferred | |

## Notes & learnings

Dated entries: what broke, the fix, what you'd do differently. Raw
material for interview stories later.

### Phase 1: Go symbol resolution & RET-site uprobe attachment

Verified end to end against `main.classify` in `testtargets/retsites/`
(3 return paths, `//go:noinline`):

- `internal/symbols.Resolve()`: ELF symbol lookup via `debug/elf`,
  checked byte-for-byte against `go tool nm`.
- `internal/symbols.RetSites()`: disassembles with
  `golang.org/x/arch/x86/x86asm`, walks instruction-by-instruction
  (`offset += inst.Len`, never a fixed stride) to find every `RET`.
  Matched `objdump -d` exactly.
- `bpf/retsite_count.bpf.c`: one BPF program (`SEC("uprobe")`, raw
  `pt_regs *ctx`, no `BPF_UPROBE` macro) attached at 4 addresses off
  the same loaded program. `bpf_get_attach_cookie()` distinguishes
  which one fired, counts land in an 8-slot `BPF_MAP_TYPE_ARRAY`.
- `poc/uprobe-retsites/main.go`: the loader/attacher.

Entry fired 7 times, the three RET sites fired 2/2/3, summing to 7.
Every call took exactly one return path, nothing lost or double-counted.

Two bugs worth remembering:

1. `UprobeOptions{Address: ...}` attaches silently but never fires.
   `UprobeOptions{Offset: ...}` plus the symbol name works, routes
   through the library's own address resolution instead of raw math.
   All 4 attach points go through this path now, no special-cased
   address logic left.
2. `-ldflags="-s -w"` strips the symbol table this whole technique
   depends on. Documented limitation, not a bug.

### 2026-08-26: Virtual address vs file offset for uprobe attachment

perf_event_open wants a file offset, not a virtual address. Raw
st_value/gopclntab addresses fail with EINVAL every time, stripped or
not, Offset field or no. cilium/ebpf's symbol-name lookup does this
conversion internally; bypassing it with a raw Address is what broke.

Fix: `Symbol.FileOffset = addr - section.Addr + section.Offset`, via
the same section lookup RetSites uses. Predicted 0x99b40 for
main.classify (vaddr 0x499b40, `.text` at vaddr 0x401000 / file offset
0x1000), attach + fire confirmed it on both classify and
classify_stripped.

Also kills the unstripped-binary requirement: FileOffset comes from
whichever of `.symtab`/`.gopclntab` resolved the symbol. Same fix
closed both gaps.

## Resources

- [Andrii Nakryiko's blog](https://nakryiko.com/) - best primary source for
  eBPF internals
- [BPF and XDP Reference Guide (Cilium docs)](https://docs.cilium.io/en/stable/bpf/)
- [libbpf-bootstrap](https://github.com/libbpf/libbpf-bootstrap) - skeleton
  generation patterns
- [Go internal ABI spec](https://go.googlesource.com/go/+/refs/heads/master/src/cmd/compile/abi-internal.md),
  required reading before Phase 2
- Pixie's OSS source (`px.dev`) - reference for uprobe-based Go/TLS
  tracing, compare against, don't copy from
- [HTTP/2 RFC 9113](https://www.rfc-editor.org/rfc/rfc9113.html)
- [HPACK RFC 7541](https://www.rfc-editor.org/rfc/rfc7541.html)
- [gRPC over HTTP/2 spec](https://github.com/grpc/grpc/blob/master/doc/PROTOCOL-HTTP2.md)

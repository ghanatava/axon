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
boundaries don't match HTTP/2 frame boundaries). Reassemble separately per (PID, TLS connection pointer, direction),
then split into typed frames and route by stream ID. Do not include goid
in this key: HTTP/2 transport goroutines share a connection. Both directions
use the same stream IDs but have independent byte streams and HPACK state.

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
- [ x ] Apply Phase 1's technique to Write and Read
- [ x ] Extract args correctly for both ABI0 and ABIInternal (two Go version
  test builds)
- [ x ] Read goroutine ID from `r14`/`g` at entry, propagate to the matching
  return-site event
- [ x ] Copy plaintext buffer into the ring buffer event
- **Exit:** plaintext HTTP/2 preface (`PRI * HTTP/2.0...`) visible in
  captured Write data, on both pre- and post-1.17 builds.

### Phase 3: HTTP/2 frame capture & stream demux
**Goal:** byte chunks that don't align to frame boundaries, turned into a
clean per-stream frame sequence.
- [ x ] Connection-scoped reassembly buffers, keyed by (pid, TLS connection
  pointer, direction); goroutine IDs identify calls, not connections
- [ x ] Parse the 9-byte frame header, split buffer into frames
- [ x ] Demux frames by stream ID
- **Exit:** correct ordered frame sequence for 2+ concurrent streams on one
  connection.

### Phase 4: HPACK & gRPC semantics
**Goal:** method name and message payloads out of the wire.
- [ x ] HPACK decoder with per-connection dynamic table state
- [ x ] Decode HEADERS frames for `:path` and other pseudo-headers
- [ x ] Parse gRPC's 5-byte length-prefixed message framing in DATA frames
- [ x ] Correlate request stream -> response stream -> full call record
  (method, sizes, status, latency)
- **Exit:** complete correct call record with method name, zero client/
  server code changes.

### Phase 5: Agent hardening & correlation at scale
**Goal:** survive concurrent connections, multiple processes, thread
migration, connection churn.
- [ x ] Multi-connection, multi-process test, verify no cross-contamination
- [ x ] Handle connection close, cleanup stale per-connection state
- [ x ] Backpressure/drop handling if the ring buffer outpaces the agent
- **Exit:** sustained load test, zero misattributed calls.

### Phase 6: Observability output
**Goal:** useful, not just correct.
- [ x ] Prometheus metrics: request count, latency histogram, error rate,
  labeled by method and pod/service
- [ x ] Grafana dashboard
- **Exit:** dashboard showing live gRPC traffic from the test workload.

### Phase 7: Kubernetes packaging
**Goal:** deployable like a real cluster tool, not a hardcoded demo.
- [ x ] DaemonSet manifest, privileged/CAP_BPF requirements documented
- [ x ] Discover target Go processes on a node: process discovery, binary
  path resolution, per-process attach/detach lifecycle
- [ x ] RBAC, resource limits
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
| 0: Environment | Done | Toolchain and trivial uprobe verified |
| 1: Go symbol resolution & RET sites | Done | See Notes & learnings below; stripped-binary case verified after the fact (FileOffset fix) |
| 2: TLS interception | Done | Live Read/Write capture verified on Go 1.16.15 and 1.24.5, both unstripped and -s -w; exact byte totals, zero capture errors/drops |
| 3: HTTP/2 demux | Done | Incremental framing + stream routing; independent evaluator and live stripped/unstripped TLS capture pass |
| 4: HPACK & gRPC semantics | Done | Independent evaluator and live Go 1.25 stripped/unstripped grpc-go TLS tests pass: 10 exact records each |
| 5: Correlation at scale | Done | Live stripped/unstripped load: 7,920 RPCs each across three processes, zero misattribution; ring-pressure recovery verified |
| 6: Observability output | Done | Prometheus endpoint, bounded workload/method metrics and provisioned Grafana; live regular/-s -w matrix and panel queries verified |
| 7: Kubernetes packaging | Done | Live kind deployment: automatic process/pod attribution, stripped workloads, restart/exec lifecycle, cleanup and RBAC verified |
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

### 2026-10-13: Phase 2 TLS capture verified

- `bpf/tls_capture.bpf.c` attaches ordinary uprobes at Read/Write entry and
  all discovered RET sites. No return-address patching.
- ABIInternal: receiver AX, slice pointer BX, length CX; successful byte
  count AX at RET. Go 1.16 stack ABI: receiver/slice at SP+8/+16/+24,
  successful byte count at SP+40 at RET.
- Current goroutine: R14 for register ABI; Linux amd64 FS-base minus eight
  for Go 1.16, with FS-base read through kernel BTF/CO-RE. Goroutine-ID
  offset comes from target DWARF when available, or a release-specific
  layout when stripped.
  Stack bounds layout is validated from DWARF when available. Stripped
  targets use verified standard-release layouts for Go 1.16–1.26.
  Go 1.23–1.24 place goid at 160; other supported releases use 152.
  Sources: [Go 1.23 runtime layouts](https://github.com/golang/go/blob/go1.23.0/src/runtime/runtime2.go),
  [Go 1.25 runtime layouts](https://github.com/golang/go/blob/go1.25.0/src/runtime/runtime2.go).
  Unknown stripped versions fail explicitly; custom runtime forks require
  DWARF or a separately verified layout.
- In-flight calls are keyed by PID, goid, direction, and the return slot's
  offset from `g.stack.hi`. This separates recursive calls, tolerates
  morestack entry retries, and survives stack relocation. Stack-backed
  buffer pointers are likewise rebased at return.
- Both directions capture only the returned successful byte count, including
  bytes returned alongside an error. Events include connection identity,
  entry/return monotonic timestamps, and explicit total/captured lengths.
- Payload limit: 4096 bytes per event. Truncation and failures are counted;
  Phase 3 adds bounded chunking (see the next entry); per-connection loss
  attribution/recovery remains a Phase 5 task.
- Agent/BPF build and Go tests checked locally. Standalone TLS fixture
  passes on Go 1.16.15 and Go 1.25.0; metadata and RET-site tests cover both
  stripped (`-s -w`) and unstripped builds. Extended layout/symbol/RET
  matrix also passes on Go 1.23.0, 1.24.0, and 1.26.0, exercising both
  runtime layout transitions. Live root-run matrix passes on Go 1.16.15
  and Go 1.24.5, each unstripped and stripped: all eight TLS endpoints
  capture plaintext HTTP/2 prefaces in both directions, exact byte totals,
  zero memory/identity failures, zero map errors, and zero ring drops.
  The newer run was initially mislabeled go1.25.0 in its filename because
  querying Go version inside the module auto-selected a different toolchain
  from the standalone build. Target build metadata confirms Go 1.24.5;
  the script now queries version under the same environment as the build.
  Each run exercises 160 truncated calls from the intentional 8192-byte
  payload case. Logs: `build/capture-goVERSION-MODE.log`.
- The live script's initial readiness check relied on `rg`, which was
  unavailable in the terminal; replaced it with `grep`. Rootless agent
  startup requires elevated BPF/perf privileges; run the live matrix with
  root and `AXON_GO116`.
- First root-run attempt reached the kernel verifier, which rejected a
  signed copy-length range. Fixed by explicitly masking/bounding the small
  unsigned length branch; the subsequent root-run load succeeded.
- Existing `testtargets/client` and `testtargets/server` also rebuilt with
  `-s -w` and verified to exchange all three messages over TLS.
- Live checker runs stripped and unstripped builds, and requires plaintext
  HTTP/2 prefaces on all eight TLS endpoint
  connections in both directions, exact per-endpoint byte totals, zero
  memory/map/ring errors, and exercised truncation. All four runs passed
  the independent `scripts/verify-phase2.py` log checker.

### 2026-10-24: Phase 3 framing and stream demultiplexing verified

- `internal/h2demux` incrementally handles split client prefaces, partial
  9-byte headers and payloads, and multiple frames in one TLS call. It
  requires an initial non-ACK SETTINGS frame in each observed direction;
  attach before connection traffic starts rather than guessing midstream
  alignment. Frames retain connection identity, direction, stream ID,
  per-direction wire sequence, flags, and owned raw payload bytes.
- Reassembly keys are `(PID, tls.Conn pointer, direction)`. This corrects
  the original plan's goid component: reads/writes of the same connection
  can come from different goroutines. GoID and entry timestamp correlate
  chunks of one call. Stream 0 control frames are retained; unknown frame
  types are preserved, the reserved stream-ID bit is masked, and incomplete
  header blocks require same-stream CONTINUATION without interleaving.
- Framing checks cover stream-ID/size constraints, padding, priority
  prefixes, SETTINGS values, and incomplete EOF. This is a framing layer,
  not the full HTTP/2 stream state machine or HPACK/gRPC interpretation.
- BPF HTTP/2 mode now emits 4096-byte chunks with call offsets, up to
  256KiB per successful TLS call. Old 56-byte event headers still decode;
  new headers are 64 bytes. Raw mode keeps its 4KiB diagnostic preview.
  Chunk gaps, overlaps, mixed calls and immediate completed-call duplicates
  fail closed. Configurable frame/connection bounds limit retained state.
- `axon-agent -mode h2` writes JSON frames with base64 payloads. It stops
  on capture memory/map errors, ring drops, or calls beyond the capture
  bound; it never silently stitches across known missing bytes. Missing
  bytes cannot be recovered by scanning for plausible headers. Automatic
  loss attribution, connection cleanup/reuse, and arbitrary concurrent
  same-direction writers remain Phase 5 work. Feed assumes wire order.
- `-replay EVENTS.jsonl` exercises the same agent/parser pipeline without
  root. `testtargets/h2phase3` sends streams 1 and 3 over real TLS, including
  fragmented headers, interleaved DATA, PINGs, 10KB/9KB frame payloads and
  >4KiB writes. Its optional recorder is test instrumentation with synthetic
  identity; rootless replay does not validate live probes.
- After implementation, an independent evaluator wrote parser, chunk
  decoding and CLI/replay tests. It found completed-call duplication and
  ambiguous preface detection for length bytes beginning with `P`; both
  were fixed and covered by regression tests. Race tests and repeated
  bounded segmentation fuzzing passed, including a final 60,982 executions.
  The golden checker rejects missing/duplicated frames, corrupted payloads,
  wrong stream IDs and changed sequence numbers.
- `scripts/evaluate-phase3.sh` passes on stripped/unstripped rootless TLS
  fixture builds: 30 exact golden frames per run, two streams, both TLS
  endpoints and directions. `go test ./...` (including the Go 1.16 target
  metadata matrix) and `go vet ./...` pass.
- Live `scripts/test-phase3.sh` passes on Go 1.24.5, unstripped and `-s -w`:
  verifier accepted the new BPF program, all 30 frames match the independent
  golden checker, and memory/identity/map/ring/truncation counters are zero.
  Evidence: `build/phase3/live-go1.24.5-{unstripped,stripped}.{jsonl,log}`.
  The new chunked capture path has not yet been live-tested on Go 1.16;
  its metadata/stripped resolution checks pass and the live matrix accepts
  `AXON_GO116` for that additional run.

### 2026-10-24: Phase 4 gRPC reconstruction and independent evaluation

- `internal/grpc` consumes ordered frames from Phase 3. Each connection
  direction has its own `x/net/http2/hpack` decoder, retained across streams
  and non-gRPC header blocks. HEADERS/CONTINUATION assembly strips padding
  and priority prefixes; PUSH_PROMISE blocks still update HPACK state.
  Peer SETTINGS controls allowed dynamic table updates within a local cap.
- Calls correlate by PID, TLS connection pointer and stream ID. Request
  direction is learned from pseudo-headers, so both client/server capture
  and response-before-request event arrival work. Method is `:path`.
  Request/response headers and final trailers are retained separately.
- DATA payloads pass through a per-stream/direction five-byte gRPC envelope
  parser. Message prefixes/bodies can span frames or share a frame. Unary
  and streaming calls, empty messages, and trailers-only errors are handled.
  Payloads remain opaque protobuf/JSON bytes; gzip messages are decompressed
  individually. Unsupported compression fails explicitly, never guessing.
- JSON call records include method, messages (base64 bytes), compression,
  gRPC status/source/message, HTTP status, decoded sizes and
  gRPC wire sizes (including five-byte message envelopes, excluding HTTP/2
  and TLS overhead). Missing gRPC status uses the HTTP fallback mapping or
  UNKNOWN; RST_STREAM emits an explicitly reset call with its mapped status.
  Percent decoding preserves literal `+` and retains malformed escapes.
- Phase 3 frames now carry capture-clock observation bounds. Latency is
  request HEADERS observation completion to response end observation, not
  application execution time. Unknown or reversed timestamps produce
  `timing_valid=false` instead of unsigned underflow or invented latency.
- Resource defaults: 64KiB compressed/expanded header blocks, 64KiB maximum
  negotiated dynamic table, 4MiB wire/decompressed message, 8MiB retained
  decoded payload per call, 1024 messages per call, 4096 stream slots per
  connection and 1024 connections. Completed stream tombstones remain until
  `Forget`; automatic lifecycle/reuse hardening remains Phase 5. Known gaps,
  malformed headers, incomplete messages and open gRPC calls at EOF fail
  explicitly. Raw and HTTP/2 modes remain available.
- `testtargets/grpcphase4` uses actual grpc-go over TLS with one connection:
  four concurrent unary calls (ordinary, large, gzip, INVALID_ARGUMENT)
  and one bidirectional stream with three messages each way. No protobuf
  generation tool is needed: it uses protobuf `BytesValue` and a service
  descriptor. Its optional recorder produces rootless test inputs with
  synthetic identities; live tracing runs without the recorder flag.
- After implementation, an independent evaluator added HPACK, framing,
  correlation, payload ownership, limits, timing, CLI and replay tests. It
  found ambiguous duplicate content-type/grpc-encoding handling; critical
  duplicates now fail explicitly and the regression passes. Race tests and
  162,653 bounded fuzz executions passed. Both stripped and unstripped
  real-TLS fixture replays produced 10 exact golden call records (five
  logical calls observed at both endpoints), including exact protobuf
  payloads, gzip flags, status 3/error text, sizes and timing. Corrupted
  inputs and golden output are rejected. See `scripts/evaluate-phase4.sh`,
  `scripts/verify-phase4.py` and `build/phase4-evaluation/REPORT.md`.
- Full repository tests and vet pass, including the existing Go 1.16
  target inspection matrix. The actual grpc-go fixture is module-based
  and builds with the module's Go 1.25+ toolchain; standalone Phase 2/3
  fixtures remain available for older-ABI checks.
- Live `scripts/test-phase4.sh` passed on Go 1.25.0 with unstripped and
  `-s -w` binaries. The fixture ran with its recorder disabled: actual TLS
  plaintext came from the existing uprobes. Each output matches the
  independent checker's ten exact golden records, and memory/identity,
  map-error, ring-drop and truncation counters are zero. Target build
  metadata independently confirms Go 1.25.0 and stripping flags. Evidence:
  `build/phase4/live-go1.25.0-{unstripped,stripped}.{jsonl,log}`.

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


### 2026-10-24: Phase 5 lifecycle, isolation and load evaluation

- Capture identities now include PID, process birth time, TLS pointer and
  capture generation. Per-direction call sequences and chunk offsets detect
  missing or reordered data. TLS Close releases connection state; idle expiry,
  connection limits and a retained-buffer budget bound userspace state.
- Completed gRPC streams release their buffers and stream slots. A request-ID
  watermark prevents reopening completed streams; 4096 is a concurrent stream
  bound, not a connection lifetime limit. This supersedes Phase 4's tombstone note.
- Correlated producer failures quarantine the affected connection; unknown
  capture failures stop parsing globally. Same-direction overlapping TLS calls
  are quarantined rather than assigned a guessed wire order. Recovery requires
  a new connection generation; arbitrary midstream resynchronization is unsupported.
- Rootless evaluator: race checks, 5,000 calls on one connection, malformed/lost
  capture cases, three-process real-TLS recorder/replay in regular and stripped
  builds (4,680 RPCs / 9,360 endpoint observations each).
- Live Go 1.25.0: regular and `-s -w` builds each produced 15,840 exact endpoint
  records for 7,920 RPCs across three processes and 396 TLS incarnations. All six
  producer error counters were zero. Observed pointer reuse: 229 / 207;
  goroutine thread changes: 40,333 / 41,790. Both ended with zero active sessions
  and zero retained parser bytes.
- Deliberately stopping the consumer filled a 256KiB ring (8 drops). The checker
  accepted only correctly attributed surviving records and required all 20
  observations from the subsequent healthy worker. Final active/retained state
  was zero. Kernel process cleanup conservatively checks process birth identity.
- Commands: `./scripts/evaluate-phase5.sh` and
  `sudo env PATH="$PATH" ./scripts/test-phase5.sh`.
  Evidence: `build/phase5-evaluation/REPORT.md`, `build/phase5/`.


### 2026-10-24: Phase 6 metrics and Grafana

- Added opt-in Prometheus `/metrics` and final text snapshots. Completed RPCs,
  status, latency histograms, untimed observations and protobuf byte totals have
  method/endpoint-role and static target service/namespace/pod labels. Selecting
  one role avoids double-counting both sides of a local RPC. Unknown timing is
  excluded from histograms; interrupted captures are not labeled successful.
- Metrics state is bounded by method/role/status groups, with visible overflow.
  Capture producer errors and session cleanup/resource snapshots are exported
  separately so capture loss cannot masquerade as a healthy RPC error rate.
- Provisioned a Linux loopback Docker Compose Prometheus/Grafana stack and 13
  panels with workload/method/role filters, rate/error/p95 queries and capture
  health. Static labels require correctly scoped target selection; automatic
  Kubernetes attribution remains Phase 7.
- Rootless regular/-s -w TLS recorder/replay tests checked 10 endpoint records
  each against HTTP scrapes and final snapshots. Live eBPF tests checked 200
  records each (100 RPCs, including 20 RPC errors), all six producer counters
  zero, 40 connections closed, zero retained bytes. Byte counters and every
  histogram bucket were compared with JSON call records.
- Actual Prometheus scrapes, Grafana datasource access and all 13 dashboard
  queries validated; throughput, error and latency series had live values.
  `scripts/demo-phase6.sh` provides repeatable live traffic until Ctrl-C.
- Evaluation: scripts/evaluate-phase6.sh, scripts/test-phase6.sh and
  scripts/verify-phase6-dashboard.py. Evidence: build/phase6-evaluation/REPORT.md.


### 2026-10-24: Phase 7 Kubernetes packaging and node discovery

- Added artifact-based runtime image, a node DaemonSet, service account, read-only
  pod-list RBAC, headless metrics Service, probes and resource requests/limits.
  Default capture is bounded to four per-process workers, each with independent
  ABI configuration, BPF maps/ring and protocol state.
- Kubernetes API refreshes are node-filtered, paginated and bounded; CA-verified
  requests re-read rotated projected tokens. Exact cgroup runtime IDs map node
  processes to running containers. Own/system pods and unsupported binaries are
  skipped. Optional label/namespace filters scope discovery without manual PIDs
  or application binary paths.
- Process identity includes PID/birth/stack/executable inode; executable FDs are
  pinned. A kernel sched_process_exec guard prevents joining two runtimes after
  exec, even with unchanged PID and executable inode. Node-namespace PID/TID
  resolution uses CO-RE ancestor PID numbers, covering kind's nested namespace.
- RPC metrics accept discovered workload labels under one global cardinality
  bound. Node capture-health counters accumulate across worker replacement;
  discovery reports active/resident/deferred workers and metadata/failure state.
- Live Kubernetes v1.32.2 kind on Linux 6.8: 40 RPCs / 80 exact client/server
  records, three pod identities, normal and -s -w Go 1.25 images, pod replacement
  and same-PID self-exec. Payload/status/PID/pod UID/container/service attribution
  and metrics verified; eight TLS connection incarnations opened and closed,
  zero capture errors, retained bytes zero. Non-Go exclusion and list-pods /
  denied-secrets RBAC checked. Rootless/race tests cover metadata pagination,
  token rotation, cgroup v1/v2 formats, PID/exec identities and deleted images.
- Phase 6 live regular/stripped regression also passes after namespace changes.
  Limitations: polling can miss short-lived targets and pre-attach traffic;
  existing HTTP/2 sessions cannot resynchronize arbitrarily. Privileged baseline
  evaluated; capability-only operation and a production multi-node cluster are
  not claimed. Full guide: deploy/phase7/README.md.
- Commands: scripts/evaluate-phase7.sh and scripts/test-phase7.sh. Evidence:
  build/phase7-evaluation/REPORT.md, agent.log, identities.json, calls.jsonl,
  active.metrics/final.metrics, batches.json and rbac.txt.

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

Phases 1–7 are complete. Live TLS plaintext capture is verified on
Go 1.16.15 and Go 1.24.5, both unstripped and built with `-s -w`.
HTTP/2 reassembly and stream routing are verified on live stripped and
unstripped Go TLS traffic. HPACK/gRPC reconstruction is independently evaluated and verified on
live Go 1.25 TLS traffic, both regular and `-s -w` binaries.
Lifecycle, loss handling and scale hardening are verified on live multi-process
traffic, including `-s -w` builds and forced ring-buffer pressure.
See [EPIC.md](./EPIC.md).

## Phase 2: build and run

Linux amd64, kernel BTF, clang/LLVM, bpftool, and Go 1.25+ are required to
build the agent. The target can use Go 1.16's stack ABI or the register ABI
in Go 1.17+. Targets built with `-ldflags="-s -w"` are supported. Symbols
and RET sites
come from `.gopclntab` when `.symtab` is absent. Runtime layout comes from
target DWARF when present, or a version-specific Linux amd64 release
layout for Go 1.16–1.26 when stripped. Unknown stripped Go versions fail
with an explicit error instead of using an unverified offset. Custom Go
runtime forks need DWARF or a verified layout entry.

```sh
./scripts/build-phase2.sh
go build -o build/tls-client ./testtargets/client
sudo ./build/axon-agent -bin build/tls-client
# Start the TLS server and run build/tls-client in another terminal.
```

Add `-pid PID` to restrict capture to one process. Each output line includes
PID, TID, goroutine ID, TLS connection pointer, direction, successful byte
count, and captured plaintext. Capture is capped at 4096 bytes per call;
`n > captured` explicitly signals truncation in raw mode. HTTP/2 mode
uses chunked capture instead (below). Ctrl-C
prints memory/identity failures, map failures, ring drops, and truncations.

The repeatable ABI matrix uses a standalone fixture with four concurrent
TLS connections, the HTTP/2 preface, and large writes. It verifies the
preface on every endpoint in both directions, exact successful byte totals,
and zero capture errors/drops. The fixture echoes the preface; it does not
implement HTTP/2 frames yet.

```sh
AXON_GO116=/path/to/go1.16/bin/go go test ./...
sudo env PATH="$PATH" AXON_GO116=/path/to/go1.16/bin/go ./scripts/test-phase2.sh
```

Both unstripped and `-s -w` fixtures run for each compiler. Set
`AXON_GO_EXTRA` to colon-separated compiler paths to extend either matrix.
Capture logs
are saved as `build/capture-goVERSION-MODE.log`. The four-run live
matrix passed with exact per-endpoint byte totals and zero capture
errors or ring drops.


## Phase 3: HTTP/2 frames

Build the agent/BPF object with `./scripts/build-phase2.sh`, then attach
before the target opens its HTTP/2 connection:

```sh
go build -ldflags='-s -w' -o build/h2fixture ./testtargets/h2phase3
sudo ./build/axon-agent -bin build/h2fixture -mode h2
# In another terminal, from the repository root:
./build/h2fixture
```

Each JSON line identifies PID, connection pointer, read/write direction,
stream ID, wire sequence, frame type/flags, length, and base64 payload.
Streams 1 and 3 share a connection; stream 0 holds control frames. Payloads
remain encoded HTTP/2 bytes; use `-mode grpc` for reconstructed calls below.

Reassembly follows the connection and direction even if the goroutine
changes. HTTP/2 mode captures 4KiB chunks up to 256KiB per TLS call; a frame
can span several calls. Calls beyond that bound, capture errors and ring
loss stop parsing explicitly. Initial connection prefaces/SETTINGS are
required; attaching in the middle of established traffic is unsupported.
The agent isolates connection lifetimes and rejects overlapping same-direction
TLS calls when wire order cannot be established. The library also exposes
frame/connection limits and `Forget` for callers managing lifetimes.

The independent evaluator writes its artifacts under
`build/phase3-evaluation/`. It runs race tests and checks the stripped and
unstripped real-TLS recorder/replay pipeline against 30 exact golden frames:

```sh
./scripts/evaluate-phase3.sh
# Optional additional fuzzing:
go test ./internal/h2demux -run='^$' -fuzz=FuzzEvaluatorSegmentation -fuzztime=5s -parallel=2
```

For end-to-end live eBPF validation (also verified stripped/unstripped):

```sh
sudo env PATH="$PATH" ./scripts/test-phase3.sh
# Add AXON_GO116=/path/to/go1.16/bin/go to include the old ABI in the live matrix.
```

Rootless replay records TLS calls with a fixture-only recorder and synthetic
connection/goroutine IDs; it validates framing, not probe placement. Both
replay and live runs use `scripts/verify-phase3.py`, which checks exact
payloads, connection/direction separation, stream ordering and completeness.

You can also replay recorded TLS events directly:

```sh
./build/axon-agent -mode h2 -replay build/phase3-evaluation/events-stripped.jsonl
```


## Phase 4: gRPC call records

`-mode grpc` adds HPACK decoding, gRPC message framing and request/response
correlation to the same TLS capture pipeline:

```sh
./scripts/build-phase2.sh
go build -ldflags='-s -w' -o build/grpcfixture ./testtargets/grpcphase4
sudo ./build/axon-agent -bin build/grpcfixture -mode grpc
# In another terminal, from the repository root:
./build/grpcfixture
```

Each JSON line is a completed call at one observed TLS endpoint: method,
stream and connection identity, request direction, headers/trailers,
request/response messages, decoded and wire byte counts, gRPC/HTTP status,
status source/message and capture-based latency. Tracing both endpoints of
one call produces two observations. Protobuf payloads remain opaque bytes
encoded as base64; gzip is decoded per message. Wire sizes include each
five-byte gRPC envelope, but exclude HTTP/2 and TLS overhead.

The latency interval runs from captured request HEADERS completion to
captured response completion. `timing_valid` is false if capture clocks
are missing or the interval cannot be established. This is an observation
interval, not a measurement of application handler execution.

HPACK state is independent per connection direction and persists across
streams. The parser supports fragmented/coalesced messages, streaming,
trailers-only errors, HTTP fallback status and RST cancellation. Missing
status never becomes success by default. Identity and gzip compression are
supported; unknown codecs fail explicitly. Defaults bound each message to
4MiB (wire and decoded), each call to 8MiB decoded payload and 1024 messages,
and each header block/list to 64KiB. The Phase 3 256KiB TLS-call capture
bound still applies. Completed streams release their state; the 4096-stream
limit applies to concurrent retained streams.

Run the independent evaluator (no root required):

```sh
./scripts/evaluate-phase4.sh
```

It runs race/fuzz checks and actual grpc-go TLS traffic from both stripped
and unstripped fixture binaries, recorded and replayed through the full
agent pipeline. Each replay must match ten exact call records, including
concurrent unary, streaming, large messages, gzip and a status-3 error.
Evidence and the evaluator report are under `build/phase4-evaluation/`.
Recorder/replay testing does not validate probe placement or the kernel.

For live capture of the uninstrumented fixture:

```sh
sudo env PATH="$PATH" ./scripts/test-phase4.sh
```

The live matrix passed on Go 1.25.0 with both regular and `-s -w`
binaries: ten exact call records each and zero capture errors/drops.
It builds the actual module-based grpc-go fixture with the selected
Go 1.25+ toolchain.
The same independent golden checker verifies its output. Replay is also
available directly:

```sh
./build/axon-agent -mode grpc -replay build/phase4-evaluation/events-stripped.jsonl
```


## Phase 5: lifecycle and sustained load

Each capture session is identified by PID, process birth time, TLS connection
pointer and generation. Sequence checks detect gaps; TLS Close and idle expiry
release parser state. Defaults allow 1024 sessions, two minutes of inactivity
and an estimated 128MiB retained-buffer budget. Configure these with
`-max-connections`, `-idle-timeout` and `-buffer-bytes`; `-ring-bytes` configures
the kernel ring (default 16MiB). The buffer estimate is not a total RSS limit.

Correlated capture loss, malformed streams and ambiguous overlapping TLS calls
quarantine that connection. New connections continue normally. Unattributed
capture errors stop parsing globally. Expired, evicted or damaged sessions
cannot resume midway through a connection; reconnect to capture a new generation.
A `runtime_stats` JSON line on stderr reports cleanup, loss and resource usage.

```sh
./scripts/evaluate-phase5.sh
sudo env PATH="$PATH" ./scripts/test-phase5.sh
```

The rootless evaluator checks identity isolation, PID/pointer reuse, missing
chunks, cleanup, bounded state and 5,000 calls on one connection. Its real-TLS
fixture writes unique payloads and manifests; a separate checker validates
request/response bytes, process/connection attribution and duplicates.
The live test runs three processes with concurrent streams and connection churn,
for both normal and `-s -w` binaries, then deliberately fills the ring and checks
recovery on a fresh connection. On Go 1.25.0 each load run verified 7,920 RPCs
(15,840 client/server observations) with zero misattribution or producer errors.
Evidence is saved under `build/phase5/` and `build/phase5-evaluation/`.

## Phase 6: Prometheus and Grafana

Run the local live demo (Docker and the existing BPF toolchain required):

```sh
sudo env PATH="$PATH" ./scripts/demo-phase6.sh
```

Open **http://127.0.0.1:3000/d/axon-grpc**. It generates real grpc-go TLS
traffic from a `-s -w` fixture, captures it with uprobes and scrapes the agent
into Prometheus. Grafana is provisioned automatically with request rate, error
ratio, p95 latency, status counts, capture loss, connection lifecycle, buffer
usage and scrape health. Ctrl-C stops the traffic and agent; dashboard services
stay up. To stop those services:

```sh
docker compose -f deploy/observability/compose.yaml down
```

To trace your own workload instead, start the stack and an agent:

```sh
docker compose -f deploy/observability/compose.yaml up -d
sudo ./build/axon-agent -bin /absolute/path/to/server -pid 1234 -mode grpc \
  -metrics-listen 127.0.0.1:9108 -service orders -namespace dev -pod orders-1
curl http://127.0.0.1:9108/metrics
```

Workload labels describe the **target**, not the agent. They can also come from
`AXON_SERVICE`, `AXON_NAMESPACE` and `AXON_POD`; empty values become `unknown`.
Labels are static per agent: scope capture with `-pid` when processes belong to
different workloads. Use Phase 7 discovery mode below for automatic process/pod attribution.
The provided Compose stack uses Linux host networking and loopback ports 3000,
9090 and 9108; Grafana permits anonymous local viewing. Metrics HTTP is opt-in
and requires `-mode grpc`. JSON call output continues on stdout.

Metric semantics:

- `axon_grpc_requests_total{method,role,grpc_status,service,namespace,pod}` counts
  completed endpoint observations. `role=server` means the request was read;
  `role=client` means it was written. The dashboard defaults to server so capturing
  both endpoints does not double totals. Streaming counts once per completed RPC.
- `axon_grpc_request_duration_seconds` is a classic histogram of the captured
  request-to-response interval, not handler execution time. Invalid timing is
  counted separately in `axon_grpc_requests_without_timing_total` and excluded
  from latency; valid zero-duration observations remain valid.
- Error ratio is nonzero `grpc_status` observations divided by all completed
  observations, using counter rates. Missing gRPC status retains the parser's
  fallback status. Interrupted captures do not become successful calls: inspect
  `axon_capture_errors_total`, invalidated/incomplete sessions and scrape health.
- Request/response byte counters contain decoded protobuf bytes. No payload,
  PID, connection pointer or stream ID is used as a metric label.
- `-metrics-max-series` bounds method/role/status groups (default 1024), each of
  which expands into counter and histogram time series. New groups beyond the
  limit are omitted and counted in `axon_metrics_dropped_observations_total`;
  established groups keep accumulating. Method/workload labels are limited to
  256 bytes. Counts reset on agent restart; dashboard rate queries handle resets.

`-metrics-output /path/to/final.prom` saves a final text snapshot, including in
rootless replay mode. The HTTP handler scrapes synchronized snapshots without
reading mutable parser state.

```sh
./scripts/evaluate-phase6.sh                    # rootless TLS replay + HTTP scrape
sudo env PATH="$PATH" ./scripts/test-phase6.sh  # live regular and -s -w matrix
python3 scripts/verify-phase6-dashboard.py      # with stack and recent live data
```

The live matrix checks 200 endpoint observations per binary, including errors,
streaming and gzip, against both final and HTTP-scraped metrics. The checker
compares every count, status, byte total and latency bucket with the JSON calls.
Dashboard validation executes all 13 panel queries against scraped traffic and
queries Prometheus through Grafana. Evidence: `build/phase6/` and
`build/phase6-evaluation/`.

Query/provisioning references: [Prometheus histograms](https://prometheus.io/docs/practices/histograms/)
and [Grafana provisioning](https://grafana.com/docs/grafana/latest/administration/provisioning/).


## Phase 7: automatic Kubernetes node discovery

The DaemonSet discovers Go TLS processes automatically, resolves their container
executables through `/proc/PID/exe`, and attaches by PID. Each process has separate
ABI, BPF and parser state. Calls and metrics receive Kubernetes pod, namespace,
service and container attribution. PID reuse, pod replacement and same-PID exec
are guarded; regular and `-s -w` workloads are supported.

```sh
./scripts/build-image.sh
kind load docker-image axon:phase7 --name YOUR_CLUSTER
kubectl apply -f deploy/daemonset.yaml
kubectl -n axon logs daemonset/axon -f
```

For reproducible validation in a separate local cluster:

```sh
./scripts/evaluate-phase7.sh
./scripts/test-phase7.sh
```

The live kind evaluation verified 40 RPCs (80 endpoint observations), including
errors, across regular/stripped pods, a pod rollout and same-PID self-exec.
Exact payload, PID, pod UID, container ID, service metrics and lifecycle cleanup
passed. The service account can list pods and cannot read secrets. The test uses
its own kubeconfig; your default Kubernetes context is preserved.

The baseline requires a privileged Linux amd64 node agent with hostPID and BTF;
it defaults to four capture workers per node. Polling may miss short-lived
processes or traffic before attachment, and established connections need to
reconnect for complete HTTP/2 reconstruction. See
[deployment instructions](./deploy/phase7/README.md) for permissions, resource
bounds, namespace/label selection, metrics scraping and cleanup.

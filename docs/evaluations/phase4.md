# Independent Phase 4 evaluation

Command: `./scripts/evaluate-phase4.sh`

Result: PASS after implementation fixed evaluator-discovered duplicate critical metadata ambiguity. `content-type`, `grpc-encoding`, and `grpc-status` duplicates now reject rather than silently selecting the first value.

- Race tests passed for grpc, h2demux, tlscapture and agent packages.
- Message segmentation fuzz: 162,653 executions in approximately 3 seconds.
- Actual grpc-go TLS traffic from unstripped and `-s -w` fixture binaries, recorded after TLS and replayed through the agent, produced 10 exact call records each.
- Golden checks verify two endpoint identities, four concurrent unary requests and one bidirectional stream per endpoint, protobuf payload bytes, gzip in both directions, status/error message, decoded/wire sizes and consistent observation latency.
- Corrupted replay JSON and intentionally incorrect golden size totals were rejected.
- Unit evaluation covers connection/direction HPACK state, non-gRPC history, PUSH_PROMISE, SETTINGS table-size controls, padded priority HEADERS+CONTINUATION, response-first correlation, per-byte message framing, zero-length messages, wire/decompression/count limits, unsupported codec, partial EOF, critical duplicates, pseudo-header ordering, RST cancellation, HTTP fallback, retained payload ownership, multi-TLS frame timestamp bounds, JSON output errors and replay EOF rejection.

Scope: the recorder uses synthetic connection/goroutine identifiers and is a rootless test input source. These checks prove parser/output semantics, not kernel verifier/probe attachment or live BPF capture. Run `scripts/test-phase4.sh` separately for live capture.


## Subsequent live validation (2026-10-24)

The user ran `sudo env PATH="$PATH" ./scripts/test-phase4.sh`. The primary
agent inspected both resulting logs and independently ran the evaluator's
`verify-phase4.py` on both JSON outputs. Both passed:

- Actual target metadata confirms Go 1.25.0, regular and `-s -w` builds.
- The target fixture ran without `-events`; no recorder instrumentation
  supplied the captured plaintext.
- Each live output contains ten exact calls across both TLS endpoints,
  with matching methods, protobuf payloads, gzip, sizes, status and timing.
- All memory/identity, map-error, ring-drop and truncation counters are zero.
- Evidence: `build/phase4/live-go1.25.0-unstripped.jsonl`,
  `build/phase4/live-go1.25.0-stripped.jsonl` and their matching `.log` files.

This supplements, rather than changes, the rootless evaluator's scope above.

# Phase 6 evaluation — 2026-10-24

Result: PASS.

## Functionality

Opt-in `/metrics`, final Prometheus text snapshots, completed request counters,
status/error labels, classic latency histograms, untimed/byte counters, bounded
metric groups, static target workload labels and synchronized capture-health
snapshots. Client/server roles are distinct; dashboard defaults to server.

## Evidence

- `go test ./...`, `go vet ./...`, race tests for metrics/agent, shell syntax,
  `git diff --check`: passed. Tests include concurrent scrapes/cardinality,
  label escaping, exact cumulative buckets, unknown versus valid zero timing,
  producer/runtime snapshots, occupied port rejection and listener cleanup.
- `scripts/evaluate-phase6.sh`: real grpc-go TLS recorder/replay for regular
  and stripped binaries. Ten endpoint observations and six metric groups each;
  JSON calls, HTTP exposition and final snapshot match exactly. Existing Phase 4
  golden checker also verified unary/streaming/gzip/error/payload/status/timing.
- `AXON_AGENT_DOCKER=1 scripts/test-phase6.sh`: actual live host eBPF capture
  via a temporary privileged Debian container, no recorder enabled. Regular and
  `-s -w` Go 1.25 fixtures each produced 200 endpoint observations for 100 RPCs,
  including 20 status-3 errors. Both HTTP scrapes and final snapshots match every
  reconstructed count, status, protobuf byte total, histogram count/sum/bucket.
  All six producer error counters zero. Forty connection incarnations closed
  per run, zero invalidations/incomplete sessions/retained parser bytes.
- `promtool check config` and `promtool check metrics`: passed.
- Actual Prometheus 3.5.0 scrapes and Grafana 12.2.0 provisioning validated.
  All 13 panel queries returned scraped data; throughput, error and p95 latency
  returned nonzero finite signals. Grafana datasource proxy successfully queried
  Prometheus. Latest observed cumulative totals: client/server each 80 successes
  and 20 errors. Browser visibly rendered method rates, error ratios, latency
  and status counts. See dashboard.png and dashboard-queries.json.

## Reproduce

Rootless: `./scripts/evaluate-phase6.sh`.
Live: `sudo env PATH="$PATH" ./scripts/test-phase6.sh`.
Dashboard stack: `docker compose -f deploy/observability/compose.yaml up -d`.
After live test: `python3 scripts/verify-phase6-dashboard.py`.
Interactive live demo: `sudo env PATH="$PATH" ./scripts/demo-phase6.sh`.

## Scope and state

Workload labels are static per agent; use a PID scope for processes from different
workloads. Automatic Kubernetes process/pod discovery is Phase 7. Latency measures
capture intervals; incomplete/lost capture can omit RPC observations. Label-group
limits omit new groups with an explicit drop counter. No cross-endpoint dedup is
claimed: select one role for request totals. Dashboard/Prometheus remain running
on loopback ports 3000/9090; evaluation capture containers and fixtures exited.
The dashboard contains recent recorded live traffic; start the demo for continuous
new traffic. `docker compose -f deploy/observability/compose.yaml down` stops it.

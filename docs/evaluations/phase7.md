# Phase 7 evaluation — 2026-10-24

Result: PASS. The final packaged image is deployed as a DaemonSet in the dedicated
`axon-phase7` kind cluster (Kubernetes v1.32.2, Linux 6.8, amd64, cgroup v2).

## Delivered

- Runtime Docker image and build script, node DaemonSet, projected service
  account, least-privilege pod-list RBAC, headless metrics Service, probes,
  privileged/hostPID requirements and resource requests/limits.
- Automatic node-local Kubernetes process discovery via full cgroup container IDs,
  procfs executable resolution, per-process Go ABI/capture workers, PID/exec
  identity checks and pinned executable descriptors.
- In-kernel sched_process_exec guard and CO-RE ancestor PID namespace resolution.
  Regular and -s -w binaries receive independent maps/rings/parser state.
- Discovered workload labels on RPC metrics and JSON calls. Bounded global
  metric groups, capture workers, BPF maps and parser buffers. Metadata failure,
  active/resident/deferred workers, exec transitions and capture-health metrics.

## Verification

`./scripts/test-phase7.sh` passed on the final image:

- Deployed regular and stripped Go 1.25 fixtures; automatically attached by PID
  without supplying app binary paths or PIDs.
- 40 RPCs, 80 exact client/server observations, including eight logical status-3
  errors, across three pod identities and four traffic batches.
- Verified request/response protobuf bytes, gRPC method/status, node PID, namespace,
  pod name/UID, container ID/name and service labels against API metadata and
  independent `/proc/PID/cgroup` reads.
- Prometheus request counts, protobuf byte counters, latency count/sum and every
  bucket matched JSON calls for each workload/role/status group.
- Rolled a stripped pod and replaced the Go runtime via self-exec under the
  **same PID**, birth time and executable inode. New stack identity was detected,
  the kernel guard recorded one exec transition, and fresh connections captured
  correctly after reattachment with no duplicate or misattributed records.
- Non-Go fixture remained untraced. All six producer error counters and metric
  overflow counters were zero. Eight TLS incarnations opened/closed, zero active
  or retained parser state at completion, and zero resident capture workers.
- Service account allowed `list pods`; denied `get secrets`.

`./scripts/evaluate-phase7.sh`, final `go test ./...`, `go vet ./...`, targeted
race tests, shell syntax checks and `git diff --check` passed. Rootless coverage
includes cgroup v1/v2/systemd IDs, exact-ID matching, node/namespace filtering,
pagination, malformed/403 API responses, token rotation, PID/exec identity,
deleted executable resolution, dynamic workload metric bounds and monotonic
telemetry aggregation across worker replacement.

Phase 6 live capture regression also passed after the BPF namespace change:
regular and -s -w targets each produced 200 exact endpoint records with zero
producer errors; HTTP/final counters, statuses, bytes and latency buckets matched.

## Artifacts and commands

Raw evidence: `evaluation.log`, `agent.log`, `batches.json`, `identities.json`,
`calls.jsonl`, `active.metrics`, `final.metrics`, `pods.json` and `rbac.txt`.
Deployment guide: `deploy/phase7/README.md`.

Reproduce: `./scripts/evaluate-phase7.sh && ./scripts/test-phase7.sh`.
Inspect: `kubectl --kubeconfig build/phase7-evaluation/kubeconfig -n axon get pods`.
Cleanup: `kind delete cluster --name axon-phase7`.

The cluster/DaemonSet remain for inspection; traffic Deployments are scaled to
zero. The non-Go negative fixture remains running. The user's default Kubernetes
context is still `bg-switch`; only the dedicated build-directory kubeconfig was
used. No production cluster was modified.

## Limits

Privileged Linux amd64 baseline evaluated; capability-only and production
multi-node operation are not claimed. Discovery defaults to four workers and
polls every two seconds; short-lived processes and pre-attach traffic can be
missed. Established HTTP/2 sessions need fresh connections. Exec/pod transitions
can interrupt observations, but old and new parser state are never deliberately
joined. Node-wide capture-health metrics have agent labels; RPC metrics have
per-target workload labels. The integration test selects evaluation Deployments
with one supervisor label selector; production discovery defaults to user
namespaces without requiring per-pod attach setup.

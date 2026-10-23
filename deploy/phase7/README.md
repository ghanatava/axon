# Node discovery and Kubernetes deployment

Axon runs one supervisor per Linux amd64 node. It lists running containers on
that node, matches exact 64-character runtime IDs in `/proc/*/cgroup`, and inspects
`/proc/PID/exe`. No application binary is copied out of the pod and deleted
executables remain accessible through procfs. Unsupported/non-Go processes are
skipped. Each eligible process receives its own PID-filtered capture worker,
Go ABI configuration, BPF maps, ring and HTTP/2/gRPC state.

## Build and deploy

The build host needs Go 1.25+, clang with the BPF target, bpftool, Docker and
readable `/sys/kernel/btf/vmlinux`. The image contains a static Go agent and a
CO-RE BPF object, so the node does not need compilers or Go. Build on amd64:

```sh
./scripts/build-image.sh                  # default image axon:phase7
kind load docker-image axon:phase7 --name YOUR_CLUSTER
kubectl apply -f deploy/daemonset.yaml
kubectl -n axon rollout status daemonset/axon
kubectl -n axon logs daemonset/axon -f
```

For another cluster, tag/push the image to your registry and change the manifest's
image. `AXON_IMAGE=registry.example/axon:version ./scripts/build-image.sh` sets the
build tag. The manifest uses `IfNotPresent`; use immutable version tags/digests
for upgrades. Agent changes require a DaemonSet rollout.

By default all running user containers on the node are candidates, excluding
`kube-system,kube-public,kube-node-lease,axon` and the agent's own pod UID.
No per-pod PID, binary path, sidecar or application code change is required.
Optional `-pod-selector=app.kubernetes.io/part-of=my-app` restricts candidates
using pod labels; `-exclude-namespaces` replaces the exclusion list.
Containerd, CRI-O and Docker-style full runtime IDs are supported with cgroup v1
and v2, including systemd scopes. Unmatched/ambiguous IDs are not attributed.

## Permissions and resources

The supplied baseline deliberately runs as root with `privileged: true`,
`hostPID: true`, a read-only root filesystem and a read-only `/sys` host mount.
It needs node procfs visibility, permission to read other users' executable
images/runtime memory, BPF loading, perf/uprobe attachment, tracepoint attachment
and access to kernel BTF. Linux 5.15+ with BTF, BPF, perf events, uprobes and
`sched_process_exec` tracing is required; Linux 6.8 is evaluated here.

`CAP_BPF` and `CAP_PERFMON` cover BPF/perf operations on modern kernels;
`CAP_SYS_PTRACE`/`CAP_DAC_READ_SEARCH` and memlock permissions also matter for
cross-container procfs and attachment. Older kernel/perf paths can require
`CAP_SYS_ADMIN`. An unprivileged, capability-only deployment is **not evaluated**;
do not treat BPF/PERFMON alone as sufficient. The namespace is labeled for the
privileged Pod Security profile. Admission policies must allow this node agent.

The service account has only cluster-wide **list pods** permission, with no
secrets access or write verbs. Requests are filtered by `spec.nodeName`; RBAC
itself does not enforce that filter. The client verifies the API server using
the projected CA and re-reads rotated service account tokens on each refresh.

The DaemonSet requests 100m CPU/128Mi and limits 1 CPU/512Mi. Default
`-max-targets=4` bounds simultaneous workers. Each worker uses a 1MiB ring,
4096 kernel connection/call slots, 128 parser sessions, an 8MiB parser buffer
budget and 30s idle expiry. These are buffer/state limits, not exact RSS limits.
Increase the worker cap and pod resources together for larger nodes. Deferred
candidates appear in `axon_discovery_deferred_processes`; saturation can leave
processes untraced until a slot becomes available.

## Lifecycle and correctness

Discovery refreshes every two seconds (`-discovery-interval`). A process is
identified by PID, birth ticks, initial stack address and executable device/inode.
Workers pin the executable FD and verify the identity before/after attachment.
A `sched_process_exec` BPF guard stops old probes before a replacement runtime
can emit TLS events, including exec of the same binary under the same PID.
The supervisor then inspects and attaches the new incarnation. PID/TID values
are resolved in the agent's node PID namespace using CO-RE ancestor PID data;
this handles nested nodes such as kind as well as ordinary hostPID deployments.

Each capture worker is isolated: one unsupported ABI or failed capture cannot
mix parser state with another process. Gone/replaced processes detach and their
maps disappear with the worker. A worker error is retried on subsequent refresh.
Metadata errors stop new discovery and make readiness fail. Existing workers
are retained for up to 30s since the last successful refresh, then stopped so
stale pod identities cannot remain attached indefinitely.

Polling can miss short-lived processes and traffic before attachment. HTTP/2
connections established before capture cannot be safely reconstructed: reconnect
or wait for a new connection. Exec/restart transitions can interrupt observations;
Axon does not claim lossless capture across those transitions. New connections
are checked for ordering/loss as in Phase 5.

## Output and health

JSON calls include `service`, `namespace`, `pod`, `pod_uid`, `container`,
`container_id` and `node`, plus node-namespace PID and capture identity.
Service comes from `app.kubernetes.io/name`, then `app`, then container name.
Prometheus RPC metrics use each call's discovered workload; client and server
roles remain separate. Metric groups are bounded globally, including workload
labels, by `-metrics-max-series` (default 1024). Pod churn consumes new groups;
new groups beyond the bound increment the visible dropped-observation counter.

Capture-health counters are aggregated across workers and keep accumulating as
workers exit; those metrics carry the agent's labels, rather than an application's
pod labels. Discovery metrics carry `node`. `/readyz` requires fresh successful
node metadata; `/livez` checks HTTP process responsiveness. An agent can be ready
with zero eligible workloads. Monitor active/resident/deferred targets and failures too.
`axon_discovery_exec_transitions_total` records the kernel exec-guard transitions.

```sh
kubectl -n axon port-forward service/axon 9108:9108
curl http://127.0.0.1:9108/metrics
```

The headless Service and pod scrape annotations expose metrics within the
cluster. Configure your Prometheus to scrape each DaemonSet pod, not one
load-balanced endpoint. The Phase 6 local Prometheus can use the port-forward
above for this single-node demo. Select an endpoint role to avoid double counting.
When filtering dashboard workload labels, node-wide capture health remains under
`service="axon"`; use All to inspect those aggregate health panels.

## Reproducible evaluation

```sh
./scripts/evaluate-phase7.sh   # rootless unit/race/regression checks
./scripts/test-phase7.sh       # dedicated kind cluster, images, workloads, checks
```

The cluster test uses its own `build/phase7-evaluation/kubeconfig`, leaving the
user's default context untouched. It selects the evaluation Deployments globally,
then discovers their PIDs/binaries automatically. It validates regular and
`-s -w` images, non-Go rejection, a pod rollout, same-PID self-exec, fresh traffic
after each change, exact pod/container/PID/service attribution, error metrics,
resource cleanup and list-pods/no-secrets RBAC. Workloads are scaled to zero at
the end; the DaemonSet, cluster and non-Go negative fixture stay available for
inspection. Raw records, identities, manifests, metrics and logs are saved under
`build/phase7-evaluation/`.

To generate more traffic, scale the Deployments back to one, wait for
`discovery attached` in the agent log, port-forward a fixture pod's port 19090,
then `curl -X POST 'http://127.0.0.1:19090/run?count=10'`.

Cleanup the dedicated test cluster when finished:

```sh
kind delete cluster --name axon-phase7
```

References: [DaemonSets](https://kubernetes.io/docs/concepts/workloads/controllers/daemonset/),
[kind configuration](https://kind.sigs.k8s.io/docs/user/configuration/),
[Linux PID namespace helper source](https://github.com/torvalds/linux/blob/v6.8/kernel/bpf/helpers.c).

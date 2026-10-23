#!/usr/bin/env bash
# Live uninstrumented grpc-go TLS -> eBPF -> HTTP/2 -> gRPC call records.
set -euo pipefail
cd "$(dirname "$0")/.."
if [[ $EUID != 0 ]]; then
    echo 'Run: sudo env PATH="$PATH" ./scripts/test-phase4.sh' >&2
    exit 1
fi
./scripts/build-phase2.sh
mkdir -p build/phase4
agent_pid=''
cleanup() {
    if [[ -n $agent_pid ]]; then
        kill -TERM "$agent_pid" 2>/dev/null || true
        wait "$agent_pid" || true
    fi
}
trap cleanup EXIT
# This real grpc-go fixture builds as part of the Go 1.25+ module. Unlike the
# standalone Phase 2/3 fixtures, do not disable modules when detecting/building.
compiler=$(command -v go)
version=$(env -u GOROOT GO111MODULE=on "$compiler" version | awk '{print $3}')
for mode in unstripped stripped; do
    target="build/phase4/grpcfixture-$version-$mode"
    flags=()
    if [[ $mode == stripped ]]; then flags=("-ldflags=-s -w"); fi
    env -u GOROOT GO111MODULE=on "$compiler" build "${flags[@]}" -o "$target" ./testtargets/grpcphase4
    calls="build/phase4/live-$version-$mode.jsonl"
    log="build/phase4/live-$version-$mode.log"
    ./build/axon-agent -bin "$target" -mode grpc > "$calls" 2> "$log" &
    agent_pid=$!
    ready=0
    for ((i=0;i<100;i++)); do
        if grep -q 'capture ready' "$log"; then ready=1; break; fi
        if ! kill -0 "$agent_pid" 2>/dev/null; then cat "$log"; exit 1; fi
        sleep 0.1
    done
    if [[ $ready != 1 ]]; then cat "$log"; echo 'Capture readiness timed out' >&2; exit 1; fi
    # No recorder flag: the target application is unaware of the tracer.
    timeout 30 "$target"
    sleep 0.5
    if ! kill -0 "$agent_pid" 2>/dev/null; then cat "$log"; wait "$agent_pid"; exit 1; fi
    kill -TERM "$agent_pid"
    wait "$agent_pid" || { cat "$log"; exit 1; }
    agent_pid=''
    python3 scripts/verify-phase4.py "$calls"
    for label in 'memory/identity errors' 'map errors' 'ring drops' 'truncated calls'; do
        grep -Fq "$label: 0" "$log" || { cat "$log"; exit 1; }
    done
    echo "$version $mode live gRPC capture passed"
done

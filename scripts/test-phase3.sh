#!/usr/bin/env bash
# Live BPF + TLS -> HTTP/2 integration check. Rootless checks: evaluate-phase3.sh.
set -euo pipefail
cd "$(dirname "$0")/.."
if [[ $EUID != 0 ]]; then
    echo 'Run: sudo env PATH="$PATH" ./scripts/test-phase3.sh' >&2
    exit 1
fi
./scripts/build-phase2.sh
mkdir -p build/phase3
compilers=("$(command -v go)")
if [[ -n ${AXON_GO116:-} ]]; then compilers=("$AXON_GO116" "${compilers[@]}"); fi
if [[ -n ${AXON_GO_EXTRA:-} ]]; then
    IFS=: read -r -a extra_compilers <<< "$AXON_GO_EXTRA"
    compilers+=("${extra_compilers[@]}")
fi
agent_pid=''
cleanup() {
    if [[ -n $agent_pid ]]; then
        kill -TERM "$agent_pid" 2>/dev/null || true
        wait "$agent_pid" || true
    fi
}
trap cleanup EXIT
for compiler in "${compilers[@]}"; do
    version=$(env -u GOROOT GO111MODULE=off "$compiler" version | awk '{print $3}')
    for mode in unstripped stripped; do
        target="build/phase3/h2fixture-$version-$mode"
        flags=()
        if [[ $mode == stripped ]]; then flags=("-ldflags=-s -w"); fi
        env -u GOROOT GO111MODULE=off "$compiler" build "${flags[@]}" -o "$target" testtargets/h2phase3/main.go
        frames="build/phase3/live-$version-$mode.jsonl"
        log="build/phase3/live-$version-$mode.log"
        ./build/axon-agent -bin "$target" -mode h2 > "$frames" 2> "$log" &
        agent_pid=$!
        ready=0
        for ((i=0;i<100;i++)); do
            if grep -q 'capture ready' "$log"; then ready=1; break; fi
            if ! kill -0 "$agent_pid" 2>/dev/null; then cat "$log"; exit 1; fi
            sleep 0.1
        done
        if [[ $ready != 1 ]]; then cat "$log"; echo 'Capture readiness timed out' >&2; exit 1; fi
        timeout 30 "$target"
        sleep 0.5
        # Unexpected agent exits must fail, even if a prefix of JSON looks valid.
        if ! kill -0 "$agent_pid" 2>/dev/null; then cat "$log"; wait "$agent_pid"; exit 1; fi
        kill -TERM "$agent_pid"
        wait "$agent_pid" || { cat "$log"; exit 1; }
        agent_pid=''
        python3 scripts/verify-phase3.py "$frames"
        for label in 'memory/identity errors' 'map errors' 'ring drops' 'truncated calls'; do
            grep -Fq "$label: 0" "$log" || { cat "$log"; exit 1; }
        done
        echo "$version $mode live HTTP/2 capture passed"
    done
done

#!/usr/bin/env bash
# Run after building; root is needed only for the BPF agent.
set -euo pipefail
cd "$(dirname "$0")/.."
if [[ $EUID != 0 ]]; then
    echo 'Run: sudo env PATH="$PATH" AXON_GO116=/path/to/go1.16/bin/go ./scripts/test-phase2.sh' >&2
    exit 1
fi
if [[ -z ${AXON_GO116:-} ]]; then
    echo 'AXON_GO116 must point to Go 1.16.x for the required ABI matrix' >&2
    exit 1
fi
if [[ $("$AXON_GO116" version) != *' go1.16.'* ]]; then
    echo 'AXON_GO116 must be a Go 1.16 compiler' >&2
    exit 1
fi
./scripts/build-phase2.sh
agent_pid=''
cleanup() {
    if [[ -n $agent_pid ]]; then
        kill -TERM "$agent_pid" 2>/dev/null || true
        wait "$agent_pid" || true
    fi
}
trap cleanup EXIT
compilers=("$AXON_GO116" "$(command -v go)")
if [[ -n ${AXON_GO_EXTRA:-} ]]; then
    IFS=: read -r -a extra_compilers <<< "$AXON_GO_EXTRA"
    compilers+=("${extra_compilers[@]}")
fi
for compiler in "${compilers[@]}"; do
    version=$(env -u GOROOT GO111MODULE=off "$compiler" version | awk '{print $3}')
    for mode in unstripped stripped; do
        target="build/tlsphase2-$version-$mode"
        flags=()
        if [[ $mode == stripped ]]; then flags=("-ldflags=-s -w"); fi
        env -u GOROOT GO111MODULE=off "$compiler" build "${flags[@]}" -o "$target" testtargets/tlsphase2/main.go
        log="build/capture-$version-$mode.log"
        ./build/axon-agent -bin "$target" > "$log" 2>&1 &
        agent_pid=$!
        ready=0
        for ((i=0;i<100;i++)); do
            if grep -q 'capture ready' "$log"; then ready=1; break; fi
            if ! kill -0 "$agent_pid" 2>/dev/null; then cat "$log"; exit 1; fi
            sleep 0.1
        done
        if [[ $ready != 1 ]]; then
            cat "$log"
            echo 'Agent readiness timed out' >&2
            exit 1
        fi
        timeout 30 "$target"
        sleep 0.5
        cleanup
        agent_pid=''
        python3 scripts/verify-phase2.py "$log"
        echo "$version $mode capture passed"
    done
done

#!/usr/bin/env bash
# Live probes + actual HTTP scrapes. Docker runner is optional for local evaluation.
set -euo pipefail
cd "$(dirname "$0")/.."
if [[ $EUID != 0 && ${AXON_AGENT_DOCKER:-0} != 1 ]]; then
    echo 'Run: sudo env PATH="$PATH" ./scripts/test-phase6.sh' >&2; exit 1
fi
./scripts/build-phase2.sh
mkdir -p build/phase6
for mode in unstripped stripped; do
    flags=()
    if [[ $mode == stripped ]]; then flags=("-ldflags=-s -w"); fi
    go build "${flags[@]}" -o "build/phase6/fixture-$mode" ./testtargets/grpcphase4
    python3 scripts/run-phase6-live.py "$mode"
    python3 scripts/verify-phase6.py "build/phase6/$mode.calls" "build/phase6/$mode.metrics"
    python3 scripts/verify-phase6.py "build/phase6/$mode.calls" "build/phase6/$mode.http.metrics"
done
printf '%s\n' 'Phase6 live capture/metrics evaluation passed.'

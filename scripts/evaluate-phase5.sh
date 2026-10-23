#!/usr/bin/env bash
# Rootless protocol/correlation/load evaluation; live probes tested separately.
set -euo pipefail
cd "$(dirname "$0")/.."
mkdir -p build/phase5-evaluation
go test -race ./internal/session ./internal/grpc ./internal/h2demux ./internal/tlscapture ./cmd/agent
go build -o build/phase5-evaluation/axon-agent ./cmd/agent
for mode in unstripped stripped; do
    flags=()
    if [[ $mode == stripped ]]; then flags=("-ldflags=-s -w"); fi
    go build "${flags[@]}" -o "build/phase5-evaluation/load-$mode" ./testtargets/grpcphase5
    workers=()
    for worker in 0 1 2; do
        ./build/phase5-evaluation/load-"$mode" -worker "$mode-$worker" -duration 3s -connections 3 -calls 40 -parallel 2 -pause 10ms \
            -events "build/phase5-evaluation/$mode-$worker.events" -manifest "build/phase5-evaluation/$mode-$worker.manifest" &
        workers+=("$!")
    done
    for worker in "${workers[@]}"; do wait "$worker"; done
    cat build/phase5-evaluation/"$mode"-*.events > "build/phase5-evaluation/$mode.events"
    ./build/phase5-evaluation/axon-agent -mode grpc -replay "build/phase5-evaluation/$mode.events" > "build/phase5-evaluation/$mode.calls" 2> "build/phase5-evaluation/$mode.log"
    python3 scripts/verify-phase5.py --calls "build/phase5-evaluation/$mode.calls" --manifests build/phase5-evaluation/"$mode"-*.manifest
done
# A missing event quarantines one capture incarnation; unrelated later traffic survives.
./build/phase5-evaluation/load-stripped -worker damaged -rounds 2 -connections 2 -calls 30 -drop-every 9 -events build/phase5-evaluation/damaged.events -manifest build/phase5-evaluation/damaged.manifest
./build/phase5-evaluation/load-stripped -worker healthy -rounds 1 -connections 1 -calls 10 -events build/phase5-evaluation/healthy.events -manifest build/phase5-evaluation/healthy.manifest
cat build/phase5-evaluation/damaged.events build/phase5-evaluation/healthy.events > build/phase5-evaluation/recovery.events
./build/phase5-evaluation/axon-agent -mode grpc -replay build/phase5-evaluation/recovery.events > build/phase5-evaluation/recovery.calls 2> build/phase5-evaluation/recovery.log
python3 scripts/verify-phase5.py --calls build/phase5-evaluation/recovery.calls --manifests build/phase5-evaluation/damaged.manifest build/phase5-evaluation/healthy.manifest --allow-loss --require-worker healthy
printf '%s\n' 'Phase5 rootless evaluation passed. Live kernel/ring-pressure validation: scripts/test-phase5.sh.'

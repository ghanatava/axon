#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
if [[ $EUID != 0 ]]; then echo 'Run: sudo env PATH="$PATH" ./scripts/test-phase5.sh' >&2; exit 1; fi
./scripts/build-phase2.sh
mkdir -p build/phase5
agent_pid=''
workers=()
cleanup() {
    for worker in "${workers[@]}"; do kill -TERM "$worker" 2>/dev/null || true; done
    if [[ -n $agent_pid ]]; then kill -CONT "$agent_pid" 2>/dev/null || true; kill -TERM "$agent_pid" 2>/dev/null || true; wait "$agent_pid" || true; fi
}
trap cleanup EXIT
ready() {
    for ((i=0;i<100;i++)); do
        if grep -q 'capture ready' "$1"; then return; fi
        if ! kill -0 "$agent_pid" 2>/dev/null; then cat "$1"; exit 1; fi
        sleep 0.1
    done
    cat "$1"; echo 'Capture readiness timed out' >&2; exit 1
}
stop_agent() {
    if ! kill -0 "$agent_pid" 2>/dev/null; then cat "$1"; wait "$agent_pid"; exit 1; fi
    kill -TERM "$agent_pid"
    wait "$agent_pid" || { cat "$1"; exit 1; }
    agent_pid=''
}
for mode in unstripped stripped; do
    flags=()
    if [[ $mode == stripped ]]; then flags=("-ldflags=-s -w"); fi
    go build "${flags[@]}" -o "build/phase5/load-$mode" ./testtargets/grpcphase5
    ./build/axon-agent -bin "build/phase5/load-$mode" -mode grpc > "build/phase5/live-$mode.calls" 2> "build/phase5/live-$mode.log" &
    agent_pid=$!
    ready "build/phase5/live-$mode.log"
    workers=()
    for worker in 0 1 2; do
        timeout 40 "./build/phase5/load-$mode" -worker "$mode-$worker" -duration 5s -connections 3 -calls 40 -parallel 2 -pause 10ms -manifest "build/phase5/live-$mode-$worker.manifest" &
        workers+=("$!")
    done
    for worker in "${workers[@]}"; do wait "$worker"; done
    workers=()
    sleep 1
    stop_agent "build/phase5/live-$mode.log"
    python3 scripts/verify-phase5.py --calls "build/phase5/live-$mode.calls" --manifests build/phase5/live-"$mode"-*.manifest
    for label in 'memory/identity errors' 'map errors' 'ring drops' 'truncated calls' 'overlapping calls' 'unattributed errors'; do
        grep -Fq "$label: 0" "build/phase5/live-$mode.log" || { cat "build/phase5/live-$mode.log"; exit 1; }
    done
done
# Deliberately fill the ring while the consumer is stopped, then recover on NEW connections.
./build/axon-agent -bin build/phase5/load-stripped -mode grpc -ring-bytes 262144 -idle-timeout 1s > build/phase5/pressure.calls 2> build/phase5/pressure.log &
agent_pid=$!
ready build/phase5/pressure.log
kill -STOP "$agent_pid"
timeout 30 ./build/phase5/load-stripped -worker pressure -connections 2 -calls 80 -parallel 2 -rounds 1 -pause 0 -manifest build/phase5/pressure.manifest
kill -CONT "$agent_pid"
sleep 1
timeout 30 ./build/phase5/load-stripped -worker healthy -connections 1 -calls 10 -parallel 1 -rounds 1 -pause 20ms -manifest build/phase5/healthy.manifest
sleep 2
stop_agent build/phase5/pressure.log
python3 scripts/verify-phase5.py --calls build/phase5/pressure.calls --manifests build/phase5/pressure.manifest build/phase5/healthy.manifest --allow-loss --require-worker healthy
python3 - <<'PY'
import json,re
for mode in ['unstripped','stripped']:
    text=open(f'build/phase5/live-{mode}.log').read()
    stats=json.loads(re.search(r'runtime_stats (\{.*\})',text).group(1))
    assert stats['invalidated']==stats['incomplete']==stats['active']==stats['retained_bytes']==0, stats
    assert stats['calls']>0 and stats['peak']>1 and stats['closed']>1, stats
text=open('build/phase5/pressure.log').read()
assert int(re.search(r'ring drops: (\d+)',text).group(1))>0, 'pressure did not fill ring'
assert 'unattributed errors: 0' in text
print('Live Phase5 load and ring-pressure recovery passed.')
PY

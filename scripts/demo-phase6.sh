#!/usr/bin/env bash
# Leave Prometheus/Grafana up and generate live traced traffic until Ctrl-C.
set -euo pipefail
cd "$(dirname "$0")/.."
if [[ $EUID != 0 ]]; then
    echo 'Run: sudo env PATH="$PATH" ./scripts/demo-phase6.sh' >&2; exit 1
fi
./scripts/build-phase2.sh
mkdir -p build/phase6-demo
go build -ldflags='-s -w' -o build/phase6-demo/fixture ./testtargets/grpcphase4
docker compose -f deploy/observability/compose.yaml up -d
agent_pid=''
cleanup() {
    if [[ -n $agent_pid ]]; then kill -TERM "$agent_pid" 2>/dev/null || true; wait "$agent_pid" || true; fi
}
trap cleanup EXIT
./build/axon-agent -bin build/phase6-demo/fixture -mode grpc -metrics-listen 127.0.0.1:9108 \
    -service phase6-echo -namespace test -pod fixture \
    > build/phase6-demo/calls.jsonl 2> build/phase6-demo/agent.log &
agent_pid=$!
ready=0
for ((i=0;i<100;i++)); do
    if grep -q 'capture ready' build/phase6-demo/agent.log; then ready=1; break; fi
    if ! kill -0 "$agent_pid" 2>/dev/null; then cat build/phase6-demo/agent.log; exit 1; fi
    sleep .1
done
if [[ $ready != 1 ]]; then cat build/phase6-demo/agent.log; exit 1; fi
printf '%s\n' 'Dashboard: http://127.0.0.1:3000/d/axon-grpc (no login needed for local viewing)' 'Generating unary, streaming, gzip and error traffic. Ctrl-C stops capture; dashboard services stay up.'
while kill -0 "$agent_pid" 2>/dev/null; do
    timeout 30 ./build/phase6-demo/fixture > /dev/null
    sleep .5
done
wait "$agent_pid"

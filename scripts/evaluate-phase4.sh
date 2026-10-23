#!/usr/bin/env bash
# Rootless independent evaluation. Recorder emits synthetic TLS identities; no BPF proof.
set -euo pipefail
cd "$(dirname "$0")/.."
mkdir -p build/phase4-evaluation
go test -race ./internal/grpc ./internal/h2demux ./internal/tlscapture ./cmd/agent
go test ./internal/grpc -run '^$' -fuzz '^FuzzEvaluatorMessageSegmentation$' -fuzztime=3s -parallel=2
go build -o build/phase4-evaluation/axon-agent ./cmd/agent
for mode in unstripped stripped; do
  flags=()
  if [[ "$mode" == stripped ]]; then flags=(-ldflags='-s -w'); fi
  go build "${flags[@]}" -o "build/phase4-evaluation/fixture-$mode" ./testtargets/grpcphase4
  "./build/phase4-evaluation/fixture-$mode" -events "build/phase4-evaluation/events-$mode.jsonl"
  ./build/phase4-evaluation/axon-agent -mode grpc -replay "build/phase4-evaluation/events-$mode.jsonl" > "build/phase4-evaluation/calls-$mode.jsonl"
  python3 scripts/verify-phase4.py "build/phase4-evaluation/calls-$mode.jsonl"
done
# Ensure both the pipeline and independent checker reject corrupted inputs.
printf '{"PID":' > build/phase4-evaluation/bad.jsonl
if ./build/phase4-evaluation/axon-agent -mode grpc -replay build/phase4-evaluation/bad.jsonl >build/phase4-evaluation/rejection.log 2>&1; then exit 1; fi
python3 - <<'PY'
import json
p='build/phase4-evaluation/calls-unstripped.jsonl'
rows=[json.loads(x) for x in open(p)]
rows[0]['request_bytes'] += 1
with open('build/phase4-evaluation/corrupt-calls.jsonl','w') as out:
    for row in rows: out.write(json.dumps(row)+'\n')
PY
if python3 scripts/verify-phase4.py build/phase4-evaluation/corrupt-calls.jsonl >>build/phase4-evaluation/rejection.log 2>&1; then exit 1; fi
printf '%s\n' 'Rootless Phase4 evaluation passed. Live BPF validation requires scripts/test-phase4.sh.'

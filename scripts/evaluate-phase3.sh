#!/usr/bin/env bash
# Rootless evaluator: real TLS recorder -> JSON event replay. Does not test BPF.
set -euo pipefail
cd "$(dirname "$0")/.."
mkdir -p build/phase3-evaluation
go test -race ./internal/h2demux ./internal/tlscapture ./cmd/agent
go build -o build/phase3-evaluation/axon-agent ./cmd/agent
for mode in unstripped stripped; do
  flags=()
  if [[ "$mode" == stripped ]]; then flags=(-ldflags='-s -w'); fi
  go build "${flags[@]}" -o "build/phase3-evaluation/fixture-$mode" ./testtargets/h2phase3
  "./build/phase3-evaluation/fixture-$mode" -events "build/phase3-evaluation/events-$mode.jsonl"
  ./build/phase3-evaluation/axon-agent -mode h2 -replay "build/phase3-evaluation/events-$mode.jsonl" > "build/phase3-evaluation/frames-$mode.jsonl"
  python3 scripts/verify-phase3.py "build/phase3-evaluation/frames-$mode.jsonl"
done
# Exercise actual CLI rejection paths without root or a BPF object.
if ./build/phase3-evaluation/axon-agent -mode nonsense -replay missing >build/phase3-evaluation/error.log 2>&1; then exit 1; fi
if ./build/phase3-evaluation/axon-agent -mode h2 -replay build/phase3-evaluation/missing.jsonl >>build/phase3-evaluation/error.log 2>&1; then exit 1; fi
printf '{"PID":' > build/phase3-evaluation/bad.jsonl
if ./build/phase3-evaluation/axon-agent -mode h2 -replay build/phase3-evaluation/bad.jsonl >>build/phase3-evaluation/error.log 2>&1; then exit 1; fi
printf '%s\n' 'Rootless Phase 3 evaluation passed. Live BPF capture/verifier requires scripts/test-phase3.sh.'

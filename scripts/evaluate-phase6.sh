#!/usr/bin/env bash
# No root: real grpc-go TLS -> recorder -> full agent -> Prometheus endpoint.
set -euo pipefail
cd "$(dirname "$0")/.."
mkdir -p build/phase6-evaluation
go test -race ./internal/metrics ./cmd/agent
go build -o build/phase6-evaluation/axon-agent ./cmd/agent
for mode in unstripped stripped; do
    flags=()
    if [[ $mode == stripped ]]; then flags=("-ldflags=-s -w"); fi
    go build "${flags[@]}" -o "build/phase6-evaluation/fixture-$mode" ./testtargets/grpcphase4
    ./build/phase6-evaluation/fixture-"$mode" -events "build/phase6-evaluation/$mode.events"
    python3 - "$mode" <<'PY'
import os,pathlib,subprocess,sys,time,urllib.request
mode=sys.argv[1];root=pathlib.Path('build/phase6-evaluation');fifo=root/(mode+'.fifo')
if fifo.exists():fifo.unlink()
os.mkfifo(fifo)
try:
    with open(root/(mode+'.calls'),'w') as calls,open(root/(mode+'.log'),'w') as log:
        p=subprocess.Popen([str(root/'axon-agent'),'-mode','grpc','-replay',str(fifo),'-metrics-listen','127.0.0.1:0','-metrics-output',str(root/(mode+'.metrics')),'-service','phase6-echo','-namespace','test','-pod','fixture'],stdout=calls,stderr=log)
        try:
            for _ in range(100):
                import re
                m=re.search(r'metrics listening on (\S+)',(root/(mode+'.log')).read_text())
                if m:break
                if p.poll() is not None:raise RuntimeError('agent exited')
                time.sleep(.05)
            assert m,'HTTP listener not ready'
            with open(fifo,'w') as writer:
                writer.write((root/(mode+'.events')).read_text());writer.flush()
                for _ in range(100):
                    text=urllib.request.urlopen('http://'+m[1]+'/metrics',timeout=2).read().decode()
                    count=sum(float(line.split()[-1]) for line in text.splitlines() if line.startswith('axon_grpc_requests_total{'))
                    if count==10:break
                    time.sleep(.05)
                assert count==10,(count,text)
                (root/(mode+'.http.metrics')).write_text(text)
            assert p.wait(timeout=10)==0,(root/(mode+'.log')).read_text()
        finally:
            if p.poll() is None:p.terminate();p.wait(timeout=5)
finally:fifo.unlink(missing_ok=True)
PY
    python3 scripts/verify-phase4.py "build/phase6-evaluation/$mode.calls"
    python3 scripts/verify-phase6.py "build/phase6-evaluation/$mode.calls" "build/phase6-evaluation/$mode.metrics"
    python3 scripts/verify-phase6.py "build/phase6-evaluation/$mode.calls" "build/phase6-evaluation/$mode.http.metrics"
done
printf '%s\n' 'Phase6 rootless metrics evaluation passed.'

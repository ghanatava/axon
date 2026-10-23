#!/usr/bin/env bash
# Operates only on the dedicated axon-phase7 kind cluster and its kubeconfig.
set -euo pipefail
cd "$(dirname "$0")/.."
mkdir -p build/phase7-evaluation build/phase7
export KUBECONFIG="$PWD/build/phase7-evaluation/kubeconfig"
if ! kind get clusters | grep -qx axon-phase7; then
    kind create cluster --name axon-phase7 --image kindest/node:v1.32.2 --kubeconfig "$KUBECONFIG" --wait 120s
elif [[ ! -f $KUBECONFIG ]]; then
    kind get kubeconfig --name axon-phase7 > "$KUBECONFIG"
fi
./scripts/build-image.sh
for mode in unstripped stripped; do
    flags=()
    if [[ $mode == stripped ]]; then flags=("-ldflags=-s -w"); fi
    CGO_ENABLED=0 go build "${flags[@]}" -o "build/phase7/fixture-$mode" ./testtargets/grpcphase7
    docker build -f deploy/phase7/Dockerfile.fixture --build-arg "MODE=$mode" -t "axon-fixture:$mode" .
done
kind load docker-image axon:phase7 axon-fixture:unstripped axon-fixture:stripped --name axon-phase7
kubectl apply -f deploy/daemonset.yaml
kubectl -n axon patch daemonset axon --type=json -p '[{"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"-pod-selector=app.kubernetes.io/part-of=axon-evaluation"}]'
# Ensure reruns use the just-built agent and start with clean metric state.
kubectl -n axon rollout restart daemonset/axon
kubectl -n axon rollout status daemonset/axon --timeout=120s
kubectl apply -f deploy/phase7/workload.json
kubectl -n axon-demo scale deployment/echo-unstripped deployment/echo-stripped --replicas=1
kubectl -n axon-demo rollout restart deployment/echo-unstripped deployment/echo-stripped
kubectl -n axon-demo rollout status deployment/echo-unstripped --timeout=120s
kubectl -n axon-demo rollout status deployment/echo-stripped --timeout=120s
python3 scripts/run-phase7-cluster.py
python3 scripts/verify-phase7.py

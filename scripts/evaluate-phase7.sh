#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
mkdir -p build/phase7-evaluation
go test -race ./internal/discovery ./internal/metrics ./cmd/agent
go test ./...
go vet ./...
printf '%s\n' 'Phase7 rootless checks passed. Cluster validation: ./scripts/test-phase7.sh'

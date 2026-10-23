#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
./scripts/build-phase2.sh
docker build -t "${AXON_IMAGE:-axon:phase7}" .

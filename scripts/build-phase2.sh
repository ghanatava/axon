#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
mkdir -p build
# Stage outputs so root-run integration checks do not prevent later user builds.
staging=$(mktemp -d build/phase2.XXXXXX)
bpftool btf dump file /sys/kernel/btf/vmlinux format c > "$staging/vmlinux.h"
clang -target bpf -mcpu=v3 -D__TARGET_ARCH_x86 -O2 -g -Wall -Werror -I "$staging" -I bpf/headers -c bpf/tls_capture.bpf.c -o "$staging/tls_capture.bpf.o"
CGO_ENABLED=0 go build -o "$staging/axon-agent" ./cmd/agent
mv "$staging/vmlinux.h" bpf/headers/vmlinux.h
mv "$staging/tls_capture.bpf.o" build/tls_capture.bpf.o
mv "$staging/axon-agent" build/axon-agent
rmdir "$staging"

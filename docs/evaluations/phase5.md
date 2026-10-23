# Phase 5 evaluation — 2026-10-24

PASS: rootless race, lifecycle, loss and resource tests; real-TLS replay for
regular and stripped builds: 9,360 exact records / 4,680 RPCs each.
5,000-call single-connection test verifies completed-stream cleanup.

PASS: live eBPF Go 1.25.0 regular and -s -w builds, each 15,840 records /
7,920 RPCs, three processes, 396 TLS incarnations. Zero misattribution and
all six producer error counters zero. Pointer reuses 229 / 207; goroutine
thread changes 40,333 / 41,790. All 396 states closed; retained bytes zero.

PASS: forced 256KiB ring pressure produced 8 drops. Surviving records were
correctly attributed; all 20 endpoint observations from the subsequent healthy
worker were present. Zero unattributed errors; final retained state zero.

The payload checker was rerun directly against all three live artifacts.
Commands: scripts/evaluate-phase5.sh and sudo scripts/test-phase5.sh.
Raw evidence: build/phase5/ and other files in this directory.
Evaluation uses tests and a separate payload/manifest checker, not an independent agent.

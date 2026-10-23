# Historical probe experiments

These entry/RET experiments are optional and use the `axon_poc` build tag.
The supported agent is `cmd/agent`; normal builds/tests do not need generated
PoC bindings or embedded objects. To regenerate and run an experiment:

```sh
./scripts/build-phase2.sh   # generates bpf/headers/vmlinux.h too
go generate -tags axon_poc ./poc/...
go run -tags axon_poc ./poc/uprobe-retsites -h
go run -tags axon_poc ./poc/dump-write-regs -h
```

Running probe attachment requires the same privileges as the main agent.

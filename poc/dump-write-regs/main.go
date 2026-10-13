package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/rlimit"

	"github.com/ghanatava/axon/internal/symbols"
)

//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -cc clang -cflags "-O2 -g -Wall" DumpWriteRegs ../../bpf/dump_write_regs.bpf.c -- -I../../bpf/headers

var binPath = flag.String("bin", "testtargets/client/client", "path to target binary")

func main() {
	flag.Parse()
	const funcName = "crypto/tls.(*Conn).Write"

	if err := rlimit.RemoveMemlock(); err != nil {
		log.Fatalf("removing memlock limit: %v", err)
	}

	sym, err := symbols.Resolve(*binPath, funcName)
	if err != nil {
		log.Fatalf("resolving symbol: %v", err)
	}
	log.Printf("resolved %s: vaddr=0x%x fileoff=0x%x", funcName, sym.Address, sym.FileOffset)

	objs := DumpWriteRegsObjects{}
	if err := LoadDumpWriteRegsObjects(&objs, nil); err != nil {
		log.Fatalf("loading BPF objects: %v", err)
	}
	defer objs.Close()

	absPath, err := filepath.Abs(*binPath)
	if err != nil {
		log.Fatalf("resolving absolute path: %v", err)
	}
	ex, err := link.OpenExecutable(absPath)
	if err != nil {
		log.Fatalf("opening executable: %v", err)
	}

	// Entry only -- offset 0. Attach exactly the way Phase 1 proved works:
	// Address = FileOffset (not virtual address), Offset relative to it.
	up, err := ex.Uprobe("", objs.DumpWriteRegs, &link.UprobeOptions{
		Address: sym.FileOffset,
		Offset:  0,
	})
	if err != nil {
		log.Fatalf("attaching uprobe: %v", err)
	}
	defer up.Close()

	log.Println("attached. Run the client now, sending known messages.")
	log.Println("In another terminal: sudo cat /sys/kernel/tracing/trace_pipe")
	log.Println("Ctrl-C here when done.")

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)
	<-stop
}

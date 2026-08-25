package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/rlimit"

	"github.com/ghanatava/axon/internal/symbols"
)

//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -cc clang -cflags "-O2 -g -Wall" RetSiteCount ../../bpf/retsite_count.bpf.c -- -I../../bpf/headers

type attachPoint struct {
	label  string
	offset uint64
	cookie uint64
}

var binPath = flag.String("bin", "testtargets/retsites/classify", "path to target binary")

func main() {
	flag.Parse()
	const funcName = "main.classify"

	if err := rlimit.RemoveMemlock(); err != nil {
		log.Fatalf("removing memlock limit: %v", err)
	}

	sym, err := symbols.Resolve(*binPath, funcName)
	if err != nil {
		log.Fatalf("resolving symbol: %v", err)
	}
	rets, err := symbols.RetSites(*binPath, funcName)
	if err != nil {
		log.Fatalf("finding RET sites: %v", err)
	}

	fmt.Printf("resolved %s: vaddr=0x%x fileoff=0x%x size=%d\n",
		funcName, sym.Address, sym.FileOffset, sym.Size)

	points := []attachPoint{
		{label: "entry", offset: 0, cookie: 0},
	}
	for i, off := range rets {
		points = append(points, attachPoint{
			label:  fmt.Sprintf("ret-site-%d", i),
			offset: off,
			cookie: uint64(i + 1),
		})
	}

	objs := RetSiteCountObjects{}
	if err := LoadRetSiteCountObjects(&objs, nil); err != nil {
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

	// FileOffset, not Address -- perf_event_open wants a byte position in
	// the file, not a virtual address. This is the untested variable.
	var links []link.Link
	for _, p := range points {
		up, err := ex.Uprobe("", objs.CountSite, &link.UprobeOptions{
			Address: sym.FileOffset,
			Offset:  p.offset,
			Cookie:  p.cookie,
		})
		if err != nil {
			log.Fatalf("attaching uprobe at %s (fileoff=0x%x offset=0x%x): %v",
				p.label, sym.FileOffset, p.offset, err)
		}
		links = append(links, up)
		fmt.Printf("attached: %-10s fileoff=0x%x offset=0x%x cookie=%d\n",
			p.label, sym.FileOffset, p.offset, p.cookie)
	}
	defer func() {
		for _, l := range links {
			l.Close()
		}
	}()

	fmt.Printf("\nrun %s in another terminal, then Ctrl-C here\n", *binPath)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)
	<-stop

	fmt.Println()
	for _, p := range points {
		var count uint64
		key := uint32(p.cookie)
		if err := objs.SiteCounts.Lookup(&key, &count); err != nil {
			log.Printf("reading count for %s: %v", p.label, err)
			continue
		}
		fmt.Printf("%-10s fired %d time(s)\n", p.label, count)
	}
}

package main

import (
	"fmt"
	"os"
	"time"
)

// 3 returns on purpose -- need multiple real RET sites to test against.
// uretprobe patches the return addr on the stack; Go moves goroutine
// stacks at runtime and corrupts that patch. Fix: disassemble ahead of
// time, uprobe each RET insn directly.
//
//go:noinline

func classify(n int) string {
	if n < 0 {
		return "negative"
	} else if n == 0 {
		return "zero"
	}
	return "positive"
}

func main() {
	inputs := []int{-3, 0, 7, -1, 42, 0}
	fmt.Printf("pid=%d --running classify() in a loop \n", os.Getpid())
	for {
		for _, n := range inputs {
			result := classify(n)
			fmt.Printf("classify(%d) = %s\n", n, result)
			time.Sleep(500 * time.Millisecond)
		}
	}
}

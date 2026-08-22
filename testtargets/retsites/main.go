package main

import (
	"fmt"
	"os"
	"time"
)

// classify has three separate return statements on purpose. A uretprobe
// (which patches the return address on the stack) is fine with a single
// return -- nothing here would prove anything. The real problem is that
// Go copies/moves goroutine stacks at runtime, which can corrupt that
// patch. The fix: disassemble the function ahead of time, find every RET
// machine instruction, and place an ordinary entry-style uprobe at each
// one instead. This function exists to give us multiple real RET
// instructions to prove that technique against.
//
//go:noinline

func classify(n int) string {
    if n<0 {
        return "negative"
    } else if n==0 {
        return "zero"
    }
    return "positive"
}

func main(){
    inputs := []int {-3,0,7,-1,42,0}
    fmt.Printf("pid=%d --running classify() in a loop \n",os.Getpid())
    for {
        for _,n := range inputs {
            result := classify(n)
            fmt.Printf("classify(%d) = %s\n", n, result)
            time.Sleep(500*time.Millisecond)
        }
    }
}
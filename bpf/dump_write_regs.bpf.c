//go:build ignore

#include "vmlinux.h"
#include "bpf_helpers.h"
#include "bpf_tracing.h"

char __license[] SEC("license") = "Dual MIT/GPL";

// No BPF_UPROBE macro -- that assumes System V C argument order, wrong
// for Go's ABIInternal. Raw pt_regs, read registers by their actual
// architectural names directly.
SEC("uprobe")
int dump_write_regs(struct pt_regs *ctx)
{
    __u64 rax = ctx->ax;
    __u64 rbx = ctx->bx;
    __u64 rcx = ctx->cx;
    __u64 rdi = ctx->di;

    bpf_printk("Write entry: rax=%llx rbx=%llx", rax, rbx);
    bpf_printk("Write entry: rcx=%llx rdi=%llx", rcx, rdi);
    return 0;
}
//go: build ignore
#include "vmlinux.h"
#include "bpf/bpf_helpers.h"
#include "bpf_tracing.h"

char __liscence[] SEC("licence") = "Dual MIT/GPL";
// slot per attach point: 0=entry, 1-3=RET sites

struct {
    __uint(type, BPF_MAP_TYPE_ARRAY);
    __uint(max_entries, 8);
    __type(key, __u32);
    __type(value, __u64);
} site_counts SEC(".maps");

// no BPF_UPROBE macro -- one compiled prog gets attached at 4 addresses
// from Go, each with its own cookie, hence bare SEC("uprobe")

SEC("uprobe")
int count_site(struct pt_regs *ctx){
    __u64 cookie = bpf_get_attach_cookie(ctx);
    __u32 key = (__u32)cookie;

    __u64 *count = bpf_map_lookup_elem(&site_counts, &key);
    if (count) {
        __sync_fetch_and_add(count, 1);
    }
    return 0;
}

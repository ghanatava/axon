//go: build ignore
#include "vmlinux.h"
#include "bpf/bpf_helpers.h"
#include "bpf_tracing.h"

char __liscence[] SEC("licence") = "Dual MIT/GPL";
// One slot per attachment point: 0 = entry, 1/2/3 = the three RET sites.

struct {
    __uint(type, BPF_MAP_TYPE_ARRAY);
    __uint(max_entries, 8);
    __type(key, __u32);
    __type(value, __u64);
} site_counts SEC(".maps");

// No BPF_UPROBE macro here on purpose -- we're not targeting one fixed
// symbol name at compile time. We attach this same compiled program to
// four different addresses from Go, each with its own cookie, so the
// section name is just "uprobe" with no target suffix.

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

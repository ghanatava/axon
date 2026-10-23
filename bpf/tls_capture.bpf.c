// Linux amd64 Go TLS capture. Ordinary entry/RET probes; no uretprobe patches.
#include "vmlinux.h"
#include "bpf_helpers.h"
#include "bpf_core_read.h"

char LICENSE[] SEC("license") = "Dual BSD/GPL";
const volatile __u32 register_abi = 1;
const volatile __u64 goid_offset = 152;
const volatile __u32 capture_chunks = 0;
const volatile __u32 target_pid = 0;
const volatile __u64 pidns_ino = 0;
#define MAX_DATA 4096
struct call_key { __u32 pid, direction; __u64 process_start, goid, frame; };
struct call { __u64 conn, buf, length, stack_offset, started, generation; };
struct connection_key { __u32 pid, pad; __u64 process_start, conn; };
struct connection_state {
    __u64 generation, last_active, active[2], sequence[2];
    __u64 failed, closed;
};
struct event {
    __u64 started, finished, goid, conn, total;
    __u32 pid, tid, direction, length;
    __u64 offset, process_start, generation, sequence;
    __u32 kind, failure; // 0=data, 1=close, 2=loss; failure reason is persistent in map.
    __u8 data[MAX_DATA];
};
struct { __uint(type, BPF_MAP_TYPE_HASH); __uint(max_entries, 16384); __type(key, struct call_key); __type(value, struct call); } calls SEC(".maps");
// A lost LRU entry creates a new capture generation; it can never reuse old HPACK.
struct { __uint(type, BPF_MAP_TYPE_LRU_HASH); __uint(max_entries, 65536); __type(key, struct connection_key); __type(value, struct connection_state); } connections SEC(".maps");
struct { __uint(type, BPF_MAP_TYPE_RINGBUF); __uint(max_entries, 1 << 24); } events SEC(".maps");
// memory/identity, map failures, ring drops, truncation, overlap, unattributed loss.
struct { __uint(type, BPF_MAP_TYPE_ARRAY); __uint(max_entries, 6); __type(key, __u32); __type(value, __u64); } stats SEC(".maps");
// perf_event_open and /proc use the agent's PID namespace; the ordinary BPF
// PID helper uses the initial host namespace. Normalize both IDs explicitly
// (essential for nested nodes such as kind, as well as containerized tracers).
static __always_inline __u64 namespace_pid_tgid(void) {
    if (!pidns_ino) return bpf_get_current_pid_tgid();
    struct task_struct *task = (void *)bpf_get_current_task();
    struct pid *thread = BPF_CORE_READ(task, thread_pid);
    struct pid *group = BPF_CORE_READ(task, group_leader, thread_pid);
    __u32 level = BPF_CORE_READ(thread, level);
    // The namespace helper only accepts the task's innermost namespace. Walk
    // ancestor numbers instead, because the agent uses the node namespace.
    for (__u32 i = 0; i < 33; i++) {
        if (i > level) break;
        struct pid_namespace *ns = BPF_CORE_READ(thread, numbers[i].ns);
        if ((__u64)BPF_CORE_READ(ns, ns.inum) == pidns_ino) {
            __u32 tid = BPF_CORE_READ(thread, numbers[i].nr);
            __u32 tgid = BPF_CORE_READ(group, numbers[i].nr);
            return ((__u64)tgid << 32) | tid;
        }
    }
    return 0;
}
// A process exec keeps its PID and birth time. Stop capture in-kernel before
// the new image can emit TLS events; userspace then re-inspects and reattaches.
struct { __uint(type, BPF_MAP_TYPE_ARRAY); __uint(max_entries, 1); __type(key, __u32); __type(value, __u64); } exec_stop SEC(".maps");
SEC("tracepoint/sched/sched_process_exec") int target_exec(void *ctx) {
    if (target_pid && (__u32)(namespace_pid_tgid() >> 32) == target_pid) {
        __u32 key = 0; __u64 stopped = 1;
        bpf_map_update_elem(&exec_stop, &key, &stopped, BPF_ANY);
    }
    return 0;
}
static __always_inline int exec_stopped(void) {
    if (!target_pid) return 0;
    __u32 key = 0; __u64 *stopped = bpf_map_lookup_elem(&exec_stop, &key);
    return stopped && *stopped;
}
static __always_inline void count(__u32 index) {
    __u64 *v = bpf_map_lookup_elem(&stats, &index);
    if (v) __sync_fetch_and_add(v, 1);
}
static __always_inline void axon_fail(struct connection_state *s, __u32 reason) {
    __sync_val_compare_and_swap(&s->failed, 0, reason);
    count(reason - 1);
}
static __always_inline __u64 process_start(void) {
    struct task_struct *task = (void *)bpf_get_current_task();
    return BPF_CORE_READ(task, group_leader, start_boottime);
}
static __always_inline int identity(struct pt_regs *ctx, struct call_key *key, __u64 *lo, __u64 *hi) {
    __u64 g = ctx->r14;
    if (!register_abi) {
        struct task_struct *task = (void *)bpf_get_current_task();
        __u64 fs = BPF_CORE_READ(task, thread.fsbase);
        if (bpf_probe_read_user(&g, 8, (void *)(fs - 8))) return -1;
    }
    if (!g || bpf_probe_read_user(&key->goid, 8, (void *)(g + goid_offset)) || !key->goid ||
        bpf_probe_read_user(lo, 8, (void *)g) || bpf_probe_read_user(hi, 8, (void *)(g + 8)) || *hi < ctx->sp) return -1;
    key->pid = namespace_pid_tgid() >> 32;
    key->process_start = process_start();
    key->direction = bpf_get_attach_cookie(ctx);
    key->frame = *hi - ctx->sp;
    if (!key->pid || !key->process_start || key->direction > 1) return -1;
    return 0;
}
SEC("uprobe") int tls_enter(struct pt_regs *ctx) {
    if (exec_stopped()) return 0;
    struct call_key key = {};
    __u64 lo = 0, hi = 0;
    if (identity(ctx, &key, &lo, &hi)) { count(0); count(5); return 0; }
    struct call c = { .started = bpf_ktime_get_ns() };
    if (register_abi) { c.conn = ctx->ax; c.buf = ctx->bx; c.length = ctx->cx; }
    else {
        __u64 args[3] = {};
        if (bpf_probe_read_user(args, sizeof(args), (void *)(ctx->sp + 8))) { count(0); count(5); return 0; }
        c.conn = args[0]; c.buf = args[1]; c.length = args[2];
    }
    struct connection_key ck = { .pid = key.pid, .process_start = key.process_start, .conn = c.conn };
    struct connection_state *s = bpf_map_lookup_elem(&connections, &ck);
    if (!s) {
        struct connection_state initial = { .generation = c.started, .last_active = c.started };
        bpf_map_update_elem(&connections, &ck, &initial, BPF_NOEXIST);
        s = bpf_map_lookup_elem(&connections, &ck);
        if (!s) { count(1); count(5); return 0; }
    }
    if (s->failed || s->closed) return 0;
    c.generation = s->generation;
    s->last_active = c.started;
    if (c.buf >= lo && c.buf < hi) c.stack_offset = hi - c.buf;
    struct call *previous = bpf_map_lookup_elem(&calls, &key);
    // morestack restarts the entry at the same frame-relative key.
    if (previous) {
        if (previous->generation != c.generation || previous->conn != c.conn) { axon_fail(s, 2); return 0; }
        c.started = previous->started;
    } else {
        __u32 dir = key.direction & 1;
        if (__sync_fetch_and_add(&s->active[dir], 1) != 0) axon_fail(s, 5);
    }
    if (bpf_map_update_elem(&calls, &key, &c, BPF_ANY)) {
        if (!previous) __sync_fetch_and_sub(&s->active[key.direction & 1], 1);
        axon_fail(s, 2);
    }
    return 0;
}
SEC("uprobe") int tls_exit(struct pt_regs *ctx) {
    if (exec_stopped()) return 0;
    struct call_key key = {};
    __u64 lo = 0, hi = 0;
    if (identity(ctx, &key, &lo, &hi)) { count(0); count(5); return 0; }
    struct call *saved = bpf_map_lookup_elem(&calls, &key);
    if (!saved) return 0;
    struct call c = *saved;
    bpf_map_delete_elem(&calls, &key);
    struct connection_key ck = { .pid = key.pid, .process_start = key.process_start, .conn = c.conn };
    struct connection_state *s = bpf_map_lookup_elem(&connections, &ck);
    // Close or capture-map eviction retires this generation. Never join the next one.
    if (!s || s->generation != c.generation) return 0;
    __u32 dir = key.direction & 1;
    if (s->closed || s->failed) goto done;
    __s64 n = ctx->ax;
    if (!register_abi && bpf_probe_read_user(&n, 8, (void *)(ctx->sp + 40))) { axon_fail(s, 1); goto done; }
    if (n <= 0) goto done;
    if ((__u64)n > c.length) { axon_fail(s, 1); goto done; }
    if ((capture_chunks && n > (__s64)(64 * MAX_DATA))) { axon_fail(s, 4); goto done; }
    if (!capture_chunks && n > MAX_DATA) count(3);
    __u64 sequence = __sync_fetch_and_add(&s->sequence[dir], 1) + 1;
    __u64 buf = c.stack_offset ? hi - c.stack_offset : c.buf;
    __u32 tid = (__u32)namespace_pid_tgid();
    for (__u32 index = 0; index < 64; index++) {
        if (index && !capture_chunks) break;
        __u64 offset = (__u64)index * MAX_DATA;
        if (offset >= (__u64)n || s->closed || s->failed) break;
        struct event *e = bpf_ringbuf_reserve(&events, sizeof(*e), 0);
        if (!e) { axon_fail(s, 3); goto done; }
        e->started = c.started; e->finished = bpf_ktime_get_ns(); e->goid = key.goid; e->conn = c.conn;
        e->total = n; e->pid = key.pid; e->tid = tid; e->direction = dir;
        __u64 remaining = (__u64)n - offset;
        __u32 length = MAX_DATA;
        if (remaining < MAX_DATA) length = (__u32)remaining & (MAX_DATA - 1);
        e->length = length; e->offset = offset; e->process_start = key.process_start;
        e->generation = c.generation; e->sequence = sequence; e->kind = 0; e->failure = 0;
        if (bpf_probe_read_user(e->data, length, (void *)(buf + offset))) { axon_fail(s, 1); bpf_ringbuf_discard(e, 0); goto done; }
        bpf_ringbuf_submit(e, 0);
    }
 done:
    s->last_active = bpf_ktime_get_ns();
    __sync_fetch_and_sub(&s->active[dir], 1);
    return 0;
}
SEC("uprobe") int tls_close(struct pt_regs *ctx) {
    if (exec_stopped()) return 0;
    struct connection_key key = { .pid = namespace_pid_tgid() >> 32, .process_start = process_start() };
    key.conn = ctx->ax;
    if (!register_abi && bpf_probe_read_user(&key.conn, 8, (void *)(ctx->sp + 8))) { count(0); count(5); return 0; }
    struct connection_state *s = bpf_map_lookup_elem(&connections, &key);
    if (!s) return 0;
    s->closed = 1;
    struct event *e = bpf_ringbuf_reserve(&events, sizeof(*e), 0);
    if (e) {
        e->started = bpf_ktime_get_ns(); e->finished = e->started;
        e->goid = 0; e->conn = key.conn; e->total = 0; e->pid = key.pid;
        e->tid = (__u32)namespace_pid_tgid(); e->direction = 0; e->length = 0; e->offset = 0;
        e->process_start = key.process_start; e->generation = s->generation; e->sequence = 0;
        e->kind = 1; e->failure = s->failed;
        bpf_ringbuf_submit(e, 0);
    } else { count(2); }
    bpf_map_delete_elem(&connections, &key);
    return 0;
}

/*
 * tc_dribble_detect.c
 *
 * eBPF program attached via kprobe to tcp_recvmsg, tracking per-flow packet
 * size and inter-arrival statistics to catch gaslighter's dribble strategy
 * (see ../dribble-and-har.md) at the kernel layer, before any bytes reach
 * the JVM's Netty pipeline.
 *
 * Why a kprobe on tcp_recvmsg instead of XDP:
 *   XDP sees packets before the kernel's TCP stack reassembles them - it's
 *   the right layer for connection-rate limiting (xdp_connguard.c) but the
 *   wrong layer for "how many bytes did this established flow deliver, and
 *   how were they spaced over time," because that requires the reassembled
 *   stream view tcp_recvmsg operates on (post-ACK, in-order, one call per
 *   userspace read). A kprobe on the receive path gives us exactly the
 *   granularity gaslighter's dribble is built to exploit: single-byte
 *   payloads, arriving on a fixed multi-second cadence, on a connection
 *   that's otherwise indistinguishable from idle.
 *
 * What "dribble" looks like at this layer, per gaslighter/README.rst:
 *   - A connection sends a 3-byte VarInt frame-length header once
 *     (0xFF 0xFF 0x03 -> declares a 65,535-byte incoming frame), then drips
 *     ONE FILLER BYTE per --dribble-interval (default 5s, so ~91h to
 *     "complete" the frame it never intends to complete).
 *   - Every recvmsg() call after the header is ~1 byte, evenly spaced,
 *     forever. Real Minecraft traffic - even slow/lossy real clients -
 *     does not produce sustained single-byte reads on a multi-second
 *     cadence; TCP either delivers a real chunk of a real packet, or the
 *     connection is genuinely idle (no bytes at all, which this program
 *     does not flag - only sustained *trickle* is suspicious, not silence).
 *
 * This program does NOT decrypt or parse Minecraft protocol content - the
 * signature is purely traffic-shape (byte count + timing), so it works
 * identically whether the connection is in Login, Configuration, or Play
 * state, and whether or not AES/CFB8 encryption (gaslighter's Encryption
 * Response handling) is in use.
 *
 * Verdict flow:
 *   flow crosses SMALL_READ_STREAK sustained small reads, each spaced within
 *   [MIN_GAP_NS, MAX_GAP_NS] of the last  ->  flow_verdicts map entry set
 *   -> userspace daemon (dribble_watchdog, see build notes below) polls the
 *      map, and on a confirmed verdict, writes the source IP into
 *      xdp_connguard.c's `blocklist` map, closing the loop between
 *      detection (this program) and enforcement (the XDP drop).
 *
 * Build:
 *   clang -O2 -g -target bpf -D__TARGET_ARCH_x86 \
 *     -c tc_dribble_detect.c -o tc_dribble_detect.o
 *
 * Attach (kprobe, not a real tc classifier despite the filename - named for
 * where it sits conceptually in the pipeline, "before it reaches userspace"):
 *   bpftool prog load tc_dribble_detect.o /sys/fs/bpf/dribble_detect
 *   bpftool prog attach ... # or, more simply, a libbpf skeleton loader
 *   that opens a kprobe on tcp_recvmsg and attaches this SEC("kprobe/...")
 *
 * A minimal userspace loader/poller is intentionally not included here -
 * wire flow_verdicts up to whatever alerting/blocklist-sync daemon fits your
 * deployment (systemd service reading the pinned map every few seconds is
 * sufficient; this doesn't need to be real-time to the millisecond).
 */

#include <linux/bpf.h>
#include <linux/in.h>
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_tracing.h>

#define MC_PORT             25565
#define SMALL_READ_MAX_BYTES  4      /* dribble sends 1 byte; some slack for framing */
#define MIN_GAP_NS   (1500ULL * 1000000ULL)   /* 1.5s - below default --dribble-interval */
#define MAX_GAP_NS   (60000ULL * 1000000ULL)  /* 60s - above it we assume genuine idle, not trickle */
#define SMALL_READ_STREAK   10        /* consecutive qualifying reads before verdict */

/* Flow key: 4-tuple, collapsed to (remote IP, remote port) since this box is
 * always the local side of every flow we care about. */
struct flow_key {
    __u32 remote_ip;
    __u16 remote_port;
    __u16 pad;
};

struct flow_state {
    __u64 last_read_ns;
    __u32 small_read_streak;
    __u32 total_bytes_seen;   /* sanity cap - see MAX_TOTAL_BYTES below */
};

struct {
    __uint(type, BPF_MAP_TYPE_LRU_HASH);
    __uint(max_entries, 1 << 15);
    __type(key, struct flow_key);
    __type(value, struct flow_state);
} flow_states SEC(".maps");

/* Confirmed verdicts - source IP only (matches xdp_connguard.c's blocklist
 * key shape so a userspace daemon can copy entries across 1:1). */
struct {
    __uint(type, BPF_MAP_TYPE_LRU_HASH);
    __uint(max_entries, 1 << 12);
    __type(key, __u32);
    __type(value, __u8);
} flow_verdicts SEC(".maps");

/* A single confirmed dribble frame is at most 65,535 bytes per
 * gaslighter's header (0xFF 0xFF 0x03). If total_bytes_seen ever exceeds
 * that on a flow we're tracking, the frame either completed or was never
 * the attack shape to begin with - stop tracking rather than false-flag a
 * legitimately slow-but-real large transfer. */
#define MAX_TOTAL_BYTES 65535

/* tcp_recvmsg(struct sock *sk, struct msghdr *msg, size_t len, int flags,
 *             int *addr_len)  -- kernel signature, args accessed via
 * BPF_KPROBE for portability across kernel versions where field offsets in
 * struct sock differ; we only need sk to pull the remote endpoint via
 * bpf_probe_read_kernel, and the return-probe byte count via BPF_KRETPROBE.
 *
 * For brevity this sketch reads remote endpoint fields directly off `sk`
 * (inet_sock layout) - on kernels where this drifts, prefer BTF-relocatable
 * field access (BPF_CORE_READ) instead of raw offsets. Shown here in the
 * simpler raw form to keep the illustrative example short; swap in CO-RE
 * reads for a production deployment across mixed kernel versions.
 */
struct sock_common {
    __u32 skc_daddr;
    __u32 skc_rcv_saddr;
    __u16 skc_dport;
    /* ... rest elided, CO-RE access recommended for real deployment ... */
};

SEC("kprobe/tcp_recvmsg")
int BPF_KPROBE(trace_tcp_recvmsg_entry, void *sk)
{
    /* Entry probe: nothing to do yet, byte count isn't known until return.
     * Kept as a named entry point for symmetry / future use (e.g. stashing
     * a start timestamp for read-latency metrics) rather than doing all
     * work in the kretprobe. */
    return 0;
}

SEC("kretprobe/tcp_recvmsg")
int BPF_KRETPROBE(trace_tcp_recvmsg_return, int ret)
{
    if (ret <= 0)
        return 0; /* error or EOF, not a data read */

    /* NOTE: recovering `sk` here requires stashing it from the entry probe
     * (e.g. in a small per-thread scratch map keyed on bpf_get_current_pid_tgid())
     * since kretprobes only receive the return value, not the original args.
     * Elided for brevity - see build notes above re: CO-RE / skeleton loader,
     * which is where this plumbing belongs in a real deployment. The logic
     * below assumes `key` has already been populated from that stash.
     */
    struct flow_key key = {0}; /* populate from entry-probe stash in real build */

    if (bpf_ntohs(key.remote_port) == 0)
        return 0;

    __u64 now = bpf_ktime_get_ns();
    struct flow_state *st = bpf_map_lookup_elem(&flow_states, &key);
    struct flow_state newst;

    if (!st) {
        newst.last_read_ns = now;
        newst.small_read_streak = (ret <= SMALL_READ_MAX_BYTES) ? 1 : 0;
        newst.total_bytes_seen = ret;
        bpf_map_update_elem(&flow_states, &key, &newst, BPF_ANY);
        return 0;
    }

    __u64 gap = now - st->last_read_ns;
    __u32 total = st->total_bytes_seen + ret;

    if (total > MAX_TOTAL_BYTES) {
        /* Either the frame legitimately completed or this was never the
         * dribble shape - drop tracking rather than risk a false positive
         * on a real large transfer that happens to arrive in small chunks
         * near the end. */
        bpf_map_delete_elem(&flow_states, &key);
        return 0;
    }

    __u32 streak = st->small_read_streak;
    if (ret <= SMALL_READ_MAX_BYTES && gap >= MIN_GAP_NS && gap <= MAX_GAP_NS) {
        streak += 1;
    } else {
        /* A real chunk arrived, or the timing doesn't match sustained
         * trickle - reset. A single large read (e.g. a real Plugin Message)
         * is exactly the "innocent" case this guard against false-flagging. */
        streak = 0;
    }

    if (streak >= SMALL_READ_STREAK) {
        __u8 blocked = 1;
        bpf_map_update_elem(&flow_verdicts, &key.remote_ip, &blocked, BPF_ANY);
        bpf_map_delete_elem(&flow_states, &key);
        return 0;
    }

    newst.last_read_ns = now;
    newst.small_read_streak = streak;
    newst.total_bytes_seen = total;
    bpf_map_update_elem(&flow_states, &key, &newst, BPF_EXIST);
    return 0;
}

char _license[] SEC("license") = "GPL";

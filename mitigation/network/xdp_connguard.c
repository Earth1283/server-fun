/*
 * xdp_connguard.c
 *
 * XDP program: per-source-IP new-connection rate limiter for the Minecraft
 * listener port. Drops excess SYNs before they ever reach the socket layer,
 * which means they never reach Netty, which means they never reach the JVM
 * heap that gaslighter is trying to bloat.
 *
 * This exists because gaslighter's --join-delay flag is explicitly designed
 * to throttle the attacker down to match a naive per-connection rate limit
 * (see gaslighter/README.rst: "bypassing server-side connection throttling").
 * A single global counter is trivial to slide under with --join-delay tuned
 * to match. Per-SOURCE-IP counting closes that gap: --join-delay limits the
 * COMBINED rate across all workers, but if all those workers share one IP
 * (no --proxies), a per-IP limit hits the same ceiling gaslighter is already
 * trying to respect - it just enforces it whether the attacker "agrees" to
 * or not. Distributing across a SOCKS5 proxy pool (-p proxies.txt) still
 * gets caught per source IP; it just requires more distinct IPs to reach the
 * same aggregate connection rate, which is the actual point of a network
 * layer defense: raise the number of distinct source IPs required, don't
 * pretend to make it zero.
 *
 * Design:
 *   - BPF_MAP_TYPE_LRU_HASH keyed on source IPv4, holding a fixed-size
 *     sliding-window bucket (token count + last-refill timestamp).
 *   - Token bucket refills continuously based on elapsed time, capped at
 *     BURST. This is the standard XDP-friendly rate limiter shape because it
 *     needs no timers, only bpf_ktime_get_ns() read at packet time.
 *   - Only touches new connection attempts (TCP SYN, no ACK) destined for
 *     MC_PORT. Established connections (the actual Keep Alive / dribble
 *     traffic) pass through untouched here - that traffic is handled by
 *     tc_dribble_detect.c instead, since by definition it's traffic FROM
 *     already-accepted connections.
 *   - A separate pinned map (blocklist) lets a userspace daemon (fed by
 *     tc_dribble_detect.c's flow stats, or by an admin) hard-drop a source IP
 *     regardless of its current token count. This is how the dribble
 *     detector and the connection-rate guard share enforcement: one detects
 *     slow-trickle abuse over time, the other enforces the resulting verdict
 *     at the earliest possible point (before a single packet even reaches
 *     the socket).
 *
 * Build:
 *   clang -O2 -g -target bpf -D__TARGET_ARCH_x86 \
 *     -c xdp_connguard.c -o xdp_connguard.o
 *
 * Load (see ../configs/systemd/gaslighter-shield.service for a persistent unit):
 *   ip link set dev eth0 xdp obj xdp_connguard.o sec xdp
 *   bpftool net show   # verify attachment
 *
 * Tune MC_PORT, BURST, REFILL_NS below for your deployment before building.
 */

#include <linux/bpf.h>
#include <linux/if_ether.h>
#include <linux/ip.h>
#include <linux/tcp.h>
#include <linux/in.h>
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_endian.h>

#define MC_PORT      25565      /* your listener port - server.properties */
#define BURST        5          /* max SYNs a single source IP may burst */
#define REFILL_NS    2000000000ULL  /* 1 token per 2s per source IP, steady-state */
#define NS_PER_TOKEN REFILL_NS

struct bucket {
    __u64 tokens;      /* fixed-point: tokens * REFILL_NS, avoids float math */
    __u64 last_ns;
};

struct {
    __uint(type, BPF_MAP_TYPE_LRU_HASH);
    __uint(max_entries, 1 << 16);
    __type(key, __u32);          /* source IPv4, network byte order */
    __type(value, struct bucket);
} conn_buckets SEC(".maps");

struct {
    __uint(type, BPF_MAP_TYPE_LRU_HASH);
    __uint(max_entries, 1 << 14);
    __type(key, __u32);          /* source IPv4 */
    __type(value, __u8);         /* 1 = hard blocked, populated by userspace */
} blocklist SEC(".maps");

/* Diagnostics counters, readable from userspace for the alerting side of
 * g1gc-heap-exhaustion.md's "detect the thing while it's happening" section. */
struct {
    __uint(type, BPF_MAP_TYPE_ARRAY);
    __uint(max_entries, 3); /* 0 = passed, 1 = rate-limited, 2 = blocklisted */
    __type(key, __u32);
    __type(value, __u64);
} stats SEC(".maps");

static __always_inline void bump_stat(__u32 idx)
{
    __u64 *v = bpf_map_lookup_elem(&stats, &idx);
    if (v)
        __sync_fetch_and_add(v, 1);
}

SEC("xdp")
int xdp_connguard_prog(struct xdp_md *ctx)
{
    void *data_end = (void *)(long)ctx->data_end;
    void *data     = (void *)(long)ctx->data;

    struct ethhdr *eth = data;
    if ((void *)(eth + 1) > data_end)
        return XDP_PASS;
    if (eth->h_proto != bpf_htons(ETH_P_IP))
        return XDP_PASS;

    struct iphdr *ip = (void *)(eth + 1);
    if ((void *)(ip + 1) > data_end)
        return XDP_PASS;
    if (ip->protocol != IPPROTO_TCP)
        return XDP_PASS;

    /* IP header can have options; compute real offset instead of assuming 20B. */
    __u32 ip_hlen = ip->ihl * 4;
    if (ip_hlen < sizeof(struct iphdr))
        return XDP_PASS;
    struct tcphdr *tcp = (void *)ip + ip_hlen;
    if ((void *)(tcp + 1) > data_end)
        return XDP_PASS;

    if (bpf_ntohs(tcp->dest) != MC_PORT)
        return XDP_PASS;

    /* Only gate new connection attempts: SYN set, ACK not set. Established
     * traffic (the actual held connections) is out of scope for this prog. */
    if (!(tcp->syn && !tcp->ack))
        return XDP_PASS;

    __u32 src_ip = ip->saddr;

    __u8 *blocked = bpf_map_lookup_elem(&blocklist, &src_ip);
    if (blocked && *blocked) {
        bump_stat(2);
        return XDP_DROP;
    }

    __u64 now = bpf_ktime_get_ns();
    struct bucket *b = bpf_map_lookup_elem(&conn_buckets, &src_ip);
    struct bucket newb;

    if (!b) {
        newb.tokens  = (__u64)(BURST - 1) * NS_PER_TOKEN; /* consume one on first SYN */
        newb.last_ns = now;
        bpf_map_update_elem(&conn_buckets, &src_ip, &newb, BPF_ANY);
        bump_stat(0);
        return XDP_PASS;
    }

    __u64 elapsed = now - b->last_ns;
    __u64 max_tokens = (__u64)BURST * NS_PER_TOKEN;
    __u64 tokens = b->tokens + elapsed;
    if (tokens > max_tokens)
        tokens = max_tokens;

    if (tokens < NS_PER_TOKEN) {
        /* No tokens available - this source IP is opening connections faster
         * than REFILL_NS allows. This is exactly the pattern --join-delay
         * produces when an attacker forgets (or doesn't bother) to spread
         * across proxies: many SYNs from one IP, evenly paced, forever. */
        b->last_ns = now;
        bpf_map_update_elem(&conn_buckets, &src_ip, b, BPF_EXIST);
        bump_stat(1);
        return XDP_DROP;
    }

    newb.tokens  = tokens - NS_PER_TOKEN;
    newb.last_ns = now;
    bpf_map_update_elem(&conn_buckets, &src_ip, &newb, BPF_EXIST);
    bump_stat(0);
    return XDP_PASS;
}

char _license[] SEC("license") = "GPL";

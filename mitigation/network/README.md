# Network-layer mitigations (eBPF)

Kernel-level defenses that catch `gaslighter` traffic before it costs the JVM anything —
before Netty allocates a buffer, before a plugin event fires, before a single byte
reaches application code. Think of it as the Secure Enclave of this mitigation stack:
nothing gets to userspace without clearing hardware-adjacent scrutiny first. These are the highest-effort, most "over-engineered" mitigations
in this directory on purpose: everything in the parent doc set (`../g1gc-heap-exhaustion.md`,
`../dribble-and-har.md`) assumes the connection made it to userspace. These two programs
are about not letting it get that far.

| File | Attaches to | Catches |
|---|---|---|
| [`xdp_connguard.c`](xdp_connguard.c) | XDP (driver/generic hook on the NIC) | New-connection floods, per-source-IP — the thing `--join-delay` is built to slide under |
| [`tc_dribble_detect.c`](tc_dribble_detect.c) | kprobe on `tcp_recvmsg` | The dribble strategy's byte-trickle shape, independent of protocol state or encryption |

Both are sketches meant to be read and adapted, not `make`-and-forget. In particular
`tc_dribble_detect.c` has an explicit gap (recovering `sk`/flow key across a
kretprobe requires a small entry-probe scratch map, or better, a `fentry`/`fexit` pair on
kernels new enough to support BTF-based tracing without kprobe overhead) — that plumbing
is deliberately left as a comment pointing at "build a libbpf skeleton" rather than
inlined, because the interesting part of this mitigation is the *detection logic and
why it's shaped this way*, not boilerplate.

## Why two separate programs instead of one

`xdp_connguard.c` operates on **new connection attempts** (SYN, no ACK) — it has to run
at the earliest possible hook (XDP, before `sk_buff` allocation) because the entire point
is rejecting a flood before the kernel commits any per-connection state to it.

`tc_dribble_detect.c` operates on **established, accepted connections** that are already
past the point XDP would have dropped them — by definition, since the dribble strategy
only starts after a successful Login-state exchange (or a kicked login that still had a
valid TCP connection). This has to run later in the stack, where reassembled stream data
is visible, because the signature it's looking for (single-byte reads, evenly spaced) only
exists at that granularity.

They're linked by a shared enforcement surface: `tc_dribble_detect.c`'s `flow_verdicts`
map and `xdp_connguard.c`'s `blocklist` map use the same key shape (source IPv4) on
purpose, so a small userspace daemon can copy confirmed dribble verdicts into the
connection guard's blocklist — once a source IP is caught dribbling, its *future* new
connection attempts get dropped at the earliest hook too, not just the flow that got
caught.

## Build requirements

- `clang`/`llvm` with BPF target support (`clang -target bpf`)
- Kernel headers for your running kernel (`linux-headers-$(uname -r)` or equivalent)
- `libbpf-dev` (provides `bpf/bpf_helpers.h`, `bpf/bpf_endian.h`, `bpf/bpf_tracing.h`)
- `bpftool` for loading/inspecting/pinning maps
- A kernel with BTF enabled (`CONFIG_DEBUG_INFO_BTF=y`) if you upgrade the kretprobe
  plumbing in `tc_dribble_detect.c` to CO-RE-relocatable field access, which is the
  right move before running this across a fleet of mixed kernel versions.

```bash
clang -O2 -g -target bpf -D__TARGET_ARCH_x86 -c xdp_connguard.c -o xdp_connguard.o
clang -O2 -g -target bpf -D__TARGET_ARCH_x86 -c tc_dribble_detect.c -o tc_dribble_detect.o
```

Neither file compiles standalone with a plain userspace `clang` invocation and no
`-I` pointed at kernel/libbpf headers — that's expected; they're kernel-side eBPF
objects, not regular C programs. See `../configs/systemd/gaslighter-shield.service`
for a persistent-loading unit once you've built the objects and written the (equally
sketched) userspace loader/verdict-sync daemon that ties the two maps together.

## Deployment order

1. Load `xdp_connguard.c` first, alone, in monitor mode (log-only — flip `XDP_DROP` to
   `XDP_PASS` with a stat bump while validating) against real player traffic for at least
   a day. Real players reconnecting after a crash, a proxy's health-check pings, and
   Velocity's own backend keep-alive can all look like rapid-fire SYNs from one IP if
   your thresholds are too tight — tune `BURST`/`REFILL_NS` against your own baseline
   before enforcing drops.
2. Add `tc_dribble_detect.c` once the connection guard's false-positive rate against real
   traffic is acceptable. Its thresholds (`SMALL_READ_MAX_BYTES`, `MIN_GAP_NS`,
   `MAX_GAP_NS`, `SMALL_READ_STREAK`) are keyed to `gaslighter`'s *default*
   `--dribble-interval` (5s) — an attacker who widens that flag to, say, 30s still fits
   inside `MAX_GAP_NS` and still gets caught, just after a longer streak; one who narrows
   it below `MIN_GAP_NS` starts looking like a real fast client and needs the streak
   count, not the gap window, to catch it. Revisit both constants if `gaslighter`'s
   defaults change.
3. Wire the verdict-sync daemon last, and alert on every write to `blocklist` before you
   trust it to run unattended — an automated system that can ban IPs with no human in the
   loop is exactly the kind of thing you want a paper trail for on day one.

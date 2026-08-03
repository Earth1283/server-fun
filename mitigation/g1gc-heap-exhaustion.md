# Mitigating the G1GC Heap Harvest

Target: `gaslighter`'s default mode (no `--prelogin`, no `--wander`) — the "Connect &
Bloat → Login → Hold Hostage → Premature Promotion" pipeline described in
`gaslighter/README.rst`. This is the one that gets a `.hprof` file written and an admin
taking up gardening.

## What's actually happening, restated as a defender

`gaslighter` wins by making three things true simultaneously:

1. Every connection allocates a heap-resident object (the 255-byte `serverAddress`
   `String`, deserialized by Netty off the handshake packet) that your server has no
   reason to ever free, because the connection is never closed.
2. Every one of those strings is unique per-connection (`randString` seeded per-worker),
   so `-XX:+UseStringDeduplication` — G1's normal answer to "too many identical strings"
   — cannot merge them. Dedup only helps against *repeated* garbage; this is deliberately
   *unique* garbage.
3. The connection count is large enough, and held long enough, that objects survive
   enough Minor GCs to get tenured into the Old Generation before you notice anything is
   wrong (the reporter line only exists on the attacker's terminal, not yours).

None of the three requires exploiting a bug. Slowloris-family attacks work by using the
server exactly as documented. So the fix is not "patch the vulnerability" — there isn't
one — it's "make the three preconditions false."

## Mitigation 1: cap what a connection is allowed to cost before Login even starts

The cheapest fix is the one that stops the object from being created at full size in the
first place.

**Velocity / BungeeCord as a mandatory front door.** Don't expose the backend Paper/Spigot
port to the internet at all — proxy everything through Velocity, which:

- Terminates the handshake itself and only forwards a connection to the backend after its
  own login sequence completes, so a connection that never reaches Login state never
  touches backend heap.
- Lets you set `login-ratelimit` in `velocity.toml` (see `configs/velocity.toml`), which
  throttles *new login attempts per source IP*, not per raw TCP connection — this matters
  because `gaslighter`'s `--join-delay` is specifically designed to slide under a
  connection-rate limiter, and a login-rate limiter forces the same 30s login-timeout
  clock the attacker is already racing.
- Ships with `player-info-forwarding-mode = "modern"`, which requires Mojang session
  validation to cross into the backend — see Mitigation 3.

**If you must expose Paper/Spigot directly**, the handshake `serverAddress` field has no
legitimate reason to be anywhere near 255 bytes for a server with a fixed, known set of
valid hostnames (your domain + any SRV aliases). A `PacketListenerAPI`/Protocol-Lib hook
that inspects the raw Handshake packet and disconnects immediately on
`len(serverAddress) > 64` (or whatever your longest real hostname is, plus slack) turns
the 255-byte bloat string into a same-tick rejection instead of a heap-resident object.
This has to happen at the packet-decode layer, before the string is handed to any plugin
event — by the time `AsyncPlayerPreLoginEvent` fires, the `String` already exists and the
damage (one Eden allocation) is already done. It's cheap damage per-connection, but at
10,000 workers it's the whole attack.

## Mitigation 2: make "held forever" impossible

The Keep Alive hold strategy (Play state, responding to `0x26` every ~15s) is
indistinguishable from a legitimate AFK player at the protocol level — the server has no
signal that this connection is fake other than *it never does anything else*. That's a
behavioral tell, and it's covered in depth by [`wander-bot-detection.md`](wander-bot-detection.md)
(which also covers why `--wander` exists specifically to defeat naive versions of this
check). The short version: track per-connection "last non-Keep-Alive client packet"
timestamp and kick anything silent past a threshold generous enough for real AFK players
(5–10 minutes) but well short of "indefinitely."

**Global connection ceiling, independent of per-IP limits.** `gaslighter` defaults to
10,000 workers and openly supports SOCKS5 proxy rotation specifically to spread
connections across source IPs. A per-IP cap does nothing against a large proxy pool. Set
a hard ceiling on total concurrent connections in Play state
(`Bukkit.getServer().getMaxPlayers()` won't help — that's a login-time check, not an
ongoing one) via a repeating task that counts real vs. suspicious connections and starts
kicking the least-active ones once the total crosses a threshold your hardware can
actually GC through. This turns an unbounded heap bloat into a bounded, self-limiting one.

## Mitigation 3: require a Mojang session for every held connection

This is the highest-leverage single change and it's a config flag, not code:

```
# server.properties
online-mode=true
```

Read `gaslighter/README.rst`'s own encryption support section: online-mode *with*
credentials completes the full RSA/AES handshake and calls
`sessionserver.mojang.com/session/minecraft/join` — meaning the attacker needs a real,
non-banned Mojang account per concurrent connection. Ten thousand of those is not a
`randString()` call, it's an acquisition problem, and a much more expensive one than
writing Go. Online-mode *without* credentials still completes encryption but fails the
Mojang join and gets kicked with "Failed to verify username!" — at which point the worker
falls back to the **dribble strategy**, which is a different, weaker attack covered in
[`dribble-and-har.md`](dribble-and-har.md). Turning on online-mode doesn't stop
`gaslighter` outright, but it forces every worker that wants Play-state persistence
(the strong hold) down to the dribble fallback (the weak one) unless the attacker is
sitting on thousands of real accounts.

If you run a proxy network with velocity-modern forwarding, backend servers can trust the
proxy's forwarded (already-verified) identity and skip re-verifying, so this cost is paid
once at the edge.

## Mitigation 4: assume some bloat gets through, and switch off the GC being exploited

Everything above raises the cost of the attack; nothing makes it zero, because a
sufficiently patient/well-resourced attacker with real accounts and a plausible
`serverAddress` length can still hold real Play-state connections. The last layer is
making sure that when Old Gen fills anyway, you get a controlled degradation instead of a
multi-second stop-the-world Full GC stall.

**Recommended: switch to Generational ZGC.** The attack is named after G1 for a reason —
it targets G1's Full GC fallback specifically. Tested against `gaslighter` on the same
AMD EPYC hardware the tool's own README references, Generational ZGC (`-XX:+UseZGC
-XX:+ZGenerational`, JDK 21+) survived the attack with minimal issues — no stall, no OOM.
This is a drop-in JVM flag change, not an architecture change; every other mitigation in
this document stacks on top of it. Full writeup, flags, and G1-tuning fallback (if you're
stuck pre-JDK 21) in [`jvm-tuning.md`](jvm-tuning.md).

## Mitigation 5: detect the thing while it's happening, not after the `.hprof`

`gaslighter`'s own reporter (`Active | New/s | Dropped | Sent`) only prints to the
attacker. Build the mirror image server-side: alert on

- concurrent connection count crossing N with average per-connection idle time > threshold,
- Eden→Old promotion rate (from `-Xlog:gc*`) spiking above baseline,
- a burst of `AsyncPlayerPreLoginEvent` firings without matching `PlayerJoinEvent`s
  (covered in [`dribble-and-har.md`](dribble-and-har.md)).

`pulse/` in this same repo is, not coincidentally, exactly the tool you'd point at your
*own* server from a monitoring box to watch these numbers in real time instead of finding
out from a crashed process and a GC log after the fact.

## What doesn't help

- **`-XX:+UseStringDeduplication` alone.** Covered above — the strings are unique by
  design specifically to defeat this.
- **Bigger heap (`-Xmx32G` instead of `16G`).** Delays the Full GC, doesn't prevent it.
  More heap means a *longer* stop-the-world pause when it finally happens, because there's
  more Old Gen to walk.
- **Naive IP-based firewall rules with no connection-rate awareness.** See
  [`network/`](network/) — a static blocklist doesn't help against a proxy pool that
  rotates, and a rate limit that only counts SYNs (not held-connection duration) doesn't
  catch a slow, patient attacker running well under any reasonable per-second threshold.

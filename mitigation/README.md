# mitigation 🩹

The methadone clinic to `gaslighter`'s bender. Everything in this directory exists to undo
the tricks documented in [`../gaslighter/README.rst`](../gaslighter/README.rst) — connection
by connection, heuristic by heuristic. If you run the "crown jewel" against your own
infrastructure and it works, this is where you come to make it stop working.

This is defensive-only. Nothing here sends a packet at anyone; it's all detection, rate
limiting, and JVM/kernel tuning applied to a server you administer — build your own walled garden.

## Map of the clinic

| Doc | Attack it treats | Layer |
|---|---|---|
| [`g1gc-heap-exhaustion.md`](g1gc-heap-exhaustion.md) | Default mode — Eden overcrowding, Old Gen promotion, Full GC stall | JVM / Netty |
| [`dribble-and-har.md`](dribble-and-har.md) | Dribble fallback (91h half-frames) + `--prelogin --har` spam | Netty / plugin |
| [`wander-bot-detection.md`](wander-bot-detection.md) | `--wander` — bots faking legitimate movement to dodge AFK/idle kicks | Plugin (behavioral) |
| [`jvm-tuning.md`](jvm-tuning.md) | The Full GC pause itself, independent of how the heap got full | JVM flags |
| [`network/`](network/) | Everything upstream of the JVM — per-IP connection floods, proxy-distributed slow trickles | Kernel (XDP / eBPF) |
| [`configs/`](configs/) | Copy-paste `nftables`, Paper, Velocity, and systemd units implementing the above | Config |

## Reading order

If you just got hit and don't know which flag was used against you, start with
`g1gc-heap-exhaustion.md` — it covers the always-on default attack path and the two
network-layer defenses (`network/xdp_connguard.c`, `network/tc_dribble_detect.c`) that catch
everything else as a side effect. The other docs are refinements for the specific flags
that slip past the baseline (`--prelogin --har`, `--dribble-interval`, `--wander`).

## Threat model recap

Reread the source before trusting any mitigation below — flag names and packet IDs here
are frozen at the time this was written and `gaslighter/main.go` is the ground truth:

- **Attacker-controlled inputs**: handshake `serverAddress` (up to 255 bytes, one per
  connection, deliberately unique to defeat JVM string interning), login username,
  connection count, connection lifetime, per-connection write cadence.
- **What the attacker does NOT control**: your JVM flags, your firewall, your plugin
  stack, or (per real Mojang online-mode) a valid session for every one of 10,000 fake
  players.
- **The actual scarce resource being attacked**: JVM Old Generation heap and Netty event
  loop threads — not bandwidth. Every mitigation here is about making a held connection
  cheap to hold, cheap to detect, and cheap to drop, rather than about surviving raw
  packet volume.

## Legalish stuff (inherited)

Same rule as the rest of the repo: run this against infrastructure you own or are
explicitly authorized to defend. If you're deploying the eBPF programs in `network/` on a
box you don't administer, that's a `sudo` prompt you shouldn't be answering.

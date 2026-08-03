# JVM/GC Tuning: Surviving the Full GC Stall Even If Bloat Gets Through

Everything in [`g1gc-heap-exhaustion.md`](g1gc-heap-exhaustion.md) is about raising the
*cost* of the attack — fewer connections get through, the ones that do get through hold
less garbage, and the ones holding garbage get caught faster. This doc is the last line:
assume some amount of heap bloat happens anyway, and pick a GC that doesn't turn "Old Gen
is full" into "the server freezes for several seconds while the world watches."

## Recommendation: switch off G1 entirely — use Generational ZGC

`gaslighter`'s entire premise (per its own README: *"exploiting the Generational
Garbage-First Garbage Collector"*) is that G1's stop-the-world Full GC is the failure
mode being engineered toward. The most direct mitigation is to stop giving it a G1 to
attack.

**This isn't theoretical here — it's been load-tested against the exact attack this repo
ships.** Running the same `gaslighter` binary against a Paper server on the same AMD EPYC
hardware the tool's README calls out as its reference target, switching from G1 to
Generational ZGC (`-XX:+UseZGC` on JDK 21+, which defaults to generational mode) survived
the attack with minimal degradation — no Full GC stall, no OOM, no heap dump. That's the
strongest evidence available for any mitigation in this directory: not "should work in
theory," but "held up against this specific tool on this specific hardware."

```
java \
  -Xms16G -Xmx16G \
  -XX:+UseZGC \
  -XX:+ZGenerational \
  -Xlog:gc*:file=/tmp/gc.log:time,uptime,level,tags \
  -jar server.jar nogui
```

(On JDK 21+, `-XX:+ZGenerational` is implied by default when `-XX:+UseZGC` is set — pass
it explicitly anyway for clarity and to stay correct on JDKs where the default flips
back, and pin your JDK version so that assumption doesn't silently rot.)

### Why ZGC survives what G1 doesn't

G1 divides the heap into regions and still performs occasional **stop-the-world Full
GC** when it can't keep up with promotion — which is exactly the scenario `gaslighter`
manufactures: thousands of surviving objects promoted faster than G1's concurrent
marking can reclaim Old Gen space, forcing the fallback to a full, synchronous,
everything-pauses collection. That pause is the entire attack payoff — it's what turns
"heap pressure" into "the server is unresponsive."

ZGC (and Generational ZGC specifically, which adds a young generation to reduce
overhead on short-lived objects — the common case for a normal game tick) is designed
around **sub-millisecond max pause times regardless of heap size**, using
colored-pointer-based concurrent marking and relocation that doesn't require stopping
application threads for anything but tiny, fixed-cost root-scanning pauses. Under
`gaslighter`'s load pattern:

- The bloat objects (per-connection handshake strings) still get allocated and still
  age — ZGC doesn't make the garbage disappear, it just never needs a stop-the-world
  pause to deal with it, because relocation happens concurrently with the application
  still running.
- If Old Gen genuinely fills (attacker connection count large enough, or sustained long
  enough), ZGC's response is allocation stalls or, at the true limit, an OOM — but it
  gets there via gradual backpressure rather than a multi-second freeze, which in
  practice means the reporter-visible "server got slow" happens well before "server is
  down," giving admins a window to react (kill workers, ban IPs, restart) instead of a
  cliff.
- No behavior change is required anywhere else in this document — ZGC is a drop-in GC
  swap, not an architecture change. Every other mitigation in this directory (online-mode,
  handshake length caps, connection ceilings, kernel-layer detection) still applies and
  still reduces load on top of a GC that already doesn't buckle under what gets through.
  It's basically the Apple Silicon transition of garbage collectors — swap the engine
  under the hood, keep everything else running, and the stutter just isn't there anymore.

### Practical notes

- Requires JDK 17+ for ZGC; **Generational ZGC needs JDK 21+** — this repo already
  standardized the fleet on Java 21 (`use-java21.sh`), so there's no version blocker.
- ZGC trades some throughput and has a higher baseline memory overhead than G1 for the
  same heap size (colored pointers reserve extra address space, though this is virtual,
  not resident, memory — check `-Xmx` headroom against real RAM, not address space).
  On a dedicated game server this trade is close to free: you're optimizing for *tail
  latency and pause time*, not raw allocation throughput, and Minecraft's own tick loop
  cares far more about "did we miss a tick" than "how many objects/sec can we allocate."
- Keep `-Xlog:gc*` on regardless of GC choice — ZGC's log format differs from G1's, and
  you want a pre-incident baseline of what "normal" ZGC pause/allocation-stall behavior
  looks like on your hardware before you're trying to read it during a live attack.
- Re-run your own load test after switching. The result above is one data point on one
  hardware target (EPYC) — validate on whatever you actually run in production before
  treating this as done.

## If you're stuck on G1 (older JDK, other constraints)

Some tuning narrows the same failure mode without a GC swap, though none of it changes
the fundamental stop-the-world Full GC fallback — it only delays or shrinks it:

```
-XX:+UseG1GC
-XX:MaxGCPauseMillis=100          # target pause; G1 self-tunes region size/threading toward this
-XX:G1ReservePercent=15           # keep more headroom before falling back to Full GC
-XX:G1HeapRegionSize=8m           # larger regions = fewer regions to track at 16G+ heaps
-XX:+ParallelRefProcEnabled       # parallelize reference processing during pauses
-XX:InitiatingHeapOccupancyPercent=35  # start concurrent marking earlier, before Old Gen is nearly full
```

`InitiatingHeapOccupancyPercent` is the most relevant knob here: lowering it makes G1
start concurrent Old Gen marking sooner relative to occupancy, buying more runway before
promotion outpaces reclamation. It does not prevent the Full GC fallback under sustained
load like `gaslighter` produces — it just raises the connection count/duration required
to trigger it. Treat this as a stopgap, not a fix, if you can't move to ZGC.

## What doesn't help

- **Bigger heap alone.** Covered in `g1gc-heap-exhaustion.md` — with G1, more heap means
  a longer stop-the-world pause when Full GC finally triggers, not a prevented one. With
  ZGC this concern mostly evaporates (pause time is largely heap-size-independent), which
  is itself another point in ZGC's favor for this specific threat model.
- **Shenandoah as a stand-in for ZGC on this workload.** Similar low-pause design goals,
  but the tested result here is specifically ZGC on EPYC — don't assume Shenandoah
  performs identically against this tool without testing it yourself first.

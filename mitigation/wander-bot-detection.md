# Mitigating `--wander`

Target: `gaslighter --wander`, which exists specifically to defeat the most obvious
version of Mitigation 2 in [`g1gc-heap-exhaustion.md`](g1gc-heap-exhaustion.md) — "kick
anything that never moves." Read `gaslighter/README.rst`'s own framing: *"`--wander`
breaks the assumption that held connections are free to share spawn chunks"* and forces
"unique per-bot chunk loading instead of everyone idling at spawn." This doc is about
un-breaking that assumption.

## The mechanic, and where it's honest about its own weaknesses

Per `main.go` (`holdConnPlay`, `worker()` wander goroutine):

- On `--wander-interval` (default 2s), each bot's own RNG decides move-or-hold with a
  coin flip, and on a move, shifts X/Z by a uniform random offset up to `--wander-step`
  (default 0.5 blocks per axis).
- A **Client Status (respawn)** packet fires unconditionally every fixed 10 seconds,
  whether or not the bot is actually dead — the README calls this out itself as "a
  no-op if the bot isn't dead... at the cost of one wasted packet most of the time,"
  chosen because it's cheaper than parsing Combat Death/Set Health to detect a real
  death.
- Bots never simulate gravity or read chunk data, so Y drifts away from real terrain —
  again, the README flags this as "plausible fodder for anti-cheat plugins to kick on"
  and shrugs: a kicked worker just reconnects.

Three tells fall directly out of this design, and none of them require guessing —
they're structural to how the feature was built, not incidental bugs:

1. **Movement is on a fixed tick with a bounded random walk**, not continuous
   client-driven motion. Real clients send position updates roughly every server tick
   they're moving (20/s) with server-side movement smoothing; a bot firing exactly once
   per `wanderInterval` with a step drawn from a *uniform* distribution has a
   statistically flat step-size histogram no real player produces (real movement is
   bursty — sprinting, stopping, looking around — and its step-size distribution is
   nothing like uniform).
2. **A respawn packet every exactly 10.000 seconds, forever, from a bot that's never
   dead.** Real players don't send Client Status (respawn) unless they're actually dead,
   and even then it's a one-off, not a metronome.
3. **Y-axis drift with no gravity.** A bot that never processes a Set Health / Combat
   Death packet and never simulates falling will, over minutes, end up at a Y coordinate
   inconsistent with any block actually existing there — including negative Y in the
   void, or Y values that don't match the loaded chunk's terrain height at all.

## Mitigation: periodicity detection on movement packets

Track inter-arrival times of Player Position (and Rotation) packets per connection.
Real clients' inter-arrival times have real jitter (network latency variance, client
tick drift, actual gameplay pauses); `--wander`'s ticker-driven sends have near-zero
jitter around the configured interval. A rolling coefficient-of-variation
(`stddev / mean`) on the last N inter-arrival gaps that stays below a tight threshold
(e.g. < 0.05) for a sustained window is a strong bot signal — this is the same
statistical shape you'd use to detect any timer-driven automation, not just this tool
specifically, which is why it's a durable mitigation even if the interval default
changes.

```java
public class MovementPeriodicityDetector {
    private static final int WINDOW = 20;
    private static final double CV_THRESHOLD = 0.05;

    private final Deque<Long> timestamps = new ArrayDeque<>();

    // Call on every serverbound Player Position(/Rotation) packet.
    public boolean onMovementPacket() {
        long now = System.nanoTime();
        timestamps.addLast(now);
        if (timestamps.size() > WINDOW) timestamps.removeFirst();
        if (timestamps.size() < WINDOW) return false; // not enough data yet

        double[] gaps = new double[WINDOW - 1];
        Iterator<Long> it = timestamps.iterator();
        long prev = it.next();
        int i = 0;
        while (it.hasNext()) {
            long t = it.next();
            gaps[i++] = (t - prev) / 1e6; // ms
            prev = t;
        }
        double mean = Arrays.stream(gaps).average().orElse(0);
        double variance = Arrays.stream(gaps).map(g -> (g - mean) * (g - mean)).average().orElse(0);
        double cv = Math.sqrt(variance) / mean;
        return cv < CV_THRESHOLD; // suspiciously metronomic
    }
}
```

Don't kick on a single trip — flag and require several consecutive windows below
threshold (the metric is cheap enough to run continuously) before acting, since a
player standing dead-still for a while can produce low-jitter *hold* packets too. The
respawn-metronome check below has a much lower false-positive rate and should be
weighted higher.

## Mitigation: the unconditional respawn timer is close to a free signal

Track timestamps of Client Status (respawn, action `0x00`) packets per connection
against actual death state (did the server send a Combat Death / health-zero update in
the preceding window?). A respawn packet with no preceding death is already unusual;
*several* at a suspiciously regular ~10s cadence with no death ever observed is close to
a direct fingerprint of this exact tool's implementation choice (README: "unconditionally
… no-op if the bot isn't dead"). This is the cheapest, highest-confidence check in this
document — implement it first.

## Mitigation: Y-axis plausibility

On each Player Position update, compare the reported Y to the actual highest solid block
at that X/Z in the loaded chunk (`World#getHighestBlockYAt` or equivalent). A connection
whose Y sits persistently well below (or above, with no jump/flight permission) the
terrain height for more than a few ticks is either a broken client or a bot that — per
the README — "never simulate[s] gravity." Combine with the periodicity check rather than
acting alone; legitimate elytra flight, boats over water, and creative-mode noclip
produce real but unusual Y/terrain mismatches too.

## What doesn't help

- **Kicking on movement alone.** That's the exact prior mitigation `--wander` was built
  to defeat — any check that only asks "did this connection ever send a Player Position
  packet" now passes trivially. The fix has to look at the *shape* of the movement, not
  its presence.
- **A single false-positive-tolerant check in isolation.** Each signal above (timing
  periodicity, respawn metronome, Y-implausibility) has real players who can trip it
  individually (AFK autoclickers, actual respawns, elytra flight). Combine at least two
  before kicking; this is a scoring problem, not a single boolean.

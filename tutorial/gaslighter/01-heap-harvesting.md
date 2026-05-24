# 01: Heap Harvesting — Inducing G1GC Despair

This is the original. The classic. The "I read three JVM internals blog posts and now I'm dangerous" attack. We aren't just filling up the heap; we are tricking the **Generational Garbage-First Garbage Collector (G1GC)** into making a series of increasingly poor financial decisions with its own memory.

### The Theory: Exploiting the JVM's Trust Issues

The G1GC is, at its core, an optimist. It assumes that objects which survive a Minor GC must be important. It promotes them. It gives them permanent housing in the Old Generation. It does not ask questions.

We exploit this optimism mercilessly.

By holding thousands of half-open connections — each carrying a 255-character handshake string of pure nonsense — we fill the Eden Space with objects that the JVM *thinks* are load-bearing. They are not. They are garbage wearing a hard hat. The JVM promotes them anyway.

Once the Old Gen is fully colonized by our fake-important strings, the JVM has no choice but to trigger a **Full GC** — a stop-the-world event where the server's main thread freezes entirely while the garbage collector stares at the heap, increasingly confused about why it can't reclaim anything. It can't reclaim anything because we are still holding all the connections open.

The result:
- **Server TPS drops** — players start rubber-banding
- **Full GC stalls** — the server freezes for seconds or minutes at a time
- **OutOfMemoryError** — the JVM gives up and writes a `.hprof` file so large it will outlive the server it came from

### Lab Setup

```bash
# The classic: max bloat, max workers, hold forever
./gaslighter-bin play.homelab.local -w 10000 -s 255
```

### Progression of Events

| Time | Server's Internal Monologue |
|---|---|
| T+0s | "Oh, 10,000 new connections. Busy day." |
| T+30s | "Eden Space is filling up faster than usual..." |
| T+2m | "Minor GC ran. Objects survived. Must be important. Promoting to Old Gen." |
| T+5m | "Old Gen at 87%. Should I be worried? Probably fine." |
| T+8m | "OLD GEN AT 99%. FULL GC. EVERYTHING STOP." |
| T+8m+3s | "...I cannot reclaim anything. They are all still connected." |
| T+9m | `java.lang.OutOfMemoryError: Java heap space` |
| T+9m+1s | Server admin frantically types `restart` |

### Pro Tip: The Debug Run First

Before launching the full fleet, always do a single `--debug` run to confirm the server responds to the handshake:

```bash
./gaslighter-bin --debug play.homelab.local
```

This gives you a colored packet log of the entire Login → Config → Play sequence so you know exactly what you're dealing with before committing 10,000 workers to the cause.

# 01: Heap Harvesting - Inducing G1GC Despair

Targeting a Minecraft server's RAM is more of an art than a science. We aren't just filling up the heap; we are tricking the **Generational Garbage-First Garbage Collector (G1GC)** into making terrible life choices.

### The Theory
By holding thousands of half-open connections with oversized handshake strings, we create "survivor" objects. The JVM sees these objects surviving Minor GCs and assumes they are "important." It then promotes them to the **Old Generation**. 

Once the Old Gen is saturated with our garbage, the JVM enters a "Full GC" panic. This freezes the server's main thread, often for seconds or minutes at a time.

### Lab Setup
```bash
./gaslighter play.homelab.local -w 10000 -s 255
```

### What to Expect
- **The "Stutter"**: Server TPS will begin to fluctuate.
- **The "Deep Freeze"**: Players will experience massive lag spikes (the Full GC pauses).
- **The "Flatline"**: An `OutOfMemoryError` followed by a massive heap dump.

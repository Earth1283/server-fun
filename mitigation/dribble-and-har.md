# Mitigating the Dribble Strategy and Pre-Login Spam

Target: the two `gaslighter` fallback/alternate modes that don't need Play state —
the **dribble strategy** (online-mode-without-credentials fallback, or the natural
result of Mitigation 3 in [`g1gc-heap-exhaustion.md`](g1gc-heap-exhaustion.md)) and
**`--prelogin [--har]`** (fire-and-forget `AsyncPlayerPreLoginEvent` spam).

## Dribble strategy

### The mechanic

Per `gaslighter/README.rst`: after a kick during Login (typically "Failed to verify
username!" on an online-mode server without valid credentials), the worker sends a
3-byte VarInt header (`0xFF 0xFF 0x03` → declares a 65,535-byte incoming frame) and then
drips one filler byte per `--dribble-interval` tick (default 5s) into that still-open
frame. Netty's `ReadTimeoutHandler` only cares that *some* byte arrived recently — it has
no concept of "this frame has been open for 91 hours and is 0.001% complete." The
connection, and its allocated 65KB read buffer, sit in memory indefinitely.

This is a **frame-completion-time attack**, not an idle-timeout attack, and that
distinction is exactly why a naive idle timeout doesn't catch it — the connection is
never idle. Bytes keep arriving.

### Mitigation: decode-deadline, independent of activity

The fix is to track frame lifetime from the moment the length-prefix VarInt is read,
separately from Netty's idle/read timeout, and enforce a hard ceiling regardless of how
much trickle traffic keeps resetting the idle timer:

```java
// Conceptual - wire into your pipeline ahead of the length-field decoder.
public class FrameDeadlineHandler extends ChannelInboundHandlerAdapter {
    private static final long MAX_FRAME_MILLIS = 10_000; // generous for real 65KB payloads
    private long frameStartedAt = -1;

    @Override
    public void channelRead(ChannelHandlerContext ctx, Object msg) throws Exception {
        if (frameStartedAt < 0) {
            frameStartedAt = System.currentTimeMillis();
        } else if (System.currentTimeMillis() - frameStartedAt > MAX_FRAME_MILLIS) {
            ctx.close(); // frame has been "in progress" too long - not a real payload
            return;
        }
        super.channelRead(ctx, msg);
    }

    public void onFrameComplete() {
        frameStartedAt = -1; // reset once a full logical packet is actually decoded
    }
}
```

Any real 65,535-byte packet a vanilla client would ever legitimately send (a large
Plugin Message, chunk data during Configuration, etc.) completes in well under a second
on any real network. A frame that's still incomplete after 10 seconds is not a slow
client — nothing at that scale should take 10s to arrive over TCP on a residential or
even satellite connection — it's the dribble strategy, and it can be closed with high
confidence and near-zero false positive rate.

### Mitigation: rate-of-progress instead of fixed deadline

If you're worried about genuinely bad connections (someone actually on satellite
internet), a softer version tracks *bytes per second within the open frame* rather than
a hard wall-clock deadline, and kicks connections whose average intra-frame throughput
falls below a floor (e.g. 100 B/s) sustained for more than a minute. One byte every 5
seconds is 0.2 B/s — two and a half orders of magnitude below any plausible real
connection, satellite included.

### Mitigation: kernel-layer detection

Because the dribble strategy's signature (tiny, evenly-spaced writes on an otherwise
idle socket) is visible without touching decrypted application data, it's also
catchable at the kernel layer — see
[`network/tc_dribble_detect.c`](network/tc_dribble_detect.c), which watches per-flow
packet-size and inter-arrival statistics via a kprobe on `tcp_recvmsg` and auto-populates
a blocklist map once a flow crosses "too many too-small packets, too evenly spaced,
for too long." This catches the pattern before it ever reaches the JVM, and doesn't care
whether the connection is in Login, Configuration, or Play state.

## Pre-login spam (`--prelogin`, `--har`)

### The mechanic

Every connection triggers `AsyncPlayerPreLoginEvent` on the way into Login state — this
fires *before* the server has committed any real resources to the connection, which is
exactly why it's attractive to spam. Plugins like LuckPerms (permission lookups),
Geyser (Bedrock bridging), or any anti-bot/anti-VPN filter typically do a database query
or an outbound HTTP call inside this event handler. `--har` (hit-and-run) doesn't even
wait for the server's response — it fires the packets and hangs up, maximizing the
attacker's connections-per-second at the cost of not knowing whether the event actually
fired. The target isn't the JVM heap this time, it's **your plugins' backend**: a
connection pool, a rate-limited third-party API, or a database that has a much lower
failure threshold than "heap exhaustion."

### Mitigation: rate-limit the event, not the connection

The connection itself is cheap (TCP handshake, three small packets) — trying to stop it
at the network layer means fighting SYN floods, which is the wrong layer for a
few-hundred-byte-per-connection attack. Rate-limit inside the event handler instead,
per source IP, with a token bucket that's stricter than anything a real player could
trigger (nobody reconnects 50 times a second):

```java
public class PreLoginRateLimiter implements Listener {
    // IP -> token bucket. Real players: at most a handful of attempts/minute.
    private final Map<InetAddress, TokenBucket> buckets = new ConcurrentHashMap<>();

    @EventHandler(priority = EventPriority.LOWEST) // run before LuckPerms/Geyser/etc.
    public void onPreLogin(AsyncPlayerPreLoginEvent event) {
        InetAddress ip = event.getAddress();
        TokenBucket bucket = buckets.computeIfAbsent(ip, k -> new TokenBucket(5, Duration.ofSeconds(10)));
        if (!bucket.tryConsume()) {
            event.setLoginResult(AsyncPlayerPreLoginEvent.Result.KICK_OTHER);
            event.setKickMessage("Too many login attempts. Slow down.");
        }
    }
}
```

Priority `LOWEST` matters: it needs to run and potentially cancel *before* the expensive
plugins' own listeners fire, or you've just added overhead on top of the attack instead
of instead of it.

### Mitigation: detect the `--har` signature specifically

`--har` connections have a distinctive shape even at the TCP level: a connection that
sends Handshake + Login Start and then closes *immediately*, without ever waiting for
a server response, is not something a real Minecraft client does — even the fastest
real client waits at least one round-trip before deciding what to do next. Log the
time-to-close after Login Start and flag/ban IPs whose connections consistently close
in under ~50ms (real network RTT to any actual game client is essentially never that
fast end-to-end). This is a strong enough signal to combine with Mitigation above:
IPs that trip the fast-close heuristic get a much stricter (or zero) token bucket.

### Mitigation: async isn't a safety net if the pool isn't bounded

`AsyncPlayerPreLoginEvent` runs off the main thread specifically so a slow lookup
doesn't freeze the game loop — but if the plugin backing it uses an unbounded thread
pool or connection pool, spam just shifts the exhaustion from "JVM heap" to "database
connections" or "HTTP client threads," which can be a *faster* failure than the G1GC
attack because there's no GC to delay the reckoning. Audit any plugin hooking this event
for a bounded executor and a queue depth limit — if the queue fills, reject new
lookups fast (kick) rather than queueing indefinitely, which just becomes another form
of unbounded resource growth with a different name.

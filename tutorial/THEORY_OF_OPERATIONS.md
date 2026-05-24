# Theory of Operations: The Art of Empirical Validation 🧪

Welcome, Senior Chaos Engineer. If you are reading this, you have graduated from "script kiddie" to "person who reads documentation before breaking things." That is already more than most. The `server-fun` suite exploits the fundamental architectural assumptions of the Minecraft protocol and the Java Virtual Machine — specifically, the assumption that clients are *honest*.

They are not.

---

## ⛽ Gaslighter: The Resource Asphyxiator

### 1. The G1GC "Eden Space" Overcrowding (a.k.a. The Roommate From Hell)

**The Target**: `java.lang.String` allocations on the JVM heap.

**The Method**: Every Minecraft Handshake packet contains a `serverAddress` field. A normal client sends something reasonable like `play.server.com`. We send 255 characters of high-entropy gibberish, because the protocol allows it and the server has to store it.

**The Science**:
1. The server's Netty I/O thread receives the packet and allocates a `String` object on the heap. It expects this object to be garbage-collected in milliseconds once the client identifies itself and moves on.
2. Gaslighter does not move on. Gaslighter **never closes the connection**. Gaslighter has found a 5-star hotel in the Eden Space and intends to stay indefinitely.
3. The JVM's **Eden Space** fills up with hundreds of thousands of these orphaned strings.
4. A **Minor GC** runs. The JVM looks at each string, sees that the connection is still alive, and concludes that the string must be "important." It is not important. It is 255 characters of noise. But the JVM doesn't know that.
5. The strings get **promoted to the Old Generation** — the JVM's equivalent of signing a lease.
6. With thousands of workers doing this simultaneously, the Old Gen fills up entirely. The JVM panics, triggers a **Full GC**, and freezes the entire server thread while it tries to figure out what went wrong. Nothing went wrong. We planned this.
7. The freeze lasts seconds. Sometimes minutes. Sometimes the server just gives up entirely and produces an `OutOfMemoryError` and a `.hprof` heap dump the size of a small country.

**Indicators of Success**: Server TPS drops. Players rubber-band. Admins start posting in their Discord "anyone else lagging?" You smile knowingly.

---

### 2. The "Glacial Login" — Sequence Stalling (a.k.a. The DMV Strategy)

**The Target**: The server's login thread pool.

**The Method**: Respond to `Encryption Request` and `Set Compression` packets at a speed best described as *geological*. Specifically, 27.5 seconds per step.

**The Science**: Minecraft's login timeout is 30 seconds. If your responses arrive in under 30 seconds — however barely — the server cannot kick you. It must hold a login thread open, waiting patiently, like a government employee who has already mentally clocked out but legally cannot leave until 5pm.

With 5,000 workers each occupying a thread for 27+ seconds at a time, the server's thread pool transforms into a bureaucratic nightmare. Legitimate players attempting to join get "Timed Out" — not because there's no bandwidth, but because every available login slot is occupied by us, doing absolutely nothing, extremely slowly, on purpose.

---

### 3. Offline Detection — Knowing When to Stop Yelling at a Brick Wall

**The Problem**: A server that is already offline cannot be made more offline. Sending 10,000 connection attempts per second to a closed port accomplishes nothing except making your machine look like it is having a stroke.

**The Solution**: Before spawning a single goroutine, Gaslighter performs a quiet pre-flight TCP check. If the server says "connection refused," Gaslighter says "noted" and exits cleanly instead of launching thousands of workers into the void.

If the server *goes down mid-run* (as healthy servers sometimes do when you're using this tool correctly), workers detect the `ECONNREFUSED`, print a single warning, and slow down to a 15-second retry interval. The moment the server comes back up — perhaps after an admin frantically restarts it — workers detect the recovered connection and immediately resume at full speed. Your audit is self-healing. Isn't that thoughtful.

---

## 🕵️ Wiretap: The Intelligence Suite

### 1. Dual-Phase Reconnaissance — Check the Lock Before You Kick the Door

Wiretap does two things in sequence:

**Phase 1 — Standard SLP**: Sends a Server List Ping and reads the response. This is exactly what the Minecraft launcher does when you add a server to your list. The server sees nothing unusual. You receive the MOTD, player count, version string, and sometimes a favicon that took the admin four hours to make. Now you know the version and whether the server is bragging about its hardware in the MOTD.

**Phase 2 — Deep Protocol Probe**: Initiates a fake Login handshake. The server receives what looks like a client attempting to join. What actually happens:
- If the server sends an `Encryption Request`, it's **Online Mode** — it wants to verify your account with Mojang. Difficult to fake without valid credentials.
- If the server sends a `Login Success` directly, it's **Offline Mode** — it accepted your made-up username without any verification. This is the **holy grail**. Every one of Gaslighter's 10,000 workers can now reach Play state without needing a single valid account.

Wiretap also captures the **RSA key size** (tells you how much CPU overhead the encryption handshake costs) and the **compression threshold** (relevant for understanding network overhead at scale).

---

## 🔌 Pluginscanner: The Tattletale

### 1. The Tab-Complete Leak — Getting the Server to Incriminate Itself

**The Target**: The Bukkit command registry.

**The Method**: Connect to the server as a player, then send a **Command Suggestions Request** (protocol 767, serverbound `0x0B` — *not* `0x0F`, which is Close Container, which gets you immediately kicked; ask us how we know) with an empty text field. This is the packet the Minecraft client sends when you press Tab in the chat box.

**The Science**: The Bukkit/Paper plugin system requires every plugin to register its commands under a `pluginname:commandname` namespace. This entire registry is handed to any connected client that requests tab-complete, because from the server's perspective, that's just how Tab works. The server has no idea it's revealing its full software stack to someone who connected under a randomly generated username with zero intention of actually playing.

The response tells you:
- Every plugin installed and its exact command set
- Which **auth plugin** is running — AuthMe, CMI, NexAuth, and their kin all have distinct command signatures and known weaknesses for `--prelogin` and `--har` mode
- Whether **economy/database plugins** (Vault, EssentialsX) are present — these hold open DB connection pools that `--har` can exhaust
- Whether **proxy plugins** (BungeeCord, Velocity, Liaison) are installed — which means you're talking to a frontend and the real backend might be sitting unprotected on a different port entirely

**Why the server can't easily stop this**: It could disable tab-complete entirely, but then no one's Tab key works in chat. It's a feature, not a bug, until it isn't.

---

## 🗺 The Chaos Workflow: "The Three-Course Meal"

A professional "unsolicited infrastructure audit" has structure. Improvisation is for amateurs.

1. **Appetizer** (`wiretap`): Survey the target. Online or offline mode? What version? What's hiding behind the SRV record? Eat your vegetables before dessert.

2. **Soup** (`pluginscanner`): Map the plugin stack. This takes about 10 seconds and appears in the server logs as a player joining and immediately disconnecting — completely normal, happens all the time, nothing to see here. Now you know exactly what you're up against.

3. **Main Course** (`gaslighter`): Apply the appropriate strategy. Offline mode with no auth plugin? Heap harvest, maximum workers, make yourself at home. AuthMe detected? `--prelogin --har` to exhaust its database connection pool before you even try the heap attack. Both? Do both. It's a tasting menu.

4. **Dessert**: When the server goes down, retire to the `tutorial/gaslighter/03-stealth-and-deniability.md` social engineering scripts and pick your favorite excuse. You've earned it.

*Happy "validating"!* 🧨

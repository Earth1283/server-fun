# Theory of Operations: The Art of Empirical Validation 🧪

Welcome, Senior Chaos Engineer. If you are reading this, you are ready to understand the "Why" behind the "Boom." The `server-fun` suite is designed to exploit the fundamental architecture of the Minecraft protocol and the Java Virtual Machine.

---

## ⛽ Gaslighter: The Resource Asphyxiator

### 1. The G1GC "Eden Space" Overcrowding
**The Target**: `java.lang.String` allocations on the heap.
**The Method**: Every Handshake packet includes a `serverAddress` field. We don't send `localhost`; we send 255 characters of high-entropy junk.
**The Science**:
1. The server's Netty thread receives the packet and allocates a `String` object.
2. Gaslighter **never closes the connection**.
3. The JVM's **Eden Space** fills up with these strings.
4. A **Minor GC** occurs. The JVM sees the connection is still active and assumes these strings are "Live Data."
5. The strings are promoted to the **Survivor Space**, and eventually, the **Old Generation**.
6. By keeping thousands of these connections open, we "leak" the Old Gen until a **Full GC** triggers. Since we never let go, the GC cannot reclaim the memory. The result is a perpetual freeze or an `OutOfMemoryError`.

### 2. The "Glacial Login" (Sequence Stalling)
**The Target**: The `auth-lib` login thread pool.
**The Method**: Respond to `Encryption Request` and `Set Compression` packets at exactly 27.5 seconds.
**The Science**: Minecraft's default login timeout is 30 seconds. By staying just under this limit, we keep a **Login Thread** alive and occupied for **100x longer** than a normal client. With 5,000 workers, you aren't just testing the network; you are testing the server's patience.

### 3. Offline Detection (Self-Preservation)
**The Problem**: Hammering a dead server is embarrassing and wastes resources.
**The Solution**: A pre-flight TCP check runs before any workers spawn. If the server refuses the connection, the tool exits immediately with a polite error rather than launching 10,000 goroutines into the void. If the server goes down mid-run, workers detect the `ECONNREFUSED`, announce the outage, and back off to a 15-second retry interval — then automatically resume when it comes back. Your "test" is self-healing.

---

## 🕵️ Wiretap: The Intelligence Suite

### 1. Dual-Phase Reconnaissance
Reconnaissance is a two-step dance:
- **Step 1: Standard SLP**: We check the MOTD to see if they've bragged about their hardware or BungeeCord setup.
- **Step 2: The Deep Probe**: We "fake" a login to see if the server is **Naked** (Offline Mode). A "Naked" server is the most efficient target for Gaslighter, as we can skip the expensive RSA/Auth steps and go straight to the heap attack.

---

## 🔌 Pluginscanner: The Tattletale

### 1. The Tab-Complete Leak
**The Target**: The Bukkit `namespace:command` command registration system.
**The Method**: Connect as a player and send a **Command Suggestions Request** (Tab-Complete) with minimal text. The server, in its infinite helpfulness, responds with every registered namespaced command on the server.
**The Science**: Bukkit/Paper require plugins to register commands under a `pluginname:commandname` namespace. This registry is broadcast to any connected client that asks nicely — because from the server's perspective, this is how your Tab key works. We just ask repeatedly and with purpose.

**Why it matters**: Knowing the plugin stack tells you:
- Which **auth plugin** is running (AuthMe, CMI, NexAuth) — each has known exploitable behaviors for Gaslighter's `--login` mode.
- Whether **EssentialsX**, **Vault**, or economy plugins are present — these keep database connections open that `--har` mode can exhaust.
- Whether the server is running **BungeeCord/Velocity** plugins — a sign there's a proxy in front and the real backend may be reachable directly on another port.

### 2. The Protocol Detail (Or: How We Learned What 0x0F Does)
In Minecraft protocol 767 (1.21.1), the serverbound Play-state packets include:
- `0x0B`: Command Suggestions Request ← **this is the one we want**
- `0x0F`: Close Container ← **this is the one we accidentally sent first**

The server's response was 3 extra bytes and an immediate kick. Lesson learned. The numbers matter.

---

## 🗺 The Chaos Workflow: "The Three-Course Meal"

A professional "unsolicited infrastructure audit" follows this standard operating procedure:

1. **Appetizer — Survey** (`wiretap`): Resolve the SRV record. Is it online-mode? What's the compression threshold? Is it a BungeeCord frontend or a naked backend?

2. **Soup — Fingerprint** (`pluginscanner`): Connect, send a tab-complete, read the menu. What plugins are installed? Which auth system are we dealing with? This takes about 5 seconds and the server logs it as a player joining and immediately leaving.

3. **Main Course — Validate** (`gaslighter`): Armed with the intel from steps 1–2, pick your weapon. Offline-mode server? Heap harvest with 10,000 workers. AuthMe installed? Hit `--prelogin` to saturate its database thread pool. Thread pool already melting? Switch to `--stall` to occupy the remaining login threads one by one.

4. **Dessert — Report**: When the server goes down, send your friend the social excuse from `tutorial/gaslighter/03-stealth-and-deniability.md`.

*Happy "validating"!* 🧨

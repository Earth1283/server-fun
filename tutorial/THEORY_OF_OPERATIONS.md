# Theory of Operations: The Art of Empirical Validation 🧪

Welcome, Senior Chaos Engineer. If you are reading this, you are ready to understand the "Why" behind the "Boom." The `server-fun` suite is designed to exploit the fundamental architecture of the Minecraft protocol and the Java Virtual Machine.

## ⛽ Gaslighter: The Resource Asphyxiator

### 1. The G1GC "Eden Space" Overcrowding
**The Target**: `java.lang.String` allocations on the heap.
**The Method**: Every Handshake packet includes a `serverAddress` field. We don't send `localhost`; we send 255 characters of high-entropy junk. 
**The Science**: 
1.  The server's Netty thread receives the packet and allocates a `String` object. 
2.  Gaslighter **never closes the connection**. 
3.  The JVM's **Eden Space** fills up with these strings.
4.  A **Minor GC** occurs. The JVM sees the connection is still active and assumes these strings are "Live Data."
5.  The strings are promoted to the **Survivor Space**, and eventually, the **Old Generation**.
6.  By keeping thousands of these connections open, we "leak" the Old Gen until a **Full GC** triggers. Since we never let go, the GC cannot reclaim the memory. The result is a perpetual freeze or an `OutOfMemoryError`.

### 2. The "Glacial Login" (Sequence Stalling)
**The Target**: The `auth-lib` login thread pool.
**The Method**: Respond to `Encryption Request` and `Set Compression` packets at exactly 27.5 seconds.
**The Science**: Minecraft's default login timeout is 30 seconds. By staying just under this limit, we keep a **Login Thread** alive and occupied for **100x longer** than a normal client. With 5,000 workers, you aren't just testing the network; you are testing the server's patience.

---

## 🕵️ Wiretap: The Intelligence Suite

### 1. Dual-Phase Reconnaissance
Reconnaissance is a two-step dance:
-   **Step 1: Standard SLP**: We check the MOTD to see if they've bragged about their hardware or BungeeCord setup.
-   **Step 2: The Deep Probe**: We "fake" a login to see if the server is **Naked** (Offline Mode). A "Naked" server is the most efficient target for Gaslighter, as we can skip the expensive RSA/Auth steps and go straight to the heap attack.

---

## 🛠 The Chaos Workflow: "The One-Two Punch"

A professional "unsolicited infrastructure audit" follows this standard operating procedure:

1.  **Survey**: Use `wiretap` to resolve the SRV record and check for Offline Mode.
2.  **Audit**: Run `wiretap` through your SOCKS5 proxy list to see which ones the target's firewall has already blacklisted.
3.  **Validate**: Launch `gaslighter` using the "Glacial Login" strategy to saturate the thread pool, then transition to a "Heap Harvest" to finish the job.
4.  **Report**: When the server goes down, send your friend the social excuse from `tutorial/gaslighter/03-stealth-and-deniability.md`.

*Happy "validating"!* 🧨

# server-fun 🧨

A collection of "stress testing" (read: server-breaking) utilities for the modern, Java-weary netrunner. If you've ever felt that a Minecraft server was enjoying its RAM a bit too much, you're in the right place.

## The Crown Jewel: **Gaslighter** (`gaslighter`)

The digital equivalent of a Slowloris attack, but with a specific grudge against the **G1 Garbage Collector**. While other tools try to drown the network, **Gaslighter** targets the server's soul: its heap.

### 🛠 How it "optimizes" your target:
- **Eden Space Overcrowding**: Floods the server with thousands of half-open connections.
- **Premature Promotion**: Forces the JVM to promote junk objects into the Old Generation faster than a mid-life crisis.
- **Full GC Therapy**: Induces Garbage Collection pauses so long that the server admins have time to take up gardening while the JVM freezes in agony.
- **OOM Dreams**: Gently nudges the server toward an `OutOfMemoryError` and a massive `.hprof` heap dump that will take three days to download.

## ✨ Features for the Discerning Chaos-Enjoyer:
- **SRV Record Magic**: Just point it at `play.target.com`. We'll find the port so you don't have to.
- **SOCKS5 Stealth**: Tunnel your "testing" through a list of proxies. Supports Random and Round-robin strategies because variety is the spice of a stress test.
- **Pre-Login Spam**: Don't want to wait for a full connection? Spam `AsyncPlayerPreLoginEvent` to keep the server's auth threads and database plugins perpetually busy.
- **Hit-and-Run (--har)**: The ultimate in fire-and-forget technology. Send the packets and hang up before the server even has a chance to say "hello."
- **Bespoke Encryption**: Hand-rolled AES/CFB8 implementation for online-mode servers. We do our own crypto because the standard library wasn't "Minecrafty" enough.
- **The Dribble Strategy**: If we can't get into the Play state, we'll slowly drip filler bytes into an open frame like a leaky faucet, keeping the connection alive for up to 91 hours of pure heap-resident fun.
- **Zero-Coordination Workers**: Thousands of goroutines working in perfect, lock-free disharmony using `sync/atomic` telemetry.
- **gaslighterc.toml**: Save your favorite settings in a config file. Because even chaos deserves a little structure.

## 🛠 Build & Install

Requirements: Go 1.25+. Note that all compiled binaries are ignored by git to keep the workspace clean.

### 1. Gaslighter (gaslighter)
```bash
cd gaslighter
go build -o ../gaslighter .
```

### 2. Wiretap
```bash
cd wiretap
go build -o ../wiretap-bin .
```

## 🧠 Theoretical Foundations (The "Science" of Chaos)

For the Senior Engineer who needs to justify these tools to a project manager, here is the technical breakdown of our "optimization" strategies.

### ⛽ Gaslighter: The JVM Whisperer
Gaslighter is not a "stress tester." It is a **Resource Asphyxiator**. It targets the two most precious commodities in a Java environment: **Memory Residency** and **Thread Availability**.

*   **The G1GC Heap Harvest**: Modern Minecraft servers love the G1 Garbage Collector. We exploit this love. By holding thousands of connections with maximized Handshake strings (255 characters of pure entropy), we overcrowd the **Eden Space**. The JVM, seeing these objects survive Minor GCs, assumes they are "critical infrastructure" and promotes them to the **Old Generation**. We aren't just using RAM; we are "leasing" the Old Gen indefinitely until the JVM triggers a **Full GC Stall**—a freeze so profound it gives the server admins time to reflect on their life choices.
*   **Glacial Logins (--stall)**: Why flood a server when you can simply occupy it? By responding to authentication challenges at **glacial speeds** (28 seconds per step), a single worker can hold a **Login Thread** hostage for nearly the full 30-second timeout. With 5,000 workers, the server's thread pool becomes a bureaucratic nightmare where no one can join, and everyone is "waiting for a response."
*   **The HAR Strategy (--har)**: Hit-and-Run. We target the `AsyncPlayerPreLoginEvent` to force the server's backend plugins (database-backed auth, geo-IP filters) to exhaust their **connection pools**. It’s the digital equivalent of ringing every doorbell in a skyscraper and running away before the security guards can check the cameras.

### 🕵️ Wiretap: The Intelligence Officer
Wiretap is the scalpel used to find the crack in the armor.

*   **SLP Surveillance**: A non-intrusive scan that extracts the MOTD and player counts. It’s like checking a server’s pulse without them knowing you’re in the room.
*   **Deep Protocol Probe**: We initiate a "Handshake State 2" (Login) to see how the server handles its laundry. We detect **Online/Offline Mode** (identifying "naked" servers), capture **RSA Key Sizes** (measuring hardware "bravery"), and map **Compression Thresholds**.
*   **Stealth Infrastructure**: Built-in **SRV Resolution** and **SOCKS5 Proxy Rotation** ensure that your reconnaissance is as invisible as a ghost in a machine.

## 🚀 Getting Started (The 2-Minute Warning)

1. **OS Tuning**: Optimizing your kernel for high-frequency spamming is a must.
   ```bash
   sudo ./setup.sh
   ```
2. **Ignite**:
   ```bash
   # Use the SRV record, go debug mode, and wait for the "connection refused" of victory.
   ./gaslighter --debug play.server.com
   ```

## 📜 Legalish Stuff
This repository is for **authorized infrastructure testing only**. Using this on servers you don't own is a great way to get your IP blocklisted and your reputation ruined. We are here to "empirically validate JVM limits," not to be a nuisance. Mostly.

*Happy leaking!* 🧊

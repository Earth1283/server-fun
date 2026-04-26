# server-fun 🧨

A collection of "stress testing" (read: server-breaking) utilities for the modern, Java-weary netrunner. If you've ever felt that a Minecraft server was enjoying its RAM a bit too much, you're in the right place.

## The Crown Jewel: **Gaslighter** (`mc-stress`)

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

### 1. Gaslighter (mc-stress)
```bash
cd mc-stress
go build -o ../gaslighter .
```

### 2. MC-Probe
```bash
cd mc-probe
go build -o ../mc-probe-bin .
```

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

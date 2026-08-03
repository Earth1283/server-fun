# server-fun 🧨

A collection of "stress testing" (read: server-breaking) utilities for the modern, Java-weary netrunner. If you've ever felt that a Minecraft server was enjoying its RAM a bit too much, you're in the right place.

*Only gaslighter can do this.*

## The Crown Jewel: **Gaslighter** (`gaslighter`)

The digital equivalent of a Slowloris attack, but with a specific grudge against the **G1 Garbage Collector**. While other tools try to drown the network, **Gaslighter** targets the server's soul: its heap.

### 🛠 How it "optimizes" your target:
- **Eden Space Overcrowding**: Floods the server with thousands of half-open connections.
- **Premature Promotion**: Forces the JVM to promote junk objects into the Old Generation faster than a mid-life crisis.
- **Full GC Therapy**: Induces Garbage Collection pauses so long that the server admins have time to take up gardening while the JVM freezes in agony.
- **OOM Dreams**: Gently nudges the server toward an `OutOfMemoryError` and a massive `.hprof` heap dump that will take three days to download. It just works — against you.
- **Offline Detection**: Pre-flight check aborts immediately if the server is already down. Workers detect mid-run outages and back off gracefully instead of hammering a corpse — then resume automatically when the server comes back up.

## ✨ Features for the Discerning Chaos-Enjoyer:
- **SRV Record Magic**: Just point it at `play.target.com`. We'll find the port so you don't have to.
- **SOCKS5 Stealth**: Tunnel your "testing" through a list of proxies. Supports Random and Round-robin strategies because variety is the spice of a stress test.
- **Pre-Login Spam**: Don't want to wait for a full connection? Spam `AsyncPlayerPreLoginEvent` to keep the server's auth threads and database plugins perpetually busy.
- **Hit-and-Run (--har)**: The ultimate in fire-and-forget technology. Send the packets and hang up before the server even has a chance to say "hello."
- **Bespoke Encryption**: Hand-rolled AES/CFB8 implementation for online-mode servers. We do our own crypto because the standard library wasn't "Minecrafty" enough.
- **The Dribble Strategy**: If we can't get into the Play state, we'll slowly drip filler bytes into an open frame like a leaky faucet, keeping the connection alive for up to 91 hours of pure heap-resident fun.
- **Zero-Coordination Workers**: Thousands of goroutines working in perfect, lock-free disharmony using `sync/atomic` telemetry.
- **Rolling Debug Display**: In `--debug` mode, Play-state packet spam is contained to a fixed 5-line terminal region so you can actually read what's happening.
- **gaslighterc.toml**: Save your favorite settings in a config file. Because even chaos deserves a little structure.

## 🕵️ **Wiretap** (`wiretap`)

The intelligence officer. Performs a two-phase reconnaissance on a target before you commit.

- **SLP Surveillance**: Extracts MOTD, player counts, and version without raising any alarms.
- **Deep Protocol Probe**: Initiates a fake login to detect **Online/Offline Mode** (a "naked" offline-mode server skips the expensive RSA dance entirely), measures **RSA Key Size**, and maps **Compression Thresholds**.
- **Stealth Infrastructure**: SRV resolution and SOCKS5 proxy rotation built in.

## 🔌 **Pluginscanner** (`pluginscanner`) — *New*

The interrogator. Logs into the server as a player, then abuses the tab-complete system to enumerate every installed plugin and its registered commands — without touching a single `/plugin list` command.

### How it works:
The Bukkit command namespace scheme (`pluginname:commandname`) leaks the entire software stack through the Command Suggestions protocol. We ask politely. The server tells us everything.

### Features:
- **Full verbose connection log**: Every packet in the Handshake → Login → Config → Play sequence is printed with timestamps, packet IDs, and byte counts — so you can see exactly what the server sends back.
- **Interactive REPL**: After connecting, you get a shell. Type `scan` to enumerate all plugins, `probe <ns>` to drill into a specific one, or type `essentials:` directly as shorthand.
- **Silent background**: Once you're in the REPL, keep-alives and other Play-state noise are handled silently in the background. The console stays clean for your actual work.
- **SOCKS5 proxy support**: Because you're not doing this from your home IP.

```
Commands:
  scan              enumerate all plugins via tab-complete
  probe <ns>        list commands for a plugin namespace
  <ns>:             shorthand — type "essentials:" directly
  help              show this menu
  exit / quit       disconnect
```

## 📈 **Pulse** (`pulse`) — *New*

The cardiologist. While the other tools *cause* the heart attack, **Pulse** stands at the bedside watching the monitor. It hammers the Server List Ping endpoint on a configurable heartbeat and renders the server's vitals as a live, beautiful TUI — so when you fire Gaslighter in the next terminal, you can *watch* the latency climb and the player count flatline in real time.

### What it watches (100% non-intrusive, SLP-only — it never joins):
- **Ping latency** via the canonical Ping/Pong packet, graphed as a scrolling time-series.
- **Player count** over time — watch them rage-quit live.
- **MOTD / version** drift — catch the admin frantically swapping configs mid-incident.
- **Up/Down transitions** — the exact second the JVM gives up the ghost.

### The dashboard:
- **Time-series line charts** (braille-rendered) for latency and players.
- **Sparklines + gauges** header for an at-a-glance heartbeat.
- **Latency histogram** with `min / avg / p50 / p95 / p99 / max` — turn "it felt laggy" into a percentile.
- **Event log** — timestamped, colour-coded: went OFFLINE, recovered, latency spike, version changed, player exodus.
- **Live controls**: `+`/`-` to retune the poll interval *without restarting*, `space` to pause, `r` to poll now, `c` to clear events, `q` to quit.
- **SQLite persistence** (`--db runs.db`): every sample is logged so you can replay the autopsy later — or feed it to the manager dashboard.
- **SRV + SOCKS5**: same stealth infrastructure as the rest of the suite.

```bash
./pulse-bin play.server.com                  # 2s heartbeat, in-memory only
./pulse-bin --interval 1s --db run.db play.server.com   # high-res + persist the evidence
```

> Pro tip: run `pulse` in one pane and `gaslighter` in another. The histogram's p99 is the number you put in the incident report.

## 🛠 Build & Install

Requirements: Go 1.25+. Note that all compiled binaries are ignored by git to keep the workspace clean.

### 1. Gaslighter
```bash
cd gaslighter
go build -o ../gaslighter-bin .
```

### 2. Wiretap
```bash
cd wiretap
go build -o ../wiretap-bin .
```

### 3. Pluginscanner
```bash
cd pluginscanner
go build -o ../pluginscanner-bin .
```

### 4. Pulse
```bash
cd pulse
go build -o ../pulse-bin .
```

## 🧠 Theoretical Foundations (The "Science" of Chaos)

For the Senior Engineer who needs to justify these tools to a project manager, here is the technical breakdown of our "optimization" strategies.

### ⛽ Gaslighter: The JVM Whisperer
Gaslighter is not a "stress tester." It is a **Resource Asphyxiator**. It targets the two most precious commodities in a Java environment: **Memory Residency** and **Thread Availability**.

*   **The G1GC Heap Harvest**: Modern Minecraft servers love the G1 Garbage Collector. We exploit this love. By holding thousands of connections with maximized Handshake strings (255 characters of pure entropy), we overcrowd the **Eden Space**. The JVM, seeing these objects survive Minor GCs, assumes they are "critical infrastructure" and promotes them to the **Old Generation**. We aren't just using RAM; we are "leasing" the Old Gen indefinitely until the JVM triggers a **Full GC Stall**—a freeze so profound it gives the server admins time to reflect on their life choices.
*   **Glacial Logins (--stall)**: Why flood a server when you can simply occupy it? By responding to authentication challenges at **glacial speeds** (28 seconds per step), a single worker can hold a **Login Thread** hostage for nearly the full 30-second timeout. With 5,000 workers, the server's thread pool becomes a bureaucratic nightmare where no one can join, and everyone is "waiting for a response."
*   **The HAR Strategy (--har)**: Hit-and-Run. We target the `AsyncPlayerPreLoginEvent` to force the server's backend plugins (database-backed auth, geo-IP filters) to exhaust their **connection pools**. It's the digital equivalent of ringing every doorbell in a skyscraper and running away before the security guards can check the cameras.

### 🕵️ Wiretap: The Intelligence Officer
Wiretap is the scalpel used to find the crack in the armor.

*   **SLP Surveillance**: A non-intrusive scan that extracts the MOTD and player counts. It's like checking a server's pulse without them knowing you're in the room.
*   **Deep Protocol Probe**: We initiate a "Handshake State 2" (Login) to see how the server handles its laundry. We detect **Online/Offline Mode** (identifying "naked" servers), capture **RSA Key Sizes** (measuring hardware "bravery"), and map **Compression Thresholds**.
*   **Stealth Infrastructure**: Built-in **SRV Resolution** and **SOCKS5 Proxy Rotation** ensure that your reconnaissance is as invisible as a ghost in a machine.

### 🔌 Pluginscanner: The Tattletale
Pluginscanner exploits the **Command Suggestions protocol** (Tab-Complete in fancy clothes). In Minecraft, every plugin registers its commands under a `namespace:command` scheme. The server helpfully broadcasts this entire registry to anyone who asks — including someone who just connected under a random fake username with no intention of actually playing.

*   **Protocol 767 specifics**: Serverbound `0x0B` (Command Suggestions Request), Clientbound `0x0E` (Command Suggestions Response). Not `0x0F` — that's Close Container, which gets you kicked immediately. Ask us how we know.
*   **The Interrogation**: Send an empty tab-complete. Collect plugin namespaces. Drill into each one. The server does all the work.

## 🚀 Getting Started (The 2-Minute Warning)

1. **OS Tuning**: Optimizing your kernel for high-frequency spamming is a must.
   ```bash
   sudo ./setup.sh
   ```
2. **Recon first**:
   ```bash
   ./wiretap-bin play.server.com
   ```
3. **Fingerprint the stack**:
   ```bash
   ./pluginscanner-bin play.server.com
   # > scan
   ```
4. **Ignite**:
   ```bash
   ./gaslighter-bin --debug play.server.com
   ```

## 📜 Legalish Stuff
This repository is for **authorized infrastructure testing only**. Using this on servers you don't own is a great way to get your IP blocklisted and your reputation ruined. We are here to "empirically validate JVM limits," not to be a nuisance. Mostly.

*Happy leaking!* 🧊

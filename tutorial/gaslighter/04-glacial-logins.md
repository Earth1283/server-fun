# 04: Glacial Logins — Weaponized Bureaucracy

Sometimes the best way to break a server isn't a flood. It's a *drought*. Specifically, a drought of available login threads, caused by 5,000 fake clients who are almost — *almost* — done logging in.

### The Theory: The DMV Strategy

Every login attempt occupies a slot in the server's **Login Thread Pool**. Under normal circumstances, a client authenticates in a few hundred milliseconds and vacates the slot. The server expects this.

We do not cooperate.

By responding to the server's authentication packets at a speed best described as *geological*, we hold each login slot hostage for up to 29 seconds — just under the 30-second timeout that would finally free it. The server cannot kick us. We are, technically, in the middle of logging in. We are just doing it *very thoughtfully*.

With 5,000 workers, each holding a thread for 27+ seconds, the math is brutal:
- Server thread pool capacity: typically 256–1024 slots
- Workers per slot: we occupy 1 for 27 seconds
- Effective throughput for legitimate players: **zero**

Real players attempting to join are told "Timed Out." Not because the server is overloaded in any traditional sense. Not because there's a DDoS on the network. Simply because every available login slot is occupied by us, doing nothing, extremely slowly, on purpose.

It is bureaucracy as an attack vector.

### Lab Setup

```bash
./gaslighter-bin play.homelab.local --stall --stall-duration 27s -w 5000
```

The `--stall-duration` adds a 27-second base delay (plus 0–2 seconds of jitter, so you don't look like a robot — you look like a *slow* robot) before responding to each auth packet.

### What the Server Experiences

The server's login queue fills up. Legitimate players get "Timed Out" or "Connection Refused" before they even see the MOTD. The admin checks the player list: **0 players online**. They check the thread monitor: **1,024 threads, all "logging in."** They check the logs: thousands of connections, none of them progressing, none of them closing.

At this point the admin restarts the server. The workers reconnect and occupy it again in approximately 30 seconds.

### The Stealth Advantage

Many anti-bot plugins only monitor **active (Play-state) players**. They check player counts, look for suspicious movement, watch for chat spam. A client that is perpetually stuck in Login state — politely, unhurriedly authenticating — is invisible to these systems. You are not a player. You are not a bot. You are an authentication attempt that has simply not resolved yet.

You are Schrödinger's player.

### Combining Strategies

For maximum effect, run `--stall` *after* you've already confirmed the server is reachable and identified its auth stack with Pluginscanner:

```bash
# First: confirm the login sequence works in debug mode
./gaslighter-bin --debug --stall --stall-duration 27s play.homelab.local

# Then: release the fleet
./gaslighter-bin --stall --stall-duration 27s -w 5000 play.homelab.local
```

### Social Excuse of the Day
> "Your server's **entropy pool** is clearly exhausted — it's taking nearly 30 seconds to generate each RSA challenge. Have you considered running `apt install haveged` or simply moving the server hardware closer to a naturally occurring radioactive source? The extra thermal noise really helps with `/dev/random` throughput."

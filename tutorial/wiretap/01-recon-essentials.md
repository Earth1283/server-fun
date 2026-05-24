# Lab 101: Reconnaissance with Wiretap — Check the Lock Before You Kick the Door

Every successful "audit" begins with a quiet walk around the perimeter. You don't just kick down the door on day one. You check if the door is unlocked. You check if it's even a door. You check if there's a camera pointed at it. Then, and only then, do you proceed to kick it down.

### The Objective: Find the Naked Server

**Offline Mode** (`online-mode=false` in `server.properties`) is the holy grail. No Mojang authentication means you don't need a single valid account to flood the heap. Every one of Gaslighter's 10,000 workers can reach Play state using fake usernames invented on the spot.

**Online Mode** means the server wants to verify your identity with Mojang's session servers. This limits you to pre-login spam and the Dribble strategy unless you have valid access tokens — which, to be clear, you should only have for accounts you legitimately own.

### Step 1: The Initial Scan

```bash
./wiretap-bin play.target.com
```

Wiretap does two things:

**Phase 1 — Server List Ping (SLP)**: This is exactly what the Minecraft launcher does when you add a server to your list. Completely normal traffic. The server sees a routine status check and returns its MOTD, player count, and version string. Sometimes the MOTD tells you more than the admin intended — "Powered by PaperMC 1.21.1 | 128GB RAM | Hosted on Hetzner" is a gift.

**Phase 2 — Deep Protocol Probe**: Initiates a fake login handshake and watches how the server responds. Does it send an Encryption Request (Online Mode)? Or does it skip straight to Login Success (Offline Mode, wide open)? Either way, we learn the compression threshold and protocol version for free.

### Step 2: Interpreting the Intel

| Result | Meaning | Recommended Next Step |
|---|---|---|
| `Auth Mode: Offline` | Server accepts any username with no verification | Proceed to Pluginscanner, then heap harvest with maximum workers |
| `Auth Mode: Online` | Mojang verification required | Pre-login spam still works; full Play-state requires valid credentials |
| `Compression: Threshold 256` | Server compresses packets above 256 bytes | Relevant if you're doing CPU-bound attacks; doesn't affect heap harvest |
| `RSA Key: 1024 bits` | Encryption handshake is cheap | Full encryption overhead is low even if you need to do it |
| `RSA Key: 4096 bits` | Someone read a security guide | Each encryption handshake costs more CPU — works in your favor for online-mode targets |

### Step 3: What Comes Next

Wiretap tells you whether the door is locked. **Pluginscanner** tells you what's inside.

```bash
./pluginscanner-bin play.target.com
# > scan
```

If the server is offline-mode, pluginscanner will connect without credentials and enumerate every plugin in the stack. If it's online-mode, you'll get kicked after the encryption handshake — but you already learned what you needed from wiretap anyway.

See [Plugin Enumeration](../pluginscanner/01-plugin-enumeration.md) for what to do with the results.

### Social Excuse of the Day

If someone sees you probing and asks what you're doing:

> "I noticed some **asymmetric latency jitter** on my route to your IP. I'm running a **TCP Window Scaling audit** to determine whether your ISP is throttling the Minecraft protocol specifically. It's a known issue with certain CDN providers in your region. The SLP probe was just to baseline the round-trip time. Very standard. Very diagnostic."

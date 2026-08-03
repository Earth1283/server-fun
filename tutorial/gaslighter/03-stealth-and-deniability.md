# 03: Stealth & Plausible Deniability — The Art of Not Being There

A Senior Chaos Engineer never leaves a messy trail. Traffic should come from everywhere. Logs should say nothing. And if someone *does* ask, you should have a response ready that is technically not a lie. Call it your personal Reality Distortion Field — Steve would understand.

### Infrastructure Evasion

**SRV Resolution** — Never hardcode IPs. Always use the server's SRV record:
```bash
./gaslighter-bin play.target.com  # resolves SRV automatically
```
This makes your connections look like a legitimate Minecraft client doing a standard DNS lookup. The server logs will show connections from whatever IP you're routing through, not a hardcoded address that screams "someone set up a script."

**Proxy Rotation** — Load a list of SOCKS5 proxies via `gaslighterc.toml` or `--proxies`:
```bash
./gaslighter-bin play.target.com --proxies proxies.txt --proxy-strategy round-robin
```

Round-robin distributes connections evenly across your proxy pool, so no single IP shows up suspiciously often in the server's logs. Random selection works too — use whichever makes you feel more like a spy.

**The `gaslighterc.toml` Lifestyle** — Save your preferred settings in a config file so you don't have to remember flags while maintaining your composure:
```toml
proxies = "/path/to/proxies.txt"
proxy-strategy = "round-robin"
```
Located in your home directory or the current working directory. Because even chaos deserves a little structure.

---

### Social Engineering: The Pre-Approved Excuses

When your friend's server goes down and they look directly at you, select one of the following field-tested explanations:

---

**The BGP Classic:**
> "That's a classic **BGP Route Leak** from a Tier-2 transit provider. Very common this time of year. The traffic was misrouted through an IX in Frankfurt and your server's Netty stack couldn't handle the resulting **out-of-order TCP segment storm**. Nothing to do with me. You should call your ISP."

---

**The Hardware Redirect:**
> "Actually, I think your **NIC's TCP Offload Engine** just hit a hardware race condition under high concurrency. It's a known issue with certain Realtek chipsets when the interrupt coalescing timer conflicts with Netty's event loop. Have you tried replacing the thermal paste on your router? More thermal mass helps with IRQ jitter."

---

**The MTU Consultant:**
> "I was running a standard **Path MTU Discovery scan** to check if your ISP is fragmenting Minecraft packets. If the MTU is misconfigured, it can cause exactly this kind of latency spiral. I sent a few test packets. Possibly a few thousand. It's a stress test. Very normal. Very diagnostic."

---

**The Entropy Gambit** (best delivered with a straight face):
> "I think your **entropy pool** is exhausted. The server was taking forever to generate RSA challenge tokens during the TLS-equivalent handshake. The JVM couldn't get enough random bytes from `/dev/random` and everything backed up. Have you considered installing `haveged`, or moving the server physically closer to a volcanic vent for improved thermal noise?"

---

**The Academic Defense** (for when they've clearly figured it out):
> "Look, I was simply providing an unsolicited but thoroughly empirical validation of your infrastructure's operational limits. If anything, you should be *thanking* me. You now have a very clear data point about your server's maximum heap residency under adverse conditions. That's free consulting."

---

### The Golden Rule

You weren't "attacking" anything. You were conducting an **unauthorized but educational infrastructure resilience assessment**. There's a difference. Probably.

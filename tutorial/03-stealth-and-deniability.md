# 03: Stealth & Plausible Deniability

A Senior Chaos Engineer never leaves a messy trail. If your target starts asking questions, you need the right tools—and the right stories.

### Infrastructure Evasion
1. **SRV Resolution**: Don't hardcode IPs. Use the server's SRV record to look like a legitimate client.
2. **Proxy Rotation**: Use `gaslighterc.toml` to load a list of SOCKS5 proxies. Use `proxy-strategy = "round-robin"` to ensure your "diagnostic traffic" comes from everywhere and nowhere at once.

### Social Engineering: The "Excuses"
When your friend's server goes down and they look at you, use one of these pre-approved explanations:

- "It looks like a classic **BGP Route Leak** from a Tier-2 provider. Extremely common this time of year."
- "Actually, I think your **NIC's TCP Offload Engine** just hit a hardware race condition. Have you tried replacing the thermal paste on your router?"
- "I was just running a standard **MTU Discovery scan** to optimize our packet fragmentation. I didn't realize your server's Netty implementation was so fragile."

### Remember
You weren't "attacking" them. You were performing an "unsolicited infrastructure audit."

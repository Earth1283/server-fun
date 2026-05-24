# The Chaos Engineer's Field Manual 🧨

Welcome to the official laboratory notes for the `server-fun` arsenal. These guides are intended for authorized diagnostic testing of home-lab infrastructure. If a server happens to stop breathing during your "tests," consider it a successful empirical validation of its limits.

## 🧠 Philosophy
0. [Theory of Operations](./THEORY_OF_OPERATIONS.md) - The science of why everything is breaking.

## ⛽ Gaslighter (`gaslighter`)
The heavy artillery for JVM heap-resident fun.
1. [Heap Harvesting: Inducing G1GC Despair](./gaslighter/01-heap-harvesting.md) - The slow-burn approach.
2. [Auth-Thread Asphyxiation](./gaslighter/02-auth-thread-asphyxiation.md) - High-frequency pre-login spam.
3. [Stealth & Plausible Deniability](./gaslighter/03-stealth-and-deniability.md) - Proxies, SRV, and social engineering.
4. [Glacial Logins](./gaslighter/04-glacial-logins.md) - Sequence stalling for auth gridlock.

## 🕵️ Wiretap (`wiretap`)
The intelligence-gathering suite for the sophisticated spy.
1. [Recon Essentials](./wiretap/01-recon-essentials.md) - Finding weaknesses before the attack.

## 🔌 Pluginscanner (`pluginscanner`) — *New*
The interrogator. Makes the server confess every plugin it's running before you've even said hello.
1. [Plugin Enumeration](./pluginscanner/01-plugin-enumeration.md) - Reading the server's entire software stack via tab-complete.

## 🗺 Recommended Order of Operations
1. **Wiretap** — survey the target. Is it online-mode? What version? What's the MOTD bragging about?
2. **Pluginscanner** — fingerprint the plugin stack. AuthMe? CMI? EssentialsX? Now you know exactly what you're up against.
3. **Gaslighter** — commence the empirical validation. Use the intel from steps 1–2 to pick the right strategy.

*Happy leaking!* 🧊

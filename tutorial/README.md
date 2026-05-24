# The Chaos Engineer's Field Manual 🧨

Welcome to the official laboratory notes for the `server-fun` arsenal. These guides exist for authorized diagnostic testing of home-lab infrastructure, and absolutely nothing else. If a server happens to stop breathing mid-"test," that's called a **successful empirical validation of its architectural limits**. Write that on the incident report.

## 🧠 Philosophy
0. [Theory of Operations](./THEORY_OF_OPERATIONS.md) — The science of why everything is breaking. Required reading before you touch anything.

## ⛽ Gaslighter (`gaslighter`)
The main event. Brings a JVM to its knees through the ancient art of *holding on and never letting go.*
1. [Heap Harvesting: Inducing G1GC Despair](./gaslighter/01-heap-harvesting.md) — The slow-burn classic. Patience is a virtue. Heap exhaustion is a gift.
2. [Auth-Thread Asphyxiation](./gaslighter/02-auth-thread-asphyxiation.md) — For when you want results *now* and the heap can wait.
3. [Stealth & Plausible Deniability](./gaslighter/03-stealth-and-deniability.md) — How to route your traffic through six countries and still have a straight face when asked about it.
4. [Glacial Logins](./gaslighter/04-glacial-logins.md) — Weaponized bureaucracy. Make the server wait. Forever.

## 🕵️ Wiretap (`wiretap`)
The reconnaissance arm. Checks the pulse before you pull the plug.
1. [Recon Essentials](./wiretap/01-recon-essentials.md) — Finding the unlocked door before you bother kicking it in.

## 🔌 Pluginscanner (`pluginscanner`)
The interrogator. Connects to the server, smiles politely, and gets the server to confess its entire software stack without raising a single alarm.
1. [Plugin Enumeration](./pluginscanner/01-plugin-enumeration.md) — Reading the server's diary via tab-complete.

## 🗺 The Recommended Order of Operations

Think of it as a heist. You don't walk into the vault on day one.

1. **Wiretap** — Case the joint. Online or offline mode? What's the MOTD bragging about? Is there a proxy in front that's hiding the real target?
2. **Pluginscanner** — Read the nametags. AuthMe? EssentialsX? LuckPerms? Now you know exactly who you're dealing with and which of their habits to exploit.
3. **Gaslighter** — Execute. Use the intel from steps 1 and 2 to pick the right weapon and apply it until something interesting happens to the server's uptime graph.

*Happy leaking!* 🧊

# Lab 101: Reconnaissance with wiretap

Every successful "audit" begins with a quiet walk around the perimeter. You don't just kick down the door; you check if the door is even locked.

### The Objective: Finding the Naked Server
Offline-mode servers (where `online-mode=false`) are the holy grail of chaos engineering. No Mojang authentication means you don't need a single valid account to flood the heap.

### Step 1: The Initial Scan
```bash
./wiretap-bin target.com
```

### Step 2: Interpreting the Intel
- **"Auth Mode: Online"**: The target is protected by Mojang's session servers. You'll need valid access tokens for a full Play-state attack, or you'll be limited to pre-login spam and the "Dribble" strategy.
- **"Auth Mode: Offline"**: The target is wide open. You can spawn 50,000 workers with zero credentials and they will all reach the Play state. This is what we call a "Target Rich Environment."
- **"Compression: Threshold 256"**: This tells you the server is using Netty's compression layer. Good to know for CPU-bound attacks.

### Social Excuse of the Day
If someone sees you probing:
"I noticed some **asymmetric latency jitter** in my route to your IP. I'm just running a **TCP Window Scaling audit** to see if your ISP is throttling the Minecraft protocol. You're welcome!"

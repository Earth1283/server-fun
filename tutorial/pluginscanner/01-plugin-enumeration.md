# 01: Plugin Enumeration — Getting the Server to Read You Its Diary

Every Minecraft server is running a stack of plugins it has never thought to hide, because it has never occurred to anyone that a client would ask about them quite so deliberately. Pluginscanner asks. The server answers. Completely. With a list.

It takes about 10 seconds. The server logs it as "a player joined and left." Nothing to see here.

### The Objective: Map the Software Stack Before You Touch It

Before committing 10,000 goroutines to a target, you want to know what you're dealing with. Different plugins have different vulnerabilities under load. AuthMe has a database connection pool. EssentialsX has an async command queue. LuckPerms has permission lookups on every action. Knowing what's installed tells you exactly which pressure points to squeeze.

### Step 0: Run Wiretap First

Pluginscanner needs to reach **Play state** to send tab-complete requests. Play state requires surviving the login sequence. Online-mode servers will kick you during encryption unless you supply real credentials.

```bash
./wiretap-bin play.target.com
```

If you see `Auth Mode: Offline`, proceed. If you see `Auth Mode: Online`, you have two options: supply `--access-token` and `--player-uuid` for a valid account you own, or accept that you're staying at the login-phase intelligence and can't get deeper.

### Step 1: Connect

```bash
./pluginscanner-bin play.target.com
```

The entire connection sequence logs verbosely to your terminal. Every packet, every state transition, every handshake:

```
01:44:41.123  → SEND  0x00  Handshake                      247 B
01:44:41.124  → SEND  0x00  Login Start                     22 B
01:44:41.126  ← RECV  0x03  Set Compression                  2 B   [00 05 ...]
01:44:41.127  ← RECV  0x02  Login Success                   34 B
01:44:41.127  → SEND  0x03  Login Acknowledged               2 B
01:44:41.128  [Login → Configuration]
01:44:41.200  ← RECV  0x09  Known Packs                     12 B
01:44:41.201  → SEND  0x07  Known Packs Response             1 B
01:44:41.202  ← RECV  0x03  Finish Configuration             1 B
01:44:41.203  → SEND  0x03  Acknowledge Configuration        1 B
01:44:41.203  [Configuration → Play]

✓  connected as ScannerXyz42 on play.target.com
```

By the time the prompt appears, the server has already told you its compression threshold, protocol version, and whether it uses a proxy-forwarding plugin in the config phase. You haven't even typed anything yet.

### Step 2: Enumerate Everything

```
> scan
```

This sends a **Command Suggestions Request** with empty text — the protocol equivalent of pressing Tab in chat with nothing typed. The server, helpfully, responds with its entire command namespace registry, because that's what Tab is supposed to do.

```
[scan]  found 9 plugin namespace(s):

  authme               4 cmds
                       login  register  changepassword  unregister
  essentials           47 cmds
                       fly  tp  home  warp  kit  gamemode  nick  heal ...
  vault                3 cmds
                       eco  balance  pay
  worldguard           12 cmds
                       region  flag  addmember  removemember  info ...
  luckperms            8 cmds
                       user  group  track  editor  export  import ...
  coreprotect          6 cmds
                       co  lookup  rollback  restore  inspect  near
  ajleaderboards       2 cmds
                       ajleaderboards  leaderboard
  viaversion           4 cmds
                       viaversion  vv  viarewind  viabackwards
  minecraft            21 cmds
                       (vanilla — less interesting unless you're here for something else)

[scan complete]  9 plugin(s) found
```

### Step 3: Drill Down

Once you have namespaces, you can probe specific plugins for their complete command list:

```
> probe authme
> luckperms:
> essentials:eco
```

Type a namespace followed by `:` and pluginscanner will send a targeted tab-complete request for that plugin's commands specifically.

### Step 4: Interpret the Intel — The Plugin Translation Table

| Plugin Found | What It Actually Means |
|---|---|
| `authme` / `nexauth` / `nlogin` | Auth plugin present. Has a database connection pool. `--prelogin --har` will exhaust it. |
| `cmi` | Combined auth + economy mega-plugin. Even more database connections. Consider it a target-rich environment. |
| `essentials` / `essentialsx` | Economy, homes, warps — all async DB queries. `--login` mode will trigger `/register` spam and compound the auth plugin's misery. |
| `vault` | Economy API bridge. Multiple plugins are sharing a single DB connection pool through this. Collateral damage potential: high. |
| `luckperms` | Permission lookups on literally every command. More async DB pressure per player action. |
| `coreprotect` | Block logging plugin. Records every action. With `--login` mode, you can fill its log database with garbage entries. |
| `viaversion` / `viarewind` | Multi-version support proxy. The server is accepting connections from players on different Minecraft versions. Usually indicates the admin cares about accessibility, which means they care about uptime. |
| `bungee` / `velocity` / `liaison` / `redisbungee` | **You are talking to a proxy frontend.** The real backend server is almost certainly running on a different port — often `25566` — with *no* connection protection whatsoever, because "players never connect directly." They do now. |
| `minecraft` only | Vanilla or a stripped server. The heap harvest still works perfectly; plugin-specific strategies don't apply. |

### The Part Where We Explain the 0x0F Incident

The tab-complete request uses **serverbound packet `0x0B`** in Minecraft protocol 767 (1.21.1). We initially sent `0x0F`. The server told us, in no uncertain terms, that `0x0F` is the Close Container packet, it expected 1 byte, we sent 4, and we could leave immediately. We have since corrected this. The comment in the source code will preserve the memory forever.

### Social Excuse of the Day

The server logs will show something like: `ScannerXyz42[/1.2.3.4:49201] logged in... ScannerXyz42 left the game.`

When the admin notices:
> "Oh that? My client's **command tree pre-caching** feature downloaded your server's full command registry on join to improve tab-complete response times locally. It's a performance optimization. Very standard behavior in modern Minecraft clients. You should see it as a compliment that your plugin setup is complex enough to make the caching worthwhile."

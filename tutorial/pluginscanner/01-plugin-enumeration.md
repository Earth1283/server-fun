# 01: Plugin Enumeration — Making the Server Tell on Itself

Every Minecraft server is running a stack of plugins it would rather keep private. Pluginscanner makes it public knowledge in about 10 seconds, using nothing but the server's own tab-complete system.

### The Objective: Map the Software Stack

Before you commit 10,000 goroutines to a target, you want to know what you're dealing with. AuthMe? EssentialsX? CMI? Each plugin has different behaviors under load. This is how you find out.

### Step 1: Run Wiretap First

You need to confirm the server is in **Offline Mode** before pluginscanner will get far. Online-mode servers will kick you during the encryption handshake (unless you supply valid Mojang credentials with `--access-token` and `--player-uuid`).

```bash
./wiretap-bin play.target.com
```

Look for `Auth Mode: Offline`. If you see `Auth Mode: Online`, you have two options:
- Supply real credentials via `--access-token` / `--player-uuid`
- Accept that you're limited to the connection phase logs (still useful for compression threshold, version info, etc.)

### Step 2: Connect

```bash
./pluginscanner-bin play.target.com
```

Watch the connection log scroll by. Every packet in the Handshake → Login → Config → Play sequence is printed with timestamps and IDs. By the time the prompt appears, you've already seen more about this server's protocol behavior than its own admin has.

```
01:44:41.123  → SEND  0x00  Handshake                      247 B
01:44:41.124  → SEND  0x00  Login Start                     22 B
01:44:41.126  ← RECV  0x03  Set Compression                  2 B
01:44:41.200  ← RECV  0x02  Login Success                   34 B
...
01:44:41.813  [Configuration → Play]
✓  connected as ScannerXYZ42 on play.target.com
```

### Step 3: Enumerate Everything

```
> scan
```

This sends a tab-complete request with empty text — the protocol equivalent of pressing Tab in chat with nothing typed. The server responds with its entire command namespace registry.

```
[scan]  found 8 plugin namespace(s):

  authme               4 cmds
                       login  register  changepassword  unregister
  essentials           47 cmds
                       fly  tp  home  warp  kit  gamemode  ...
  vault                3 cmds
                       eco  balance  pay
  worldguard           12 cmds
                       region  flag  addmember  ...
  luckperms            8 cmds
                       user  group  track  ...
  coreprotect          6 cmds
                       co  lookup  rollback  ...
  ajleaderboards       2 cmds
                       ...
  minecraft            21 cmds
                       (vanilla commands — less interesting)

[scan complete]  8 plugin(s) found
```

### Step 4: Drill Down

Once you have namespaces, probe specific plugins for their full command list:

```
> probe authme
> luckperms:
> essentials:tp
```

### Step 5: Interpret the Intel

| Plugin Found | What It Means for Gaslighter |
|---|---|
| `authme` / `nexauth` / `cmi` | Auth plugin present — `--prelogin` and `--har` will hammer its database connection pool |
| `essentials` / `cmi` | Economy + home system — lots of async DB queries per player action; `--login` mode will trigger them |
| `vault` | Economy API bridge — indicates multiple plugins sharing a DB connection pool |
| `luckperms` | Permission lookups on every command — extra DB load |
| `bungee` / `velocity` / `liaison` | You're talking to a proxy frontend — the real backend is probably running unprotected on a different port |
| `minecraft` only | Vanilla or a heavily stripped server — heap harvest still works, but plugin-specific strategies won't apply |

### Social Excuse of the Day

If the server admin sees a mysterious "ScannerXYZ42 joined and left immediately":

> "Oh that? I was testing whether my new Minecraft client's **command pre-caching** feature was working correctly. It downloads the server's command tree on join for faster tab-complete response times. Very standard. Very normal."

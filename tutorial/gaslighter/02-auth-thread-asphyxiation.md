# 02: Auth-Thread Asphyxiation — For the Impatient Engineer

The heap harvest is elegant. It is patient. It is the long con. But sometimes you don't have time for the long con. Sometimes you need the server to buckle *now*, and the JVM's garbage collector can take the afternoon off.

This is the guide for those times.

### The Strategy: Abuse the Pre-Login Pipeline

Paper/Spigot servers fire an `AsyncPlayerPreLoginEvent` for every new connection attempt, before the player has even authenticated. Plugins hook into this event to do expensive things:

- **LuckPerms** fetches the player's permission group from a database
- **AuthMe / CMI / NexAuth** check if the username exists in their auth database
- **Anti-bot plugins** query GeoIP APIs, check VPN blacklists, consult their feelings

All of these database calls open **connections from the server's DB connection pool**. Connection pools are finite. When the pool is exhausted, legitimate players get "Timed Out" — not because the server is overloaded, but because every database connection is occupied by us, doing fake logins.

### Mode 1: Standard Pre-Login Spam (`--prelogin`)

```bash
./gaslighter-bin target.com --prelogin -w 20000
```

Send the Login Start packet, wait briefly for the server to process it (and fire `AsyncPlayerPreLoginEvent`), then close the socket. Repeat. Thousands of times per second. The server's async login pipeline becomes a revolving door that never stops spinning.

### Mode 2: Hit-and-Run (`--prelogin --har`)

```bash
./gaslighter-bin target.com --prelogin --har -w 20000
```

Don't even wait for a response. Send the Login Start and immediately hang up. The server is left holding the bag — and the thread, and the database query, and the anti-bot check — while we've already moved on to the next victim.

This is the digital equivalent of ordering food at a drive-through from all 14 windows simultaneously and then driving away.

### What You're Actually Exhausting

The server's **connection pool** is the real target here. Most database-backed plugins configure a pool of 10–20 simultaneous DB connections. Once those are saturated, every new auth attempt blocks until one frees up. Legitimate players timeout. The admin's monitoring dashboard turns red. The Discord server lights up with "IS THE SERVER DOWN??" messages.

The beautiful part: because pre-login spam never actually authenticates, many anti-bot plugins that only watch *connected players* will never see you. You are a ghost. A very busy, very expensive ghost.

### Diagnostic Signs Your Attack Is Working
- High CPU on the server's "Async Login Thread" pool
- "Timed out" messages for legitimate players (the real ones)
- Log spam: `[AuthMe] Database connection pool exhausted` or similar
- Server admin posting "we're being attacked by bots" in their Discord, which is technically accurate

### Social Excuse of the Day
> "I was running a **connection pool saturation benchmark** to help you identify the optimal `maxPoolSize` setting for your HikariCP configuration. You should probably set it higher. You're welcome."

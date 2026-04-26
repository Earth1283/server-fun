# 02: Auth-Thread Asphyxiation

Sometimes you don't have the patience for a slow-burn heap attack. Sometimes you just want to see the server's authentication pipeline buckle under the weight of its own existence.

### The Strategy: Pre-Login Spam
Paper/Spigot servers fire an `AsyncPlayerPreLoginEvent` for every new connection. Many servers have heavy plugins (LuckPerms, database-backed auth, anti-bot) that hook into this event. 

By using `--prelogin`, we fire these events at the speed of Go's scheduler.

### The "Hit-and-Run" (--har)
Normal bots wait for a server response. Professionals don't.
```bash
./gaslighter target.com --prelogin --har -w 20000
```
This sends the `Login Start` packet and closes the socket immediately. The server is left holding the bag (and the thread) while you've already moved on to the next worker.

### Diagnostic Signs
- High CPU usage on the server's "Async Chat/Login Thread."
- "Timed out" messages for legitimate players.
- Database connection pool exhaustion on the backend.

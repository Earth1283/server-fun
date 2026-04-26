# 04: Glacial Logins - The Art of the Sequence Stall

Sometimes, the best way to break a server isn't with a flood, but with a drought. Specifically, a drought of available login threads.

### The Theory: Sequence Stalling
Every time a client attempts to log in, the server's **Login Thread Pool** allocates a spot. Most servers expect this process to take a few hundred milliseconds. If you never finish the login, the server eventually kicks you—but not before holding that thread (and its associated memory objects) for up to 30 seconds.

By using `--stall`, we slow down our responses to the server's authentication packets to just under the timeout limit. 

### Lab Setup
```bash
./gaslighter-bin play.homelab.local --stall --stall-duration 27s -w 5000
```
This tells Gaslighter to wait 27 seconds (plus a little jitter) before responding to each server packet. With 5,000 workers, you can perpetually occupy the server's entire login capacity.

### What to Expect
- **Authentication Gridlock**: Legitimate players will get "Timed Out" or "Connection Refused" before they even see the MOTD.
- **Stealthy Residency**: Because you aren't actually "connected" yet, many anti-bot plugins that only check *active* players will never see you.
- **Resource Asphyxiation**: The server's CPU and database pool will slowly climb as it tries to manage thousands of "hanging" authentication states.

### Social Excuse of the Day
"I think your **entropy pool** is exhausted. The server is taking forever to generate the RSA challenge-response. Have you tried installing `haveged` or moving the server closer to a volcanic vent for more thermal noise?"

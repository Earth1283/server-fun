# Proxy Strategy Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add configurable proxy selection strategies (random and round-robin) to `mc-stress`.

**Architecture:** Use a global `atomic.Uint64` to track round-robin state. Update `init()` to add a `proxy-strategy` flag and `getDialer()` to use it.

**Tech Stack:** Go, Viper, Cobra, sync/atomic

---

### Task 1: Add Global State and Flag

**Files:**
- Modify: `mc-stress/main.go`

- [ ] **Step 1: Add `proxyCounter` to global variables**

```go
var (
	activeConns  atomic.Int64
	bytesSent    atomic.Int64
	droppedConns atomic.Int64
	newConns     atomic.Int64
	proxyCounter atomic.Uint64 // New variable

	// Mojang credentials for online-mode auth. If empty, encryption is attempted
```

- [ ] **Step 2: Add `proxy-strategy` flag to `init()`**

```go
func init() {
	initConfig()
	f := rootCmd.Flags()
	f.IntP("workers", "w", 10000, "concurrent connections to maintain")
	f.IntP("bloat-size", "s", 255, "handshake server-address string length (max 255)")
	f.DurationP("dribble-interval", "d", 5*time.Second, "interval between keep-alive bytes")
	f.DurationP("join-delay", "j", 0, "minimum gap between new connections (e.g. 100ms)")
	f.StringP("access-token", "a", "", "Mojang access token for online-mode auth")
	f.StringP("player-uuid", "u", "", "Mojang player UUID matching the access token")
	f.BoolP("verbose", "v", false, "print per-connection TCP errors")
	f.Bool("prelogin", false, "pre-login spam mode: fire-and-forget login events")
	f.Bool("har", false, "hit-and-run mode: don't wait for server response in pre-login mode")
	f.StringP("proxies", "p", "", "path to .txt file with SOCKS5 proxies")
	f.String("proxy-strategy", "random", "proxy selection strategy: random or round-robin")
	viper.BindPFlags(f)
}
```

- [ ] **Step 3: Commit**

```bash
git add mc-stress/main.go
git commit -m "feat: add proxy-strategy flag and global counter"
```

---

### Task 2: Update `getDialer` Logic

**Files:**
- Modify: `mc-stress/main.go`

- [ ] **Step 1: Update `getDialer()` to respect `proxy-strategy`**

```go
func getDialer() (proxy.Dialer, error) {
	baseDialer := &net.Dialer{Timeout: 10 * time.Second}
	if len(proxyPool) == 0 {
		return baseDialer, nil
	}

	var proxyAddr string
	strategy := viper.GetString("proxy-strategy")
	if strategy == "round-robin" {
		counter := proxyCounter.Add(1)
		idx := (counter - 1) % uint64(len(proxyPool))
		proxyAddr = proxyPool[idx]
	} else {
		// default to random
		proxyAddr = proxyPool[rand.Intn(len(proxyPool))]
	}

	return proxy.SOCKS5("tcp", proxyAddr, nil, baseDialer)
}
```

- [ ] **Step 2: Verify compilation**

Run: `go build -o gaslighter .` in `mc-stress`
Expected: Success

- [ ] **Step 3: Commit**

```bash
git add mc-stress/main.go
git commit -m "feat: implement random and round-robin proxy strategies"
```

---

### Task 3: Update Documentation

**Files:**
- Modify: `mc-stress/README.rst`

- [ ] **Step 1: Add `proxy-strategy` to Usage section**

```rst
    Flags:
      -s, --bloat-size int             handshake server-address string length (max 255) (default 255)
          --debug                      single-connection debug mode with colored packet log
      -d, --dribble-interval duration  interval between keep-alive bytes, online-mode fallback (default 5s)
          --har                        hit-and-run: don't wait for server response in pre-login mode
      -j, --join-delay duration        minimum gap between new connections (e.g. 4001ms)
          --prelogin                   pre-login spam mode: fire-and-forget login events
      -p, --proxies string             path to a file containing proxies (ip:port, one per line)
          --proxy-strategy string      proxy selection strategy: random or round-robin (default "random")
      -a, --access-token string        Mojang access token for online-mode auth
```

- [ ] **Step 2: Add `proxy-strategy` to Configuration File section**

```toml
    proxies = "proxies.txt"
    proxy-strategy = "round-robin"
    workers = 5000
    join-delay = "1s"
```

- [ ] **Step 3: Commit**

```bash
git add mc-stress/README.rst
git commit -m "docs: document proxy-strategy flag"
```

# Proxy and Configuration File Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add SOCKS5 proxy support and a TOML-based configuration file (`gaslighterc.toml`) to the `mc-stress` tool.

**Architecture:** 
- Use `viper` for loading and merging configuration from `~/gaslighterc.toml` and `./gaslighterc.toml`.
- Implement a proxy pool that selects a random proxy for each new connection.
- Wrap `net.Dialer` with `golang.org/x/net/proxy` to tunnel traffic through SOCKS5.

**Tech Stack:** Go 1.22+, Cobra, Viper, golang.org/x/net/proxy, github.com/pelletier/go-toml/v2.

---

### Task 1: Initialize Viper and Config Loading

**Files:**
- Modify: `mc-stress/main.go`

- [ ] **Step 1: Add imports and global variables**
Add `github.com/spf13/viper`, `path/filepath`, and a global `proxyPool []string`.

```go
import (
    // ... existing imports
    "github.com/spf13/viper"
    "golang.org/x/net/proxy"
)

var (
    // ... existing vars
    proxyPool []string
)
```

- [ ] **Step 2: Implement initConfig function**
Create a function to load TOML config from home and local directories.

```go
func initConfig() {
	home, err := os.UserHomeDir()
	if err == nil {
		viper.AddConfigPath(home)
	}
	viper.AddConfigPath(".")
	viper.SetConfigName("gaslighterc")
	viper.SetConfigType("toml")

	viper.AutomaticEnv()
	viper.ReadInConfig()
}
```

- [ ] **Step 3: Call initConfig and bind flags**
Update `init()` to call `initConfig()` and bind Cobra flags to Viper.

```go
func init() {
    initConfig()
    f := rootCmd.Flags()
    // ... existing flags
    f.StringP("proxies", "p", "", "path to .txt file with SOCKS5 proxies")
    
    viper.BindPFlags(f)
}
```

- [ ] **Step 4: Commit**
`git add mc-stress/main.go && git commit -m "feat: initialize viper and config loading"`

---

### Task 2: Proxy List Loading

**Files:**
- Modify: `mc-stress/main.go`

- [ ] **Step 1: Implement loadProxies helper**
Read the proxy file and populate `proxyPool`.

```go
func loadProxies(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			proxyPool = append(proxyPool, line)
		}
	}
	return nil
}
```

- [ ] **Step 2: Update RunE to call loadProxies**
Get the proxy path from Viper and load them.

```go
RunE: func(cmd *cobra.Command, args []string) error {
    // ... existing logic
    proxyPath := viper.GetString("proxies")
    if proxyPath != "" {
        if err := loadProxies(proxyPath); err != nil {
            return fmt.Errorf("failed to load proxies: %w", err)
        }
        fmt.Printf("Loaded %d proxies from %s\n", len(proxyPool), proxyPath)
    }
    // ...
}
```

- [ ] **Step 3: Commit**
`git add mc-stress/main.go && git commit -m "feat: implement proxy list loading"`

---

### Task 3: Proxy-Aware Dialer

**Files:**
- Modify: `mc-stress/main.go`

- [ ] **Step 1: Implement getDialer helper**
Create a function that returns a dialer, optionally wrapped with SOCKS5.

```go
func getDialer() (proxy.Dialer, error) {
	baseDialer := &net.Dialer{
		Timeout: 10 * time.Second,
	}

	if len(proxyPool) == 0 {
		return baseDialer, nil
	}

	proxyAddr := proxyPool[rand.Intn(len(proxyPool))]
	return proxy.SOCKS5("tcp", proxyAddr, nil, baseDialer)
}
```

- [ ] **Step 2: Update worker to use getDialer**
Replace `net.DialTimeout` with the proxy-aware dialer.

```go
func worker(...) {
    // ...
    for {
        // ...
        dialer, err := getDialer()
        if err != nil {
            dbgErr("proxy setup", err)
            time.Sleep(1 * time.Second)
            continue
        }

        conn, err := dialer.Dial("tcp", target)
        // ...
    }
}
```

- [ ] **Step 3: Update debugRun to use getDialer**
Ensure debug mode also supports proxies.

```go
func debugRun(...) {
    // ...
    dialer, err := getDialer()
    if err != nil {
        dbgErr("proxy setup", err)
        return
    }
    conn, err := dialer.Dial("tcp", target)
    // ...
}
```

- [ ] **Step 4: Commit**
`git add mc-stress/main.go && git commit -m "feat: integrate proxy-aware dialer into worker and debugRun"`

---

### Task 4: Documentation Update

**Files:**
- Modify: `mc-stress/README.rst`

- [ ] **Step 1: Add --proxies flag to Usage**
Add the flag description and an example.

- [ ] **Step 2: Add Configuration File section**
Explain `gaslighterc.toml` locations and merging logic.

- [ ] **Step 3: Commit**
`git add mc-stress/README.rst && git commit -m "docs: update readme with proxy and config file details"`

---

### Task 5: Verification

- [ ] **Step 1: Verify local config override**
Create a `gaslighterc.toml` in the current directory and verify it's loaded.
- [ ] **Step 2: Verify CLI flag override**
Run with a flag and verify it overrides the TOML config.
- [ ] **Step 3: Verify proxy selection**
Load a list of mock proxies and verify (via verbose mode or logs) that different proxies are picked.

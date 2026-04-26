# Proxy-Aware Dialer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement a proxy-aware dialer and integrate it into `worker` and `debugRun`.

**Architecture:** Use `golang.org/x/net/proxy` to create SOCKS5 dialers from a pool of proxy addresses.

**Tech Stack:** Go, golang.org/x/net/proxy.

---

### Task 1: Update Imports and Implement `getDialer`

**Files:**
- Modify: `mc-stress/main.go`

- [ ] **Step 1: Update imports**
  Change `_ "golang.org/x/net/proxy"` to `"golang.org/x/net/proxy"`.

- [ ] **Step 2: Implement `getDialer`**
  Add the following function to `mc-stress/main.go`:
  ```go
  func getDialer() (proxy.Dialer, error) {
  	baseDialer := &net.Dialer{Timeout: 10 * time.Second}
  	if len(proxyPool) == 0 {
  		return baseDialer, nil
  	}
  	proxyAddr := proxyPool[rand.Intn(len(proxyPool))]
  	return proxy.SOCKS5("tcp", proxyAddr, nil, baseDialer)
  }
  ```

- [ ] **Step 3: Commit**
  ```bash
  git add mc-stress/main.go
  git commit -m "feat: implement getDialer and update imports"
  ```

### Task 2: Update `worker` to use `getDialer`

**Files:**
- Modify: `mc-stress/main.go`

- [ ] **Step 1: Update `worker` logic**
  In the `worker` function, inside the `for` loop, replace `net.DialTimeout` with `getDialer` and `dialer.Dial`.
  
  ```go
  dialer, err := getDialer()
  if err != nil {
      if verbose {
          fmt.Fprintf(os.Stderr, "\nproxy dialer: %v\n", err)
      }
      time.Sleep(time.Second)
      continue
  }

  conn, err := dialer.Dial("tcp", target)
  ```

- [ ] **Step 2: Commit**
  ```bash
  git add mc-stress/main.go
  git commit -m "feat: update worker to use proxy-aware dialer"
  ```

### Task 3: Update `debugRun` to use `getDialer`

**Files:**
- Modify: `mc-stress/main.go`

- [ ] **Step 1: Update `debugRun` logic**
  In the `debugRun` function, replace `net.DialTimeout` with `getDialer` and `dialer.Dial`.

  ```go
  dialer, err := getDialer()
  if err != nil {
      dbgErr("proxy dialer", err)
      return
  }
  conn, err := dialer.Dial("tcp", target)
  ```

- [ ] **Step 2: Commit**
  ```bash
  git add mc-stress/main.go
  git commit -m "feat: update debugRun to use proxy-aware dialer"
  ```

### Task 4: Verification

**Files:**
- None

- [ ] **Step 1: Run build**
  Run: `go build -o gaslighter .` in `mc-stress` directory.
  Expected: Successful compilation.

- [ ] **Step 2: Final commit**
  ```bash
  git add docs/superpowers/plans/2026-04-26-proxy-dialer.md docs/superpowers/specs/2026-04-26-proxy-dialer-design.md
  git commit -m "docs: add proxy dialer design and plan"
  ```

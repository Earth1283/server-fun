# Pre-Login Spam Mode Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement `--prelogin` and `--har` flags to allow high-frequency triggering of `AsyncPlayerPreLoginEvent`.

**Architecture:** Add new flags to Cobra `rootCmd`, propagate them to the `worker` function, and implement a dedicated loop branch in `worker` for the spam mode.

**Tech Stack:** Go 1.22, Cobra

---

### Task 1: Add CLI Flags to `main.go`

**Files:**
- Modify: `mc-stress/main.go`

- [ ] **Step 1: Add global variables for new flags**

Add `prelogin` and `har` variables to the global `var` block.

```go
var (
	accessToken string
	playerUUID  string
	login       bool
	prelogin    bool // Add this
	har         bool // Add this
)
```

- [ ] **Step 2: Initialize flags in `init()`**

Update the `init()` function to register the new flags.

```go
func init() {
	f := rootCmd.Flags()
	// ... existing flags ...
	f.Bool("prelogin", false, "enable high-frequency pre-login spam mode")
	f.Bool("har", false, "hit-and-run mode for --prelogin (don't wait for server response)")
}
```

- [ ] **Step 3: Update `RunE` to capture flags**

Update the `RunE` function in `rootCmd` to read the new flag values.

```go
		prelogin, _ = cmd.Flags().GetBool("prelogin")
		har, _ = cmd.Flags().GetBool("har")
```

- [ ] **Step 4: Commit**

```bash
git add mc-stress/main.go
git commit -m "feat: add --prelogin and --har flags to CLI"
```

### Task 2: Update `worker` Function Signature and Logic

**Files:**
- Modify: `mc-stress/main.go`

- [ ] **Step 1: Update `worker` signature**

Update `worker` to accept `prelogin` and `har` parameters.

```go
func worker(target string, port uint16, bloatSize int, dribbleInterval time.Duration, verbose bool, seed int64, prelogin bool, har bool) {
```

- [ ] **Step 2: Implement `prelogin` branch in `worker` loop**

Add the logic to handle the spam mode inside the `worker` loop.

```go
		// ... dial ...
		
		// ... send handshake ...

		loginPkt := buildLoginStart(randString(rng, 16))
		// ... send login start ...

		if prelogin {
			if !har {
				// Wait for any packet back to ensure the server processed Login Start
				conn.SetReadDeadline(time.Now().Add(2 * time.Second))
				readPacket(conn, false)
			}
			conn.Close()
			activeConns.Add(1) // Briefly count as active for metrics
			activeConns.Add(-1)
			newConns.Add(1)
			continue
		}

		activeConns.Add(1)
		newConns.Add(1)
		// ... rest of normal worker logic ...
```

- [ ] **Step 3: Update `RunE` to pass new parameters to `worker`**

```go
		for i := 0; i < workers; i++ {
			go worker(target, port, bloatSize, dribble, verbose, time.Now().UnixNano()+int64(i), prelogin, har)
		}
```

- [ ] **Step 4: Commit**

```bash
git add mc-stress/main.go
git commit -m "feat: implement pre-login spam logic in worker"
```

### Task 3: Documentation and Verification

**Files:**
- Modify: `mc-stress/README.rst`

- [ ] **Step 1: Update README.rst with new flag descriptions**

Add documentation for `--prelogin` and `--har`.

- [ ] **Step 2: Verify compilation**

Run: `go build -o mc-stress-bin ./mc-stress`
Expected: Success.

- [ ] **Step 3: Commit**

```bash
git add mc-stress/README.rst
git commit -m "docs: document --prelogin and --har flags in README"
```

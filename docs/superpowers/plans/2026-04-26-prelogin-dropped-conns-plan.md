# Pre-login droppedConns improvement Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Increment `droppedConns` counter when a read error occurs during pre-login spam.

**Architecture:** Update the `worker` function in `mc-stress/main.go` to check the error from `readPacket` when `har` is false and increment the atomic counter.

**Tech Stack:** Go (Golang)

---

### Task 1: Update worker function in mc-stress/main.go

**Files:**
- Modify: `mc-stress/main.go`

- [ ] **Step 1: Modify the prelogin block to check for errors**

In the `worker` function, update the `prelogin` block to capture and handle the error from `readPacket`.

```go
<<<<
		if prelogin {
			if !har {
				// Wait for any packet back to ensure the server processed Login Start
				conn.SetReadDeadline(time.Now().Add(2 * time.Second))
				readPacket(conn, false)
			}
			conn.Close()
====
		if prelogin {
			if !har {
				// Wait for any packet back to ensure the server processed Login Start
				conn.SetReadDeadline(time.Now().Add(2 * time.Second))
				if _, _, err := readPacket(conn, false); err != nil {
					if verbose {
						fmt.Fprintf(os.Stderr, "\nprelogin read: %v\n", err)
					}
					droppedConns.Add(1)
				}
			}
			conn.Close()
>>>>
```

- [ ] **Step 2: Run go build to verify syntax**

Run: `go build -o /dev/null mc-stress/main.go`
Expected: Success

- [ ] **Step 3: Commit the change**

```bash
git add mc-stress/main.go docs/superpowers/specs/2026-04-26-prelogin-dropped-conns-design.md
git commit -m "fix: increment droppedConns on read error in pre-login mode"
```

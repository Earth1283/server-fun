# Design Spec: Increment droppedConns on prelogin read error

## 1. Overview
In `mc-stress/main.go`, the `worker` function handles `prelogin` spam mode. Currently, when `har` (hit-and-run) is disabled, it calls `readPacket` to wait for a server response but ignores any potential error. To improve the accuracy of the `Dropped` metric, we should increment the `droppedConns` counter if this read fails.

## 2. Proposed Changes
### 2.1 worker function update
In the `prelogin` block of the `worker` function:
- Capture the error from `readPacket(conn, false)`.
- If an error occurs:
    - Increment `droppedConns` atomic counter.
    - If `verbose` mode is enabled, log the error to `os.Stderr`.

## 3. Implementation Details
The change will be applied to `mc-stress/main.go`.

```go
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
            // ...
```

## 4. Verification Plan
### 4.1 Automated Tests
The existing tests in `main_test.go` are minimal and focus on VarInt encoding. Adding a test for the `worker` loop is complex due to network dependencies.
We will rely on manual verification and ensuring the code compiles.

### 4.2 Manual Verification
- Run `go build` to ensure no syntax errors.
- (In a real scenario) Run the stresser against a server that drops connections during prelogin and observe the "Dropped" counter in the reporter.

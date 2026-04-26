# Design Spec: Pre-Login Spam Mode

## Overview
A new mode for `mc-stress` to trigger the `AsyncPlayerPreLoginEvent` at a high frequency. This is intended to test for memory leaks or performance bottlenecks in Minecraft server plugins (specifically anti-bot plugins) that hook into this event.

## Problem Statement
Standard `mc-stress` behavior focuses on holding connections open to saturate the heap. However, some leaks occur during the initial login handshake phase. Testing these requires a high volume of connection attempts rather than long-lived connections.

## Proposed Changes

### CLI Flags
- `--prelogin`: Boolean flag to enable the pre-login spam mode.
- `--har` (Hit-and-Run): Boolean flag, active only when `--prelogin` is set. When enabled, the tool will not wait for a server response after sending the `Login Start` packet.

### Worker Implementation
The `worker` function will be modified to include a branch for `prelogin` mode:
1. Dial target.
2. Send Handshake (state=2).
3. Send Login Start (random username).
4. If `--har` is false:
    - Wait for one packet from the server (e.g., `Encryption Request`).
5. Close connection.
6. Increment `newConns` and `activeConns` (briefly, then decrement) or just track via `newConns`.

### Metrics and Reporting
- `New/s` will show the rate of events triggered per second.
- `Dropped` will show failed connection or handshake attempts.

## Success Criteria
- The tool can trigger thousands of `AsyncPlayerPreLoginEvent` calls per second (limited by CPU/Network).
- The user can toggle between waiting for a response (reliable) and immediate closure (fast).
- The feature is documented in the README.

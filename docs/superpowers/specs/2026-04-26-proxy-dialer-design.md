# Spec: Proxy-Aware Dialer

Implement a proxy-aware dialer for `mc-stress` to support SOCKS5 proxies.

## 1. Goal
Integrate SOCKS5 proxy support into the connection logic of `mc-stress`. If proxies are configured, each new connection should use a random proxy from the pool.

## 2. Architecture
- **Proxy Pool:** A global slice `proxyPool []string` (already exists).
- **Dialer Factory:** A new function `getDialer()` that returns a `proxy.Dialer`.
- **Integration:** Update `worker` and `debugRun` to use the dialer returned by `getDialer()`.

## 3. Components

### 3.1 `getDialer() (proxy.Dialer, error)`
- Base dialer: `net.Dialer` with 10s timeout.
- If `proxyPool` is empty: return the base dialer.
- If `proxyPool` is NOT empty:
  - Pick a random address from `proxyPool`.
  - Return a SOCKS5 dialer using `proxy.SOCKS5`.

### 3.2 `worker` function
- Call `getDialer()` inside the loop.
- Handle error by logging and sleeping.
- Use `dialer.Dial` instead of `net.DialTimeout`.

### 3.3 `debugRun` function
- Call `getDialer()` once.
- Use `dialer.Dial` instead of `net.DialTimeout`.

## 4. Error Handling
- In `worker`: Use `fmt.Fprintf(os.Stderr, ...)` if `verbose` is true.
- In `debugRun`: Use `dbgErr`.

## 5. Testing
- Compile the project using `go build -o gaslighter .`.
- (Manual) Run with a proxy list to verify it still works.

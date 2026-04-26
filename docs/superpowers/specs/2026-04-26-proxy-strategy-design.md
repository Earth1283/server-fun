# Spec: Proxy Strategy Configuration

## 1. Goal
Add support for configurable proxy selection strategies (random and round-robin) in `mc-stress`.

## 2. Architecture
- **Global State:** Add `proxyCounter atomic.Uint64` to track the current index for round-robin selection.
- **Configuration:** Add a new CLI flag/config key `proxy-strategy` (default: "random").
- **Dialer Logic:** Update `getDialer()` to select a proxy from `proxyPool` based on the configured strategy.

## 3. Implementation Details

### 3.1 Global Variables
Update the `var` block in `mc-stress/main.go`:
```go
var (
    activeConns  atomic.Int64
    bytesSent    atomic.Int64
    droppedConns atomic.Int64
    newConns     atomic.Int64
    proxyCounter atomic.Uint64 // New variable
    // ...
)
```

### 3.2 Configuration (init)
Update `init()` in `mc-stress/main.go`:
```go
func init() {
    initConfig()
    f := rootCmd.Flags()
    // ...
    f.String("proxy-strategy", "random", "proxy selection strategy: random or round-robin")
    viper.BindPFlags(f)
}
```

### 3.3 Proxy Selection (getDialer)
Update `getDialer()` in `mc-stress/main.go`:
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

## 4. Documentation
Update `README.rst` to include the new flag in the `Usage` and `Configuration File` sections.

## 5. Verification
- Run `go build -o gaslighter .` in `mc-stress` to ensure it compiles.

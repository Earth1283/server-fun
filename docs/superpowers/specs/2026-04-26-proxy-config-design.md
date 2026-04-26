# Design Spec: Proxy and Configuration File Support

## Overview
Add proxy support and a TOML-based configuration file system to the `mc-stress` (gaslighter) tool.

## Requirements
- **Proxy Support**: 
  - New flag `-p` / `--proxies` for a `.txt` file with SOCKS5 proxies.
  - SOCKS5 proxies are newline-separated in the format `host:port`.
  - Connections should pick a proxy at random from the list.
- **Configuration File**:
  - Filename: `gaslighterc.toml`.
  - Locations: User's home directory and current working directory.
  - Merging: Current directory overrides home directory; CLI flags override both.
- **Documentation**:
  - Update `README.rst` with the new flag and config file details.

## Architecture

### 1. Configuration Management (`viper`)
Use `github.com/spf13/viper` to manage configurations. 
- Initialize Viper to look for `gaslighterc.toml`.
- Add search paths: `$HOME` and `.`.
- Bind existing Cobra flags to Viper keys so that config values are automatically used if flags are absent.

### 2. Proxy Management
- **Proxy Pool**: A global slice of SOCKS5 addresses loaded once at startup.
- **Dialing**: Use `golang.org/x/net/proxy` to create a SOCKS5 dialer. If no proxies are available or specified, fall back to `net.Dialer`.

### 3. Worker Integration
Update the `worker` function to accept a proxy list (or a dialer factory). Each iteration of the worker's loop will:
1. Pick a random proxy from the pool (if enabled).
2. Dial the target via the proxy.

## Data Flow
1. **Startup**: 
   - Viper loads `~/gaslighterc.toml`.
   - Viper loads `./gaslighterc.toml` and merges.
   - CLI flags are parsed and override config values.
2. **Execution**:
   - If `--proxies` is provided, load the list into memory.
   - Spawn workers, passing the proxy pool.
   - Each worker dials using a random proxy from the pool.

## Testing Strategy
- **Unit Tests**: Add tests for proxy list parsing.
- **Manual Verification**:
  - Run with a mock SOCKS5 proxy to verify routing.
  - Verify that `gaslighterc.toml` values are correctly respected.
  - Verify that CLI flags override TOML values.

#!/bin/bash

# mc-stress setup script for high-frequency spamming
# Optimized for Linux hosts to bypass networking bottlenecks

set -e

# ANSI colors
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m' # No Color

echo -e "${GREEN}==> Initializing mc-stress setup for high-frequency spamming${NC}"

# 1. Check for root privileges
if [[ $EUID -ne 0 ]]; then
   echo -e "${RED}Error: This script must be run as root (or with sudo) to apply system optimizations.${NC}"
   exit 1
fi

# 2. Check for Go 1.22+
if ! command -v go &> /dev/null; then
    echo -e "${YELLOW}Warning: Go is not installed.${NC}"
    echo "Please install Go 1.22 or later: https://go.dev/doc/install"
    exit 1
fi

GO_VERSION=$(go version | awk '{print $3}' | sed 's/go//')
MAJOR=$(echo $GO_VERSION | cut -d. -f1)
MINOR=$(echo $GO_VERSION | cut -d. -f2)

if [ "$MAJOR" -lt 1 ] || ([ "$MAJOR" -eq 1 ] && [ "$MINOR" -lt 22 ]); then
    echo -e "${RED}Error: Go version 1.22+ is required. Found: $GO_VERSION${NC}"
    exit 1
fi

# 3. Build the tool
echo -e "${GREEN}==> Building mc-stress (gaslighter)...${NC}"
cd "$(dirname "$0")/mc-stress"
go build -o ../gaslighter .
cd ..

# 4. Apply OS Optimizations
echo -e "${GREEN}==> Applying OS optimizations for high-frequency networking...${NC}"

# File descriptor limit (per-process)
ulimit -n 131072
echo "  [OK] ulimit -n 131072 (File descriptor limit)"

# System-wide file limit
sysctl -w fs.file-max=2097152
echo "  [OK] fs.file-max=2097152"

# Ephemeral port range
sysctl -w net.ipv4.ip_local_port_range="1024 65535"
echo "  [OK] net.ipv4.ip_local_port_range=\"1024 65535\""

# TCP TIME_WAIT recycling
sysctl -w net.ipv4.tcp_tw_reuse=1
echo "  [OK] net.ipv4.tcp_tw_reuse=1"

# Socket buffer sizes (optimized for high count, small payload)
sysctl -w net.core.rmem_default=4096
sysctl -w net.core.wmem_default=4096
echo "  [OK] Socket buffer sizes tuned for high concurrency"

echo -e "\n${GREEN}==> Setup Complete!${NC}"
echo -e "${YELLOW}Note: System optimizations (sysctl) are TEMPORARY and will reset after reboot.${NC}"
echo "To make them permanent, add them to /etc/sysctl.conf"

echo -e "\n${GREEN}Usage for Pre-Login Spam:${NC}"
echo "  ./gaslighter <ip:port> --prelogin"
echo -e "\n${GREEN}Usage for Maximum Throughput (Hit-and-Run):${NC}"
echo "  ./gaslighter <ip:port> --prelogin --har -w 20000"

chmod +x gaslighter 2>/dev/null || true

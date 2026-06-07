package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net"
	"strconv"
	"sync/atomic"
	"time"

	"golang.org/x/net/proxy"
)

// ---------------------------------------------------------------------------
// Proxy / dialer
// ---------------------------------------------------------------------------

var (
	proxyPool     []string
	proxyStrategy = "random"
	proxyCounter  atomic.Uint64
)

func getDialer() (proxy.Dialer, error) {
	base := &net.Dialer{Timeout: 10 * time.Second}
	if len(proxyPool) == 0 {
		return base, nil
	}
	var addr string
	if proxyStrategy == "round-robin" {
		idx := (proxyCounter.Add(1) - 1) % uint64(len(proxyPool))
		addr = proxyPool[idx]
	} else {
		addr = proxyPool[rand.Intn(len(proxyPool))]
	}
	return proxy.SOCKS5("tcp", addr, nil, base)
}

// ---------------------------------------------------------------------------
// VarInt / packet helpers
// ---------------------------------------------------------------------------

func writeVarInt(buf []byte, v int) []byte {
	for {
		b := byte(v & 0x7F)
		v >>= 7
		if v != 0 {
			b |= 0x80
		}
		buf = append(buf, b)
		if v == 0 {
			break
		}
	}
	return buf
}

func writeString(buf []byte, s string) []byte {
	raw := []byte(s)
	buf = writeVarInt(buf, len(raw))
	return append(buf, raw...)
}

func readVarInt(r io.Reader) (int, error) {
	var v int
	for shift := uint(0); shift < 35; shift += 7 {
		var b [1]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return 0, err
		}
		v |= int(b[0]&0x7F) << shift
		if b[0]&0x80 == 0 {
			return v, nil
		}
	}
	return 0, fmt.Errorf("VarInt too large")
}

func decodeVarInt(buf []byte) (int, int) {
	var v int
	for i, b := range buf {
		v |= int(b&0x7F) << (7 * uint(i))
		if b&0x80 == 0 {
			return v, i + 1
		}
	}
	return v, len(buf)
}

func buildPacket(id int, payload []byte) []byte {
	var data []byte
	data = writeVarInt(data, id)
	data = append(data, payload...)
	var out []byte
	out = writeVarInt(out, len(data))
	return append(out, data...)
}

func readPacket(r io.Reader) (int, []byte, error) {
	length, err := readVarInt(r)
	if err != nil {
		return 0, nil, err
	}
	if length <= 0 || length > 1<<21 {
		return 0, nil, fmt.Errorf("bad packet length %d", length)
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(r, buf); err != nil {
		return 0, nil, err
	}
	id, n := decodeVarInt(buf)
	return id, buf[n:], nil
}

func buildHandshake(host string, port uint16, nextState int) []byte {
	var p []byte
	p = writeVarInt(p, 0x00)
	p = writeVarInt(p, 767) // protocol version (1.21.x); SLP is version-agnostic
	p = writeString(p, host)
	pb := make([]byte, 2)
	binary.BigEndian.PutUint16(pb, port)
	p = append(p, pb...)
	p = writeVarInt(p, nextState)
	var out []byte
	out = writeVarInt(out, len(p))
	return append(out, p...)
}

// ---------------------------------------------------------------------------
// SLP
// ---------------------------------------------------------------------------

type slpResponse struct {
	Version struct {
		Name     string `json:"name"`
		Protocol int    `json:"protocol"`
	} `json:"version"`
	Players struct {
		Max    int `json:"max"`
		Online int `json:"online"`
	} `json:"players"`
	Description interface{} `json:"description"`
}

func (r *slpResponse) motd() string {
	switch v := r.Description.(type) {
	case string:
		return v
	case map[string]interface{}:
		if t, ok := v["text"].(string); ok && t != "" {
			return t
		}
		if extra, ok := v["extra"].([]interface{}); ok {
			var s string
			for _, e := range extra {
				switch ev := e.(type) {
				case string:
					s += ev
				case map[string]interface{}:
					if t, ok := ev["text"].(string); ok {
						s += t
					}
				}
			}
			return s
		}
	}
	return ""
}

// ping performs a full SLP exchange and returns the parsed status plus the
// round-trip latency measured via the canonical Ping/Pong (0x01) packet.
func ping(target string, timeout time.Duration) (*slpResponse, float64, error) {
	dialer, err := getDialer()
	if err != nil {
		return nil, 0, err
	}
	conn, err := dialer.Dial("tcp", target)
	if err != nil {
		return nil, 0, err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(timeout))

	host, portStr, _ := net.SplitHostPort(target)
	port, _ := strconv.Atoi(portStr)

	// Handshake (state 1) + Status Request.
	if _, err := conn.Write(buildHandshake(host, uint16(port), 1)); err != nil {
		return nil, 0, err
	}
	if _, err := conn.Write(buildPacket(0x00, nil)); err != nil {
		return nil, 0, err
	}

	id, payload, err := readPacket(conn)
	if err != nil {
		return nil, 0, err
	}
	if id != 0x00 {
		return nil, 0, fmt.Errorf("unexpected status packet 0x%02X", id)
	}
	strLen, n := decodeVarInt(payload)
	if n+strLen > len(payload) {
		return nil, 0, fmt.Errorf("truncated SLP JSON")
	}
	var resp slpResponse
	if err := json.Unmarshal(payload[n:n+strLen], &resp); err != nil {
		return nil, 0, err
	}

	// Ping/Pong for true latency.
	token := time.Now().UnixNano()
	pl := make([]byte, 8)
	binary.BigEndian.PutUint64(pl, uint64(token))
	start := time.Now()
	if _, err := conn.Write(buildPacket(0x01, pl)); err != nil {
		// Status arrived fine; report it without a latency reading.
		return &resp, float64(time.Since(start).Microseconds()) / 1000, nil
	}
	if _, _, err := readPacket(conn); err != nil {
		return &resp, float64(time.Since(start).Microseconds()) / 1000, nil
	}
	latency := float64(time.Since(start).Microseconds()) / 1000
	return &resp, latency, nil
}

// resolveTarget applies SRV resolution and a default port when none is given.
func resolveTarget(input string) string {
	if _, _, err := net.SplitHostPort(input); err == nil {
		return input // already host:port
	}
	if _, addrs, err := net.LookupSRV("minecraft", "tcp", input); err == nil && len(addrs) > 0 {
		host := addrs[0].Target
		host = host[:len(host)-1] // strip trailing dot
		return net.JoinHostPort(host, strconv.Itoa(int(addrs[0].Port)))
	}
	return net.JoinHostPort(input, "25565")
}

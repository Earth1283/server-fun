package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"regexp"
	"strings"
	"time"
)

type PlayerSample struct {
	Name string `json:"name"`
	ID   string `json:"id"`
}

type SLPResponse struct {
	Version struct {
		Name     string `json:"name"`
		Protocol int    `json:"protocol"`
	} `json:"version"`
	Players struct {
		Max    int            `json:"max"`
		Online int            `json:"online"`
		Sample []PlayerSample `json:"sample"`
	} `json:"players"`
	Description interface{} `json:"description"`
	Favicon     string      `json:"favicon"`
	RTT         time.Duration `json:"rtt,omitempty"`
}

var mcColorRegex = regexp.MustCompile(`(?i)§[0-9A-FK-OR]`)

// StripMCFormatting removes Minecraft section-sign color codes from a string.
func StripMCFormatting(s string) string {
	return mcColorRegex.ReplaceAllString(s, "")
}

func (r *SLPResponse) MOTD() string {
	raw := parseDescription(r.Description)
	return strings.TrimSpace(StripMCFormatting(raw))
}

func parseDescription(desc interface{}) string {
	switch v := desc.(type) {
	case string:
		return v
	case map[string]interface{}:
		var sb strings.Builder
		if text, ok := v["text"].(string); ok {
			sb.WriteString(text)
		}
		if extra, ok := v["extra"].([]interface{}); ok {
			for _, e := range extra {
				sb.WriteString(parseDescription(e))
			}
		}
		return sb.String()
	}
	return "Unknown"
}

func doSLP(host string, port uint16, protocolVersion int) (*SLPResponse, error) {
	target := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	dialer, err := getDialer()
	if err != nil {
		return nil, err
	}

	conn, err := dialer.Dial("tcp", target)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	// 1. Handshake (state 1 - status)
	hs := buildHandshake(host, port, 1, protocolVersion)
	if _, err := conn.Write(hs); err != nil {
		return nil, fmt.Errorf("failed to send handshake: %w", err)
	}

	// 2. Status Request
	req := buildPacket(0x00, nil)
	if _, err := conn.Write(req); err != nil {
		return nil, fmt.Errorf("failed to send status request: %w", err)
	}

	// 3. Status Response
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	id, payload, err := readPacket(conn, false)
	if err != nil {
		return nil, fmt.Errorf("failed to read status response: %w", err)
	}
	if id != 0x00 {
		return nil, fmt.Errorf("unexpected SLP response packet ID 0x%02X", id)
	}

	strLen, n := decodeVarInt(payload)
	if n+strLen > len(payload) {
		return nil, fmt.Errorf("truncated SLP JSON")
	}

	var resp SLPResponse
	if err := json.Unmarshal(payload[n:n+strLen], &resp); err != nil {
		return nil, err
	}

	// 4. Ping/Pong RTT Measurement
	start := time.Now()
	pingPayload := make([]byte, 8)
	binary.BigEndian.PutUint64(pingPayload, uint64(start.UnixNano()))
	pingPacket := buildPacket(0x01, pingPayload)

	if _, err := conn.Write(pingPacket); err == nil {
		conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		pID, pData, pErr := readPacket(conn, false)
		if pErr == nil && pID == 0x01 && len(pData) >= 8 {
			resp.RTT = time.Since(start)
		}
	}

	return &resp, nil
}

func buildHandshake(host string, port uint16, nextState int, protocolVersion int) []byte {
	var payload []byte
	payload = writeVarInt(payload, 0x00)            // Packet ID
	payload = writeVarInt(payload, protocolVersion) // Protocol version
	payload = writeString(payload, host)
	portBuf := make([]byte, 2)
	binary.BigEndian.PutUint16(portBuf, port)
	payload = append(payload, portBuf...)
	payload = writeVarInt(payload, nextState)
	var out []byte
	out = writeVarInt(out, len(payload))
	return append(out, payload...)
}


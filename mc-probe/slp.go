package main

import (
	"encoding/json"
	"fmt"
	"net"
	"time"
)

type SLPResponse struct {
	Version struct {
		Name     string `json: "name"`
		Protocol int    `json: "protocol"`
	} `json: "version"`
	Players struct {
		Max    int `json: "max"`
		Online int    `json: "online"`
		Sample []struct {
			Name string `json: "name"`
			ID   string `json: "id"`
		} `json: "sample"`
	} `json: "players"`
	Description interface{} `json: "description"`
	Favicon     string      `json: "favicon"`
}

func (r *SLPResponse) MOTD() string {
	switch v := r.Description.(type) {
	case string:
		return v
	case map[string]interface{}:
		if text, ok := v["text"].(string); ok {
			return text
		}
		if extra, ok := v["extra"].([]interface{}); ok {
			var motd string
			for _, e := range extra {
				if m, ok := e.(map[string]interface{}); ok {
					if t, ok := m["text"].(string); ok {
						motd += t
					}
				} else if s, ok := e.(string); ok {
					motd += s
				}
			}
			return motd
		}
	}
	return "Unknown"
}

func doSLP(target string) (*SLPResponse, error) {
	dialer, err := getDialer()
	if err != nil {
		return nil, err
	}

	conn, err := dialer.Dial("tcp", target)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	// 1. Handshake (state 1)
	host, _, _ := net.SplitHostPort(target)
	hs := buildHandshake(host, 25565, 1) // SLP is state 1
	conn.Write(hs)

	// 2. Status Request
	req := buildPacket(0x00, nil)
	conn.Write(req)

	// 3. Status Response
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	id, payload, err := readPacket(conn, false)
	if err != nil {
		return nil, err
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

	return &resp, nil
}

func buildHandshake(host string, port uint16, nextState int) []byte {
	var payload []byte
	payload = writeVarInt(payload, 0x00) // Packet ID
	payload = writeVarInt(payload, 767)  // Protocol version
	payload = writeString(payload, host)
	portBuf := make([]byte, 2)
	portBuf[0] = byte(port >> 8)
	portBuf[1] = byte(port)
	payload = append(payload, portBuf...)
	payload = writeVarInt(payload, nextState)
	var out []byte
	out = writeVarInt(out, len(payload))
	return append(out, payload...)
}

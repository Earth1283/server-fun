package main

import (
	"encoding/binary"
	"testing"
)

func TestVarIntEncodingDecoding(t *testing.T) {
	tests := []int{0, 1, 127, 128, 255, 2097151, 2147483647}
	for _, expected := range tests {
		var buf []byte
		buf = writeVarInt(buf, expected)
		val, bytesRead := decodeVarInt(buf)
		if val != expected {
			t.Errorf("expected VarInt %d, got %d", expected, val)
		}
		if bytesRead != len(buf) {
			t.Errorf("expected bytesRead %d, got %d", len(buf), bytesRead)
		}
	}
}

func TestStripMCFormatting(t *testing.T) {
	raw := "§aHello §lWorld! §r§c[Test]§r"
	expected := "Hello World! [Test]"
	result := StripMCFormatting(raw)
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestParseDescription(t *testing.T) {
	descMap := map[string]interface{}{
		"text": "Welcome to ",
		"extra": []interface{}{
			map[string]interface{}{"text": "Server!"},
		},
	}
	res := parseDescription(descMap)
	if res != "Welcome to Server!" {
		t.Errorf("expected %q, got %q", "Welcome to Server!", res)
	}
}

func TestBuildHandshake(t *testing.T) {
	host := "localhost"
	var port uint16 = 25565
	nextState := 1
	protocolVersion := 767

	hs := buildHandshake(host, port, nextState, protocolVersion)
	if len(hs) == 0 {
		t.Fatalf("expected non-empty handshake packet")
	}

	// First byte(s) are packet length
	pktLen, n := decodeVarInt(hs)
	if pktLen != len(hs)-n {
		t.Errorf("expected packet payload length %d, got %d", len(hs)-n, pktLen)
	}

	payload := hs[n:]
	packetID, m := decodeVarInt(payload)
	if packetID != 0x00 {
		t.Errorf("expected handshake packet ID 0x00, got 0x%02X", packetID)
	}

	pos := m
	pVer, k := decodeVarInt(payload[pos:])
	if pVer != protocolVersion {
		t.Errorf("expected protocol version %d, got %d", protocolVersion, pVer)
	}
	pos += k

	hLen, j := decodeVarInt(payload[pos:])
	pos += j
	readHost := string(payload[pos : pos+hLen])
	if readHost != host {
		t.Errorf("expected host %q, got %q", host, readHost)
	}
	pos += hLen

	readPort := binary.BigEndian.Uint16(payload[pos : pos+2])
	if readPort != port {
		t.Errorf("expected port %d, got %d", port, readPort)
	}
	pos += 2

	state, _ := decodeVarInt(payload[pos:])
	if state != nextState {
		t.Errorf("expected next state %d, got %d", nextState, state)
	}
}

func TestResolveTarget(t *testing.T) {
	// IP with explicit port
	host, port, srv, err := resolveTarget("127.0.0.1:25566")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if host != "127.0.0.1" || port != 25566 || srv != "" {
		t.Errorf("unexpected resolveTarget result: host=%s port=%d srv=%s", host, port, srv)
	}

	// Plain IP
	host, port, srv, err = resolveTarget("192.168.1.1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if host != "192.168.1.1" || port != 25565 || srv != "" {
		t.Errorf("unexpected resolveTarget result: host=%s port=%d srv=%s", host, port, srv)
	}
}

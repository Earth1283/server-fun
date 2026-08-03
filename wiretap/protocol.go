package main

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"math/rand"
	"net"
	"strings"
	"time"
)

type ProbeResult struct {
	OnlineMode       bool     `json:"online_mode"`
	Compression      int      `json:"compression"`
	RSAKeySize       int      `json:"rsa_key_size_bits,omitempty"`
	ShouldAuth       bool     `json:"should_auth,omitempty"`
	DisconnectReason string   `json:"disconnect_reason,omitempty"`
	ProxyEnforced    bool     `json:"proxy_enforced"`
	ProxyChannel     string   `json:"proxy_channel,omitempty"`
	ServerFingerprint string  `json:"server_fingerprint,omitempty"`
}

func doProbe(host string, port uint16, protocolVersion int) (*ProbeResult, error) {
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

	// 1. Handshake (state 2 - login)
	hs := buildHandshake(host, port, 2, protocolVersion)
	if _, err := conn.Write(hs); err != nil {
		return nil, fmt.Errorf("failed to write handshake: %w", err)
	}

	// 2. Login Start
	name := fmt.Sprintf("Probe_%d", rand.Intn(10000))
	ls := buildLoginStart(name)
	if _, err := conn.Write(ls); err != nil {
		return nil, fmt.Errorf("failed to write login start: %w", err)
	}

	res := &ProbeResult{Compression: -1}
	compressed := false

	for {
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		id, payload, err := readPacket(conn, compressed)
		if err != nil {
			// Timeout or EOF; return what we collected so far
			break
		}

		switch id {
		case 0x00: // Disconnect (Login)
			msg := parseDisconnectPayload(payload)
			res.DisconnectReason = StripMCFormatting(msg)
			res.inferFingerprint()
			return res, nil

		case 0x01: // Encryption Request
			res.OnlineMode = true
			_, pubKeyDER, _, shouldAuth, err := parseEncryptionRequest(payload)
			res.ShouldAuth = shouldAuth
			if err == nil {
				pubAny, err := x509.ParsePKIXPublicKey(pubKeyDER)
				if err == nil {
					if pubKey, ok := pubAny.(*rsa.PublicKey); ok {
						res.RSAKeySize = pubKey.N.BitLen()
					}
				}
			}
			res.inferFingerprint()
			return res, nil // Online mode confirmed, cannot proceed without auth token

		case 0x02: // Login Success
			res.OnlineMode = false
			res.inferFingerprint()
			return res, nil // Offline mode confirmed

		case 0x03: // Set Compression
			threshold, _ := decodeVarInt(payload)
			res.Compression = threshold
			compressed = true

		case 0x04: // Login Plugin Request (Velocity / BungeeGuard / Modern Forwarding)
			res.ProxyEnforced = true
			_, channel, _ := parseLoginPluginRequest(payload)
			res.ProxyChannel = channel
			res.inferFingerprint()
			return res, nil
		}
	}

	res.inferFingerprint()
	return res, nil
}

func (p *ProbeResult) inferFingerprint() {
	if p.ProxyChannel != "" {
		if strings.Contains(p.ProxyChannel, "velocity") {
			p.ServerFingerprint = "Velocity Proxy (Modern Forwarding Enforced)"
			return
		}
		if strings.Contains(p.ProxyChannel, "bungeeguard") {
			p.ServerFingerprint = "BungeeCord / WaterFall (BungeeGuard Token Enforced)"
			return
		}
		p.ServerFingerprint = fmt.Sprintf("Proxy Enforced (%s)", p.ProxyChannel)
		return
	}

	if p.DisconnectReason != "" {
		reason := strings.ToLower(p.DisconnectReason)
		if strings.Contains(reason, "velocity") {
			p.ServerFingerprint = "Velocity Proxy"
			return
		}
		if strings.Contains(reason, "bungeecord") || strings.Contains(reason, "bungee") {
			p.ServerFingerprint = "BungeeCord / WaterFall Proxy"
			return
		}
		if strings.Contains(reason, "outdated server") || strings.Contains(reason, "outdated client") {
			p.ServerFingerprint = "Minecraft Server (Version Mismatch)"
			return
		}
	}

	if p.OnlineMode {
		p.ServerFingerprint = "Online Mode (Mojang/Microsoft Authenticated)"
	} else if p.Compression >= 0 {
		p.ServerFingerprint = "Offline Mode (Unauthenticated, Compression Active)"
	} else {
		p.ServerFingerprint = "Offline Mode (Unauthenticated)"
	}
}

func parseDisconnectPayload(payload []byte) string {
	msgLen, n := decodeVarInt(payload)
	if n+msgLen <= len(payload) {
		raw := payload[n : n+msgLen]
		var desc interface{}
		if err := json.Unmarshal(raw, &desc); err == nil {
			return parseDescription(desc)
		}
		return string(raw)
	}
	return string(payload)
}

func parseLoginPluginRequest(payload []byte) (messageID int, channel string, err error) {
	msgID, n := decodeVarInt(payload)
	if n >= len(payload) {
		return 0, "", fmt.Errorf("truncated login plugin request")
	}
	chLen, m := decodeVarInt(payload[n:])
	pos := n + m
	if pos+chLen > len(payload) {
		return msgID, "", fmt.Errorf("truncated channel name")
	}
	channel = string(payload[pos : pos+chLen])
	return msgID, channel, nil
}

func buildLoginStart(name string) []byte {
	var payload []byte
	payload = writeString(payload, name)
	payload = append(payload, make([]byte, 16)...) // UUID 0
	return buildPacket(0x00, payload)
}

func parseEncryptionRequest(payload []byte) (serverID string, pubKeyDER, verifyToken []byte, shouldAuth bool, err error) {
	idLen, n := decodeVarInt(payload)
	if n+idLen > len(payload) {
		return "", nil, nil, false, fmt.Errorf("truncated server ID")
	}
	serverID = string(payload[n : n+idLen])
	pos := n + idLen

	pkLen, n := decodeVarInt(payload[pos:])
	pos += n
	if pos+pkLen > len(payload) {
		return "", nil, nil, false, fmt.Errorf("truncated public key")
	}
	pubKeyDER = payload[pos : pos+pkLen]
	pos += pkLen

	vtLen, n := decodeVarInt(payload[pos:])
	pos += n
	if pos+vtLen > len(payload) {
		return "", nil, nil, false, fmt.Errorf("truncated verify token")
	}
	verifyToken = payload[pos : pos+vtLen]
	pos += vtLen

	if pos < len(payload) {
		shouldAuth = payload[pos] != 0
	}
	return
}


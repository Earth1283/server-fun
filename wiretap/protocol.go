package main

import (
	"crypto/rsa"
	"crypto/x509"
	"fmt"
	"math/rand"
	"net"
	"time"
)

type ProbeResult struct {
	OnlineMode  bool
	Compression int
	RSAKeySize  int
	Registry    []string
}

func doProbe(target string) (*ProbeResult, error) {
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
	host, portStr, _ := net.SplitHostPort(target)
	var port uint16
	fmt.Sscanf(portStr, "%d", &port)
	hs := buildHandshake(host, port, 2)
	conn.Write(hs)

	// 2. Login Start
	name := fmt.Sprintf("Probe_%d", rand.Intn(10000))
	ls := buildLoginStart(name)
	conn.Write(ls)

	res := &ProbeResult{Compression: -1}
	compressed := false

	for {
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		id, payload, err := readPacket(conn, compressed)
		if err != nil {
			return res, nil // Assume EOF or timeout means we got what we could
		}

		switch id {
		case 0x01: // Encryption Request
			res.OnlineMode = true
			_, pubKeyDER, _, _, err := parseEncryptionRequest(payload)
			if err == nil {
				pubAny, err := x509.ParsePKIXPublicKey(pubKeyDER)
				if err == nil {
					if pubKey, ok := pubAny.(*rsa.PublicKey); ok {
						res.RSAKeySize = pubKey.N.BitLen()
					}
				}
			}
			return res, nil // Online mode confirmed, can't proceed without auth

		case 0x03: // Set Compression
			threshold, _ := decodeVarInt(payload)
			res.Compression = threshold
			compressed = true

		case 0x02: // Login Success
			res.OnlineMode = false
			return res, nil // Offline mode confirmed

		case 0x00: // Disconnect
			return res, nil
		}
	}
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

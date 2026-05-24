package main

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"crypto/aes"
	"crypto/cipher"
	cryptorand "crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/binary"
	"fmt"
	"io"
	"math/rand"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/net/proxy"
)

// ---------------------------------------------------------------------------
// ANSI colours (verbatim from gaslighter)
// ---------------------------------------------------------------------------

const (
	cReset      = "\033[0m"
	cDim        = "\033[2m"
	cBoldGreen  = "\033[1;32m"
	cBoldCyan   = "\033[1;36m"
	cBoldRed    = "\033[1;31m"
	cBoldYellow = "\033[1;33m"
	cGreen      = "\033[32m"
	cCyan       = "\033[36m"
	cGray       = "\033[90m"
)

func ts() string { return fmt.Sprintf("%s%s%s", cDim, time.Now().Format("15:04:05.000"), cReset) }

func dbgSend(id int, name string, pkt []byte) {
	fmt.Printf("%s %s→ SEND%s  %s0x%02X%s  %-34s %s%6d B%s\n",
		ts(), cBoldGreen, cReset, cGreen, id, cReset, name, cGray, len(pkt), cReset)
}

func dbgRecv(id int, name string, payload []byte) {
	preview := ""
	if len(payload) > 0 {
		n := 16
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

// ---------------------------------------------------------------------------
// Proxy pool
// ---------------------------------------------------------------------------

var (
	proxyPool    []string
	proxyCounter atomic.Uint64
	replActive   atomic.Bool // suppresses background packet logs once REPL is running
)

func getDialer() (proxy.Dialer, error) {
	baseDialer := &net.Dialer{Timeout: 10 * time.Second}
	if len(proxyPool) == 0 {
		return baseDialer, nil
	}
	var proxyAddr string
	if viper.GetString("proxy-strategy") == "round-robin" {
		idx := (proxyCounter.Add(1) - 1) % uint64(len(proxyPool))
		proxyAddr = proxyPool[idx]
	} else {
		proxyAddr = proxyPool[rand.Intn(len(proxyPool))]
	}
	return proxy.SOCKS5("tcp", proxyAddr, nil, baseDialer)
}

func loadProxies(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			proxyPool = append(proxyPool, line)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// VarInt / packet helpers (verbatim from gaslighter)
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

func buildPacket(id int, payload []byte, compressed bool) []byte {
	var data []byte
	data = writeVarInt(data, id)
	data = append(data, payload...)

	if compressed {
		var inner []byte
		inner = writeVarInt(inner, 0)
		inner = append(inner, data...)
		var out []byte
		out = writeVarInt(out, len(inner))
		return append(out, inner...)
	}
	var out []byte
	out = writeVarInt(out, len(data))
	return append(out, data...)
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
			zr, err := zlib.NewReader(bytes.NewReader(buf[n:]))
			if err != nil {
				return 0, nil, fmt.Errorf("zlib reader: %v", err)
			}
			defer zr.Close()
			data, err = io.ReadAll(zr)
			if err != nil {
				return 0, nil, fmt.Errorf("zlib read: %v", err)
			}
		} else {
			data = buf[n:]
		}
	}
	id, n := decodeVarInt(data)
	return id, data[n:], nil
}

// ---------------------------------------------------------------------------
// AES/CFB8 encryption (verbatim from gaslighter)
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
// Packet builders
// ---------------------------------------------------------------------------

func randString(n int) string {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = chars[rand.Intn(len(chars))]
	}
	return string(b)
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

func parseEncryptionRequest(payload []byte) (serverID string, pubKeyDER, verifyToken []byte, err error) {
	idLen, n := decodeVarInt(payload)
	if n+idLen > len(payload) {
		return "", nil, nil, fmt.Errorf("truncated server ID")
	}
	serverID = string(payload[n : n+idLen])
	pos := n + idLen

	pkLen, n := decodeVarInt(payload[pos:])
	pos += n
	if pos+pkLen > len(payload) {
		return "", nil, nil, fmt.Errorf("truncated public key")
	}
	pubKeyDER = payload[pos : pos+pkLen]
	pos += pkLen

	vtLen, n := decodeVarInt(payload[pos:])
	pos += n
	if pos+vtLen > len(payload) {
		return "", nil, nil, fmt.Errorf("truncated verify token")
	}
	verifyToken = payload[pos : pos+vtLen]
	return
}

// ---------------------------------------------------------------------------
// Tab-complete packets (new)
// ---------------------------------------------------------------------------

// buildTabComplete sends a Command Suggestions Request (protocol 767 / 1.21.1).
// Serverbound 0x0B — NOT 0x0F, which is Close Container in this protocol version.
func buildTabComplete(txnID int, text string, compressed bool) []byte {
	var payload []byte
	payload = writeVarInt(payload, txnID)
	payload = writeString(payload, text)
	return buildPacket(0x0B, payload, compressed)
}

type tabResponse struct {
	txnID   int
	matches []string
}

func parseTabComplete(payload []byte) (tabResponse, error) {
	if len(payload) == 0 {
		return tabResponse{}, fmt.Errorf("empty payload")
	}
	txnID, n := decodeVarInt(payload)
	pos := n
	if pos+2 > len(payload) {
		return tabResponse{txnID: txnID}, nil
	}

	// start (VarInt) — replacement start
	_, n = decodeVarInt(payload[pos:])
	pos += n
	// length (VarInt) — replacement length
	_, n = decodeVarInt(payload[pos:])
	pos += n

	if pos >= len(payload) {
		return tabResponse{txnID: txnID}, nil
	}
	count, n := decodeVarInt(payload[pos:])
	pos += n

	matches := make([]string, 0, count)
	for i := 0; i < count && pos < len(payload); i++ {
		strLen, n := decodeVarInt(payload[pos:])
		pos += n
		if pos+strLen > len(payload) {
			break
		}
		matches = append(matches, string(payload[pos:pos+strLen]))
		pos += strLen

		// Has Tooltip (boolean)
		if pos >= len(payload) {
			break
		}
		hasTooltip := payload[pos] != 0
		pos++

		if hasTooltip && pos < len(payload) {
			// Tooltip is an NBT Text Component — skip it by reading a string
			tLen, n := decodeVarInt(payload[pos:])
			pos += n
			pos += tLen
		}
	}
	return tabResponse{txnID: txnID, matches: matches}, nil
}

// ---------------------------------------------------------------------------
// Packet name helpers for verbose logging
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
		return fmt.Sprintf("Unknown Login 0x%02X", id)
	}
}

func configSPacketName(id int) string {
	switch id {
	case 0x02:
		return "Disconnect (Config)"
	case 0x03:
		return "Finish Configuration"
	case 0x04:
		return "Keep Alive (Config)"
	case 0x05:
		return "Ping (Config)"
	case 0x07:
		return "Registry Data"
	case 0x09:
		return "Known Packs"
	case 0x0D:
		return "Update Tags (Config)"
	default:
		return fmt.Sprintf("Config 0x%02X", id)
	}
}

func playSPacketName(id int) string {
	switch id {
	case 0x0E:
		return "Command Suggestions Response"
	case 0x1B, 0x1D:
		return "Disconnect (Play)"
	case 0x26:
		return "Keep Alive"
	case 0x28:
		return "Join Game"
	case 0x3C:
		return "Player Position"
	default:
		return fmt.Sprintf("Play 0x%02X", id)
	}
}

// ---------------------------------------------------------------------------
// connectToPlay — full Handshake→Login→Config→Play with verbose logging
// ---------------------------------------------------------------------------

func connectToPlay(target string, port uint16, playerName string) (net.Conn, bool, error) {
	dialer, err := getDialer()
	if err != nil {
		return nil, false, fmt.Errorf("proxy dialer: %w", err)
	}

	conn, err := dialer.Dial("tcp", target)
	if err != nil {
		return nil, false, fmt.Errorf("connect: %w", err)
	}

	// ── Handshake ──────────────────────────────────────────────────────────
	hs := buildHandshake(playerName, port)
	conn.Write(hs)
	dbgSend(0x00, "Handshake", hs)
	dbgInfo("proto=767  host=%s  port=%d  next=Login", playerName, port)

	ls := buildLoginStart(playerName)
	conn.Write(ls)
	dbgSend(0x00, fmt.Sprintf("Login Start  name=%s", playerName), ls)

	dbgState("Handshake", "Login")

	// ── Login state ────────────────────────────────────────────────────────
	compressed := false
	active := net.Conn(conn)

	for {
		conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		id, payload, err := readPacket(active, compressed)
		conn.SetReadDeadline(time.Time{})
		if err != nil {
			conn.Close()
			return nil, false, fmt.Errorf("read (Login): %w", err)
		}

		dbgRecv(id, loginSPacketName(id), payload)

		switch id {
		case 0x00: // Disconnect
			conn.Close()
			return nil, false, fmt.Errorf("server disconnected during Login")

		case 0x03: // Set Compression
			threshold, _ := decodeVarInt(payload)
			compressed = (threshold >= 0)
			dbgInfo("compression enabled  threshold=%d", threshold)

		case 0x01: // Encryption Request
			serverID, pubKeyDER, verifyToken, err := parseEncryptionRequest(payload)
			if err != nil {
				conn.Close()
				return nil, false, fmt.Errorf("parse Encryption Request: %w", err)
			}
			dbgInfo("serverID=%q  pubkey=%d B  verifyToken=%x", serverID, len(pubKeyDER), verifyToken)

			sharedSecret := make([]byte, 16)
			cryptorand.Read(sharedSecret)

			pubAny, err := x509.ParsePKIXPublicKey(pubKeyDER)
			if err != nil {
				conn.Close()
				return nil, false, fmt.Errorf("parse RSA key: %w", err)
			}
			pubKey, ok := pubAny.(*rsa.PublicKey)
			if !ok {
				conn.Close()
				return nil, false, fmt.Errorf("unexpected key type")
			}
			dbgInfo("RSA key: %d bits", pubKey.N.BitLen())

			encSecret, _ := rsa.EncryptPKCS1v15(cryptorand.Reader, pubKey, sharedSecret)
			encToken, _ := rsa.EncryptPKCS1v15(cryptorand.Reader, pubKey, verifyToken)

			resp := buildEncryptionResponse(encSecret, encToken)
			conn.Write(resp)
			dbgSend(0x01, "Encryption Response", resp)

			active = enableEncryption(conn, sharedSecret)
			dbgOK("AES/CFB8 encryption enabled")
			dbgInfo("(online-mode server — will be kicked unless Mojang auth provided)")

		case 0x02: // Login Success
			dbgInfo("UUID+name in payload (%d B)", len(payload))
			ack := buildPacket(0x03, nil, compressed)
			active.Write(ack)
			dbgSend(0x03, "Login Acknowledged", ack)
			dbgState("Login", "Configuration")

			// ── Config state ───────────────────────────────────────────────
			active, err = drainConfigVerbose(active, compressed)
			if err != nil {
				return nil, false, err
			}
			return active, compressed, nil

		case 0x04: // Login Plugin Request — respond with failure
			reqID, n := decodeVarInt(payload)
			_ = n
			resp := buildPacket(0x02, append(writeVarInt(nil, reqID), 0x00), compressed)
			active.Write(resp)
			dbgSend(0x02, "Login Plugin Response (fail)", resp)
		}
	}
}

func drainConfigVerbose(conn net.Conn, compressed bool) (net.Conn, error) {
	for {
		conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		id, data, err := readPacket(conn, compressed)
		conn.SetReadDeadline(time.Time{})
		if err != nil {
			return conn, fmt.Errorf("read (Config): %w", err)
		}

		dbgRecv(id, configSPacketName(id), data)

		switch id {
		case 0x02: // Disconnect
			return conn, fmt.Errorf("server disconnected during Config")

		case 0x03: // Finish Configuration
			ack := buildPacket(0x03, nil, compressed)
			conn.Write(ack)
			dbgSend(0x03, "Acknowledge Configuration", ack)
			dbgState("Configuration", "Play")
			return conn, nil

		case 0x04: // Keep Alive (Config)
			resp := buildPacket(0x04, data, compressed)
			conn.Write(resp)
			dbgSend(0x04, "Keep Alive Response (Config)", resp)

		case 0x05: // Ping (Config)
			resp := buildPacket(0x05, data, compressed)
			conn.Write(resp)
			dbgSend(0x05, "Pong (Config)", resp)

		case 0x09, 0x0E: // Known Packs / Select Known Packs
			resp := buildPacket(0x07, []byte{0x00}, compressed)
			conn.Write(resp)
			dbgSend(0x07, "Known Packs Response (0 packs)", resp)
		}
	}
}

// ---------------------------------------------------------------------------
// Background reader (Play state)
// ---------------------------------------------------------------------------

func startReader(conn net.Conn, compressed bool, tabCh chan<- tabResponse, done chan struct{}) {
	defer close(done)

	for {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		id, data, err := readPacket(conn, compressed)
		conn.SetReadDeadline(time.Time{})
		if err != nil {
			if err.Error() != "EOF" && !replActive.Load() {
				dbgErr("reader", err)
			}
			return
		}

		switch id {
		case 0x26: // Keep Alive — handle silently in REPL mode
			resp := buildPacket(0x18, data, compressed)
			conn.Write(resp)
			if !replActive.Load() {
				dbgRecv(id, "Keep Alive", data)
				dbgSend(0x18, "Keep Alive Response", resp)
			}

		case 0x0E: // Command Suggestions Response (protocol 767) — route to REPL
			resp, err := parseTabComplete(data)
			if err != nil {
				if !replActive.Load() {
					dbgErr("parseTabComplete", err)
				}
				continue
			}
			select {
			case tabCh <- resp:
			default:
			}

		case 0x1B, 0x1D: // Disconnect — always surface this
			fmt.Printf("\n%s[server disconnected]%s\n", cBoldRed, cReset)
			return

		default:
			if !replActive.Load() {
				dbgRecv(id, playSPacketName(id), data)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Scan logic
// ---------------------------------------------------------------------------

var (
	scanCache   map[string][]string
	scanCacheMu sync.Mutex
	txnCounter  int
)

func nextTxn() int {
	txnCounter++
	return txnCounter
}

func sendProbe(conn net.Conn, compressed bool, text string, tabCh <-chan tabResponse, timeout time.Duration) []string {
	txn := nextTxn()
	pkt := buildTabComplete(txn, text, compressed)
	conn.Write(pkt)
	dbgSend(0x0B, fmt.Sprintf("Command Suggestions Request  txn=%d  text=%q", txn, text), pkt)

	select {
	case resp := <-tabCh:
		dbgInfo("tab-complete response  txn=%d  matches=%d", resp.txnID, len(resp.matches))
		return resp.matches
	case <-time.After(timeout):
		dbgInfo("tab-complete timeout  txn=%d", txn)
		return nil
	}
}

func autoScan(conn net.Conn, compressed bool, tabCh <-chan tabResponse, probTimeout time.Duration) map[string][]string {
	scanCacheMu.Lock()
	if scanCache != nil {
		cached := scanCache
		scanCacheMu.Unlock()
		return cached
	}
	scanCacheMu.Unlock()

	fmt.Printf("\n%s[scan]%s sending tab-complete (empty)%s\n", cBoldYellow, cGray, cReset)
	matches := sendProbe(conn, compressed, "", tabCh, probTimeout)

	// Extract unique namespaces from "namespace:command" results
	nsSet := map[string]struct{}{}
	for _, m := range matches {
		if idx := strings.Index(m, ":"); idx > 0 {
			nsSet[m[:idx]] = struct{}{}
		}
	}

	namespaces := make([]string, 0, len(nsSet))
	for ns := range nsSet {
		namespaces = append(namespaces, ns)
	}
	sort.Strings(namespaces)

	if len(namespaces) == 0 {
		fmt.Printf("%s[scan]%s no namespaced commands found — server may not support tab-complete or no plugins installed%s\n", cBoldRed, cGray, cReset)
		return map[string][]string{}
	}

	fmt.Printf("%s[scan]%s found %d plugin namespace(s):%s\n\n", cBoldGreen, cGray, len(namespaces), cReset)

	plugins := map[string][]string{}
	for _, ns := range namespaces {
		probe := ns + ":"
		cmds := sendProbe(conn, compressed, probe, tabCh, probTimeout)
		// Strip namespace prefix from each command
		stripped := make([]string, 0, len(cmds))
		for _, c := range cmds {
			if after, ok := strings.CutPrefix(c, ns+":"); ok {
				stripped = append(stripped, after)
			} else {
				stripped = append(stripped, c)
			}
		}
		plugins[ns] = stripped
		printPluginResult(ns, stripped)
	}

	scanCacheMu.Lock()
	scanCache = plugins
	scanCacheMu.Unlock()

	return plugins
}

func printPluginResult(ns string, cmds []string) {
	fmt.Printf("  %s%-20s%s  %s%d cmd%s%s\n",
		cBoldCyan, ns, cReset,
		cGray, len(cmds), pluralS(len(cmds)), cReset)
	if len(cmds) > 0 {
		fmt.Printf("              %s%s%s\n", cDim, strings.Join(cmds, "  "), cReset)
	}
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// ---------------------------------------------------------------------------
// REPL
// ---------------------------------------------------------------------------

func printMenu() {
	fmt.Printf("\n%sCommands:%s\n", cBoldYellow, cReset)
	fmt.Printf("  %sscan%s              enumerate all plugins via \":\"\n", cGreen, cReset)
	fmt.Printf("  %sprobe <ns>%s        list commands for a plugin namespace\n", cGreen, cReset)
	fmt.Printf("  %s<ns>:%s             shorthand — type \"essentials:\" directly\n", cGreen, cReset)
	fmt.Printf("  %shelp%s              show this menu\n", cGreen, cReset)
	fmt.Printf("  %sexit%s / %squit%s      disconnect\n\n", cGreen, cReset, cGreen, cReset)
}

func runREPL(conn net.Conn, compressed bool, tabCh <-chan tabResponse, probTimeout time.Duration) {
	replActive.Store(true)
	printMenu()
	scanner := bufio.NewScanner(os.Stdin)

	for {
		fmt.Printf("%s>%s ", cBoldGreen, cReset)
		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}

		switch {
		case input == "exit" || input == "quit":
			fmt.Println("Disconnecting.")
			return

		case input == "help":
			printMenu()

		case input == "scan":
			plugins := autoScan(conn, compressed, tabCh, probTimeout)
			fmt.Printf("\n%s[scan complete]%s %d plugin(s) found%s\n\n", cBoldGreen, cGray, len(plugins), cReset)

		case strings.HasPrefix(input, "probe "):
			ns := strings.TrimSpace(strings.TrimPrefix(input, "probe "))
			probeNamespace(conn, compressed, ns, tabCh, probTimeout)

		case strings.HasSuffix(input, ":"):
			ns := strings.TrimSuffix(input, ":")
			probeNamespace(conn, compressed, ns, tabCh, probTimeout)

		case strings.Contains(input, ":"):
			// Raw probe — send as-is (no slash prefix)
			matches := sendProbe(conn, compressed, input, tabCh, probTimeout)
			printMatches(input, matches)

		default:
			fmt.Printf("%s  unknown command — type \"help\" for options%s\n", cGray, cReset)
		}
	}
}

func probeNamespace(conn net.Conn, compressed bool, ns string, tabCh <-chan tabResponse, timeout time.Duration) {
	probe := ns + ":"
	matches := sendProbe(conn, compressed, probe, tabCh, timeout)
	stripped := make([]string, 0, len(matches))
	for _, m := range matches {
		if after, ok := strings.CutPrefix(m, ns+":"); ok {
			stripped = append(stripped, after)
		} else {
			stripped = append(stripped, m)
		}
	}
	printPluginResult(ns, stripped)
	fmt.Println()
}

func printMatches(label string, matches []string) {
	if len(matches) == 0 {
		fmt.Printf("%s  [%s] no matches%s\n", cGray, label, cReset)
		return
	}
	fmt.Printf("  %s[%s]%s  %s\n", cBoldCyan, label, cReset, strings.Join(matches, "  "))
}

// ---------------------------------------------------------------------------
// CLI
// ---------------------------------------------------------------------------

var rootCmd = &cobra.Command{
	Use:   "pluginscanner <ip[:port] | hostname>",
	Short: "Minecraft plugin fingerprinter via tab-complete enumeration",
	Long: `Connects to a Minecraft server, reaches Play state, then uses tab-complete
to enumerate installed plugins and their registered commands via the
Bukkit namespace:command scheme.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		inputTarget := args[0]
		var target string
		var port uint16

		_, portStr, err := net.SplitHostPort(inputTarget)
		if err != nil {
			if net.ParseIP(inputTarget) != nil {
				target = net.JoinHostPort(inputTarget, "25565")
				port = 25565
			} else {
				_, addrs, srvErr := net.LookupSRV("minecraft", "tcp", inputTarget)
				if srvErr == nil && len(addrs) > 0 {
					target = net.JoinHostPort(addrs[0].Target, strconv.Itoa(int(addrs[0].Port)))
					port = addrs[0].Port
					fmt.Printf("%s[SRV]%s %s → %s\n\n", cBoldCyan, cReset, inputTarget, target)
				} else {
					target = net.JoinHostPort(inputTarget, "25565")
					port = 25565
				}
			}
		} else {
			target = inputTarget
			p, err := strconv.ParseUint(portStr, 10, 16)
			if err != nil {
				return fmt.Errorf("invalid port: %w", err)
			}
			port = uint16(p)
		}

		proxiesPath := viper.GetString("proxies")
		if proxiesPath != "" {
			if err := loadProxies(proxiesPath); err != nil {
				return fmt.Errorf("load proxies: %w", err)
			}
			fmt.Printf("%s[proxy]%s loaded %d proxies\n\n", cBoldGreen, cReset, len(proxyPool))
		}

		probTimeout, _ := cmd.Flags().GetDuration("timeout")
		playerName := fmt.Sprintf("Scanner%s", randString(6))

		fmt.Printf("%s[pluginscanner]%s target=%s  player=%s\n\n", cBoldYellow, cReset, target, playerName)

		conn, compressed, err := connectToPlay(target, port, playerName)
		if err != nil {
			dbgErr("connect", err)
			return err
		}
		defer conn.Close()

		dbgOK(fmt.Sprintf("connected as %s%s%s on %s", cBoldCyan, playerName, cReset, target))

		tabCh := make(chan tabResponse, 8)
		done := make(chan struct{})
		go startReader(conn, compressed, tabCh, done)

		runREPL(conn, compressed, tabCh, probTimeout)
		return nil
	},
}

func init() {
	home, _ := os.UserHomeDir()
	viper.AddConfigPath(home)
	viper.AddConfigPath(".")
	viper.SetConfigName("gaslighterc")
	viper.SetConfigType("toml")
	viper.AutomaticEnv()
	viper.ReadInConfig()

	f := rootCmd.Flags()
	f.Duration("timeout", 5*time.Second, "per-probe response timeout")
	f.StringP("proxies", "p", "", "path to .txt file with SOCKS5 proxies")
	f.String("proxy-strategy", "random", "proxy selection: random or round-robin")
	viper.BindPFlags(f)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

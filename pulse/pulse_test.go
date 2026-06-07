package main

import (
	"io"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestVarIntRoundTrip(t *testing.T) {
	for _, v := range []int{0, 1, 127, 128, 255, 300, 25565, 2097151} {
		buf := writeVarInt(nil, v)
		got, n := decodeVarInt(buf)
		if got != v || n != len(buf) {
			t.Errorf("varint %d: got %d (n=%d, len=%d)", v, got, n, len(buf))
		}
	}
}

// mockServer speaks just enough SLP to satisfy ping().
func mockServer(t *testing.T, statusJSON string) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		// Read handshake + status request (length-prefixed).
		for i := 0; i < 2; i++ {
			n, err := readVarInt(conn)
			if err != nil {
				return
			}
			io.CopyN(io.Discard, conn, int64(n))
		}
		// Status response.
		var payload []byte
		payload = writeString(payload, statusJSON)
		conn.Write(buildPacket(0x00, payload))
		// Ping -> Pong echo.
		n, err := readVarInt(conn)
		if err != nil {
			return
		}
		buf := make([]byte, n)
		io.ReadFull(conn, buf)
		conn.Write(buildPacket(0x01, buf[1:])) // strip packet id, echo payload
	}()
	return ln
}

func TestPing(t *testing.T) {
	json := `{"version":{"name":"1.21.4","protocol":769},` +
		`"players":{"max":100,"online":42},"description":{"text":"Hello"}}`
	ln := mockServer(t, json)
	defer ln.Close()

	resp, lat, err := ping(ln.Addr().String(), 3*time.Second)
	if err != nil {
		t.Fatalf("ping: %v", err)
	}
	if resp.Version.Name != "1.21.4" || resp.Players.Online != 42 || resp.Players.Max != 100 {
		t.Errorf("bad parse: %+v", resp)
	}
	if resp.motd() != "Hello" {
		t.Errorf("motd = %q", resp.motd())
	}
	if lat <= 0 {
		t.Errorf("latency = %v, want > 0", lat)
	}
}

func TestPingUnreachable(t *testing.T) {
	if _, _, err := ping("127.0.0.1:1", 500*time.Millisecond); err == nil {
		t.Error("expected error for unreachable target")
	}
}

func TestStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pulse.db")
	s, err := openStore(path, "test:25565")
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	for i := 0; i < 5; i++ {
		if err := s.save(sample{t: time.Now(), ok: true, latency: float64(i * 10), online: i}); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM samples WHERE run = ?`, s.run).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 5 {
		t.Errorf("count = %d, want 5", count)
	}
}

func TestPercentilesAndHistogram(t *testing.T) {
	vals := []float64{10, 20, 30, 40, 50, 60, 70, 80, 90, 100}
	p50, p95, p99 := percentiles(vals)
	if p50 != 50 || p95 != 100 || p99 != 100 {
		t.Errorf("percentiles = %v/%v/%v", p50, p95, p99)
	}
	mn, mx, avg := minMaxAvg(vals)
	if mn != 10 || mx != 100 || avg != 55 {
		t.Errorf("minMaxAvg = %v/%v/%v", mn, mx, avg)
	}
	buckets := histogram(vals, 5)
	total := 0
	for _, b := range buckets {
		total += b.count
	}
	if total != len(vals) {
		t.Errorf("histogram total = %d, want %d", total, len(vals))
	}
}

// TestViewNoOverflow guards the chart-tearing bug: at several terminal sizes,
// no rendered line may exceed the terminal width (overflow == lipgloss wrapping
// == a shredded braille graph).
func TestViewNoOverflow(t *testing.T) {
	sizes := []struct{ w, h int }{{80, 24}, {120, 40}, {100, 30}, {160, 50}}
	lats := []float64{12, 18, 9, 240, 33, 27, 500, 14, 20, 11, 90, 7}
	for _, sz := range sizes {
		m := newModel("host:25565", "host", time.Second, time.Second, 240, nil)
		mi, _ := m.Update(tea.WindowSizeMsg{Width: sz.w, Height: sz.h})
		m = mi.(model)
		for i, l := range lats {
			s := sample{t: time.Now(), ok: true, latency: l, online: i, max: 100, version: "1.21.4"}
			mi, _ = m.Update(sampleMsg(s))
			m = mi.(model)
		}
		for n, line := range strings.Split(m.View(), "\n") {
			if w := lipgloss.Width(line); w > sz.w {
				t.Errorf("size %dx%d: line %d width %d > %d:\n%q", sz.w, sz.h, n, w, sz.w, line)
			}
		}
	}
}

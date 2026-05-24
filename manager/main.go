package main

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
)

//go:embed static
var staticFiles embed.FS

// Session holds a running tool subprocess with a line buffer readable by SSE handlers.
type Session struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	mu    sync.Mutex
	cond  *sync.Cond
	lines []string
	done  bool
}

func newSession() *Session {
	s := &Session{}
	s.cond = sync.NewCond(&s.mu)
	return s
}

var sessions sync.Map // string → *Session

func newID() string {
	b := make([]byte, 4)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// toolBin finds a tool binary by checking CWD, parent of CWD, and exe directory.
func toolBin(name string) string {
	cwd, _ := os.Getwd()
	candidates := []string{
		filepath.Join(cwd, name),
		filepath.Join(cwd, "..", name),
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(dir, name),
			filepath.Join(filepath.Dir(dir), name),
		)
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "./" + name
}

// combineOutputs merges stdout and stderr without blocking on either.
func combineOutputs(stdout, stderr io.Reader) io.Reader {
	pr, pw := io.Pipe()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); io.Copy(pw, stdout) }()
	go func() { defer wg.Done(); io.Copy(pw, stderr) }()
	go func() { wg.Wait(); pw.Close() }()
	return pr
}

// scanCRLF is like bufio.ScanLines but also splits on bare \r so that
// gaslighter's in-place metric updates ("\r\033[K...") are flushed immediately.
func scanCRLF(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	for i, b := range data {
		if b == '\n' {
			tok := data[:i]
			if len(tok) > 0 && tok[len(tok)-1] == '\r' {
				tok = tok[:len(tok)-1]
			}
			return i + 1, tok, nil
		}
		if b == '\r' {
			if i+1 < len(data) {
				if data[i+1] == '\n' {
					return i + 2, data[:i], nil // \r\n
				}
				return i + 1, data[:i], nil // bare \r — split here
			}
			if !atEOF {
				return 0, nil, nil // wait: might be \r\n
			}
			return i + 1, data[:i], nil
		}
	}
	if atEOF {
		return len(data), bytes.TrimRight(data, "\r\n"), nil
	}
	return 0, nil, nil
}

// startReader feeds subprocess output into the session line buffer.
func startReader(s *Session, r io.Reader) {
	go func() {
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 256*1024), 256*1024)
		scanner.Split(scanCRLF)
		for scanner.Scan() {
			if scanner.Text() == "" {
				continue // skip empty tokens from bare \r
			}
			s.mu.Lock()
			s.lines = append(s.lines, scanner.Text())
			s.mu.Unlock()
			s.cond.Broadcast()
		}
		s.mu.Lock()
		s.done = true
		s.mu.Unlock()
		s.cond.Broadcast()
	}()
}

// sseHeaders sets SSE response headers and returns the flusher.
func sseHeaders(w http.ResponseWriter) (http.Flusher, bool) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, ok := w.(http.Flusher)
	return flusher, ok
}

func sseData(w io.Writer, f http.Flusher, text string) {
	b, _ := json.Marshal(text)
	fmt.Fprintf(w, "data: %s\n\n", b)
	f.Flush()
}

func sseDone(w io.Writer, f http.Flusher) {
	fmt.Fprintf(w, "event: done\ndata: \"done\"\n\n")
	f.Flush()
}

// streamSession sends all buffered lines then continues streaming until done or context cancelled.
func streamSession(w http.ResponseWriter, r *http.Request, s *Session) {
	flusher, ok := sseHeaders(w)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	ctx := r.Context()
	localDone := make(chan struct{})
	defer close(localDone)
	go func() {
		select {
		case <-ctx.Done():
			s.cond.Broadcast()
		case <-localDone:
		}
	}()

	idx := 0
	for {
		s.mu.Lock()
		for idx >= len(s.lines) && !s.done {
			s.cond.Wait()
			if ctx.Err() != nil {
				s.mu.Unlock()
				return
			}
		}
		batch := make([]string, len(s.lines)-idx)
		copy(batch, s.lines[idx:])
		idx += len(batch)
		isDone := s.done
		s.mu.Unlock()

		for _, line := range batch {
			sseData(w, flusher, line)
		}
		if isDone {
			sseDone(w, flusher)
			return
		}
	}
}

// ── Wiretap ─────────────────────────────────────────────────────────────────

func handleWiretap(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Target        string `json:"target"`
		Proxies       string `json:"proxies"`
		ProxyStrategy string `json:"proxyStrategy"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Target == "" {
		http.Error(w, "target required", http.StatusBadRequest)
		return
	}

	args := []string{req.Target}
	if req.Proxies != "" {
		args = append(args, "--proxies", req.Proxies)
	}
	if req.ProxyStrategy != "" && req.ProxyStrategy != "random" {
		args = append(args, "--proxy-strategy", req.ProxyStrategy)
	}

	flusher, ok := sseHeaders(w)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	cmd := exec.CommandContext(r.Context(), toolBin("wiretap-bin"), args...)
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()

	if err := cmd.Start(); err != nil {
		sseData(w, flusher, "error: "+err.Error())
		sseDone(w, flusher)
		return
	}

	scanner := bufio.NewScanner(combineOutputs(stdout, stderr))
	scanner.Buffer(make([]byte, 256*1024), 256*1024)
	for scanner.Scan() {
		if r.Context().Err() != nil {
			cmd.Process.Kill()
			break
		}
		sseData(w, flusher, scanner.Text())
	}
	cmd.Wait()
	sseDone(w, flusher)
}

// ── Pluginscanner ────────────────────────────────────────────────────────────

func handlePsConnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Target  string `json:"target"`
		Timeout string `json:"timeout"`
		Proxies string `json:"proxies"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Target == "" {
		http.Error(w, "target required", http.StatusBadRequest)
		return
	}

	args := []string{req.Target}
	if req.Timeout != "" && req.Timeout != "5s" {
		args = append(args, "--timeout", req.Timeout)
	}
	if req.Proxies != "" {
		args = append(args, "--proxies", req.Proxies)
	}

	cmd := exec.Command(toolBin("pluginscanner-bin"), args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		http.Error(w, "pipe error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()

	if err := cmd.Start(); err != nil {
		http.Error(w, "failed to start pluginscanner: "+err.Error(), http.StatusInternalServerError)
		return
	}

	s := newSession()
	s.cmd = cmd
	s.stdin = stdin
	startReader(s, combineOutputs(stdout, stderr))

	id := newID()
	sessions.Store(id, s)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"id": id})
}

func handlePsStream(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	val, ok := sessions.Load(id)
	if !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	streamSession(w, r, val.(*Session))
}

func handlePsInput(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	id := r.URL.Query().Get("id")
	val, ok := sessions.Load(id)
	if !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	var req struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	s := val.(*Session)
	s.mu.Lock()
	if s.stdin != nil {
		fmt.Fprintln(s.stdin, req.Text)
	}
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func handlePsStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	id := r.URL.Query().Get("id")
	val, ok := sessions.LoadAndDelete(id)
	if !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s := val.(*Session)
	s.mu.Lock()
	if s.cmd != nil && s.cmd.Process != nil {
		s.cmd.Process.Kill()
		s.cmd.Wait()
	}
	s.done = true
	s.mu.Unlock()
	s.cond.Broadcast()
	w.WriteHeader(http.StatusNoContent)
}

// ── Gaslighter ───────────────────────────────────────────────────────────────

func handleGlStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Target          string `json:"target"`
		Workers         int    `json:"workers"`
		BloatSize       int    `json:"bloatSize"`
		DribbleInterval string `json:"dribbleInterval"`
		JoinDelay       string `json:"joinDelay"`
		Verbose         bool   `json:"verbose"`
		Prelogin        bool   `json:"prelogin"`
		Har             bool   `json:"har"`
		Stall           bool   `json:"stall"`
		StallDuration   string `json:"stallDuration"`
		Proxies         string `json:"proxies"`
		ProxyStrategy   string `json:"proxyStrategy"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Target == "" {
		http.Error(w, "target required", http.StatusBadRequest)
		return
	}

	args := []string{req.Target}
	if req.Workers > 0 {
		args = append(args, "--workers", strconv.Itoa(req.Workers))
	}
	if req.BloatSize > 0 {
		args = append(args, "--bloat-size", strconv.Itoa(req.BloatSize))
	}
	if req.DribbleInterval != "" {
		args = append(args, "--dribble-interval", req.DribbleInterval)
	}
	if req.JoinDelay != "" {
		args = append(args, "--join-delay", req.JoinDelay)
	}
	if req.Verbose {
		args = append(args, "--verbose")
	}
	if req.Prelogin {
		args = append(args, "--prelogin")
		if req.Har {
			args = append(args, "--har")
		}
	} else if req.Stall {
		args = append(args, "--stall")
		if req.StallDuration != "" {
			args = append(args, "--stall-duration", req.StallDuration)
		}
	}
	if req.Proxies != "" {
		args = append(args, "--proxies", req.Proxies)
	}
	if req.ProxyStrategy != "" && req.ProxyStrategy != "random" {
		args = append(args, "--proxy-strategy", req.ProxyStrategy)
	}

	cmd := exec.Command(toolBin("gaslighter-bin"), args...)
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()

	if err := cmd.Start(); err != nil {
		http.Error(w, "failed to start gaslighter: "+err.Error(), http.StatusInternalServerError)
		return
	}

	s := newSession()
	s.cmd = cmd
	startReader(s, combineOutputs(stdout, stderr))

	id := newID()
	sessions.Store(id, s)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"id": id})
}

func handleGlStream(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	val, ok := sessions.Load(id)
	if !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	streamSession(w, r, val.(*Session))
}

func handleGlStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	id := r.URL.Query().Get("id")
	val, ok := sessions.LoadAndDelete(id)
	if !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s := val.(*Session)
	s.mu.Lock()
	if s.cmd != nil && s.cmd.Process != nil {
		s.cmd.Process.Kill()
		s.cmd.Wait()
	}
	s.done = true
	s.mu.Unlock()
	s.cond.Broadcast()
	w.WriteHeader(http.StatusNoContent)
}

// ────────────────────────────────────────────────────────────────────────────

func main() {
	port := "8080"
	if p := os.Getenv("PORT"); p != "" {
		port = p
	}

	sub, err := fs.Sub(staticFiles, "static")
	if err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(sub)))
	mux.HandleFunc("/api/wiretap", handleWiretap)
	mux.HandleFunc("/api/pluginscanner/connect", handlePsConnect)
	mux.HandleFunc("/api/pluginscanner/stream", handlePsStream)
	mux.HandleFunc("/api/pluginscanner/input", handlePsInput)
	mux.HandleFunc("/api/pluginscanner/stop", handlePsStop)
	mux.HandleFunc("/api/gaslighter/start", handleGlStart)
	mux.HandleFunc("/api/gaslighter/stream", handleGlStream)
	mux.HandleFunc("/api/gaslighter/stop", handleGlStop)

	addr := ":" + port
	log.Printf("Server Fun Manager → http://localhost%s\n", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

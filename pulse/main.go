package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	var (
		interval = flag.Duration("interval", 2*time.Second, "initial poll interval (adjustable live with +/-)")
		timeout  = flag.Duration("timeout", 5*time.Second, "per-poll SLP timeout")
		window   = flag.Int("window", 240, "samples kept in memory / shown on charts")
		dbPath   = flag.String("db", "", "SQLite file to persist samples (empty = no persistence)")
		proxies  = flag.String("proxies", "", "file of socks5 host:port proxies, one per line")
		strategy = flag.String("proxy-strategy", "random", "proxy selection: random | round-robin")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "pulse — continuous SLP monitor with a live TUI\n\n")
		fmt.Fprintf(os.Stderr, "Usage:\n  pulse [flags] <host[:port]>\n\nFlags:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}
	proxyStrategy = *strategy
	if *proxies != "" {
		if err := loadProxies(*proxies); err != nil {
			fmt.Fprintln(os.Stderr, "proxy load:", err)
			os.Exit(1)
		}
	}

	display := flag.Arg(0)
	target := resolveTarget(display)

	var store *Store
	if *dbPath != "" {
		s, err := openStore(*dbPath, target)
		if err != nil {
			fmt.Fprintln(os.Stderr, "sqlite:", err)
			os.Exit(1)
		}
		store = s
		defer store.close()
	}

	m := newModel(target, display, *interval, *timeout, *window, store)
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "pulse:", err)
		os.Exit(1)
	}
}

func loadProxies(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		proxyPool = append(proxyPool, line)
	}
	return sc.Err()
}

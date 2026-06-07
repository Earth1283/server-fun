package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/NimbleMarkets/ntcharts/canvas/runes"
	"github.com/NimbleMarkets/ntcharts/linechart/streamlinechart"
	"github.com/NimbleMarkets/ntcharts/sparkline"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ---------------------------------------------------------------------------
// Styles
// ---------------------------------------------------------------------------

var (
	cGreen  = lipgloss.Color("42")
	cCyan   = lipgloss.Color("44")
	cRed    = lipgloss.Color("196")
	cYellow = lipgloss.Color("214")
	cGray   = lipgloss.Color("242")
	cWhite  = lipgloss.Color("252")

	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("231")).Background(cCyan).Padding(0, 1)
	boxStyle   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(cGray).Padding(0, 1)
	labelStyle = lipgloss.NewStyle().Foreground(cGray)
	dimStyle   = lipgloss.NewStyle().Foreground(cGray)
	footStyle  = lipgloss.NewStyle().Foreground(cGray)
)

// ---------------------------------------------------------------------------
// Messages & commands
// ---------------------------------------------------------------------------

type sampleMsg sample
type pollTickMsg struct{}

func doPoll(target string, timeout time.Duration) tea.Cmd {
	return func() tea.Msg {
		resp, lat, err := ping(target, timeout)
		s := sample{t: time.Now()}
		if err != nil {
			s.ok = false
			s.errMsg = err.Error()
			return sampleMsg(s)
		}
		s.ok = true
		s.latency = lat
		s.online = resp.Players.Online
		s.max = resp.Players.Max
		s.version = resp.Version.Name
		s.protocol = resp.Version.Protocol
		s.motd = strings.TrimSpace(resp.motd())
		return sampleMsg(s)
	}
}

func scheduleTick(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return pollTickMsg{} })
}

// ---------------------------------------------------------------------------
// Model
// ---------------------------------------------------------------------------

type model struct {
	target  string
	display string
	timeout time.Duration
	store   *Store

	interval time.Duration
	window   int
	paused   bool

	samples []sample
	events  []event
	last    *sample

	latChart  streamlinechart.Model
	playChart streamlinechart.Model
	latSpark  sparkline.Model
	playSpark sparkline.Model

	polls, okPolls int

	width, height int
	ready         bool
}

func newModel(target, display string, interval, timeout time.Duration, window int, store *Store) model {
	return model{
		target:   target,
		display:  display,
		timeout:  timeout,
		interval: interval,
		window:   window,
		store:    store,
	}
}

func (m model) Init() tea.Cmd {
	return doPoll(m.target, m.timeout)
}

// ---------------------------------------------------------------------------
// Update
// ---------------------------------------------------------------------------

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.rebuildCharts()
		m.ready = true
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		case " ", "p":
			m.paused = !m.paused
			if m.paused {
				m.addEvent(evInfo, "polling paused")
			} else {
				m.addEvent(evInfo, "polling resumed")
				return m, doPoll(m.target, m.timeout)
			}
		case "+", "=":
			m.interval = stepInterval(m.interval, true)
			m.addEvent(evInfo, "interval → "+fmtDur(m.interval))
		case "-", "_":
			m.interval = stepInterval(m.interval, false)
			m.addEvent(evInfo, "interval → "+fmtDur(m.interval))
		case "c":
			m.events = nil
		case "r":
			return m, doPoll(m.target, m.timeout) // force immediate poll
		}
		return m, nil

	case pollTickMsg:
		if m.paused {
			return m, nil
		}
		return m, doPoll(m.target, m.timeout)

	case sampleMsg:
		s := sample(msg)
		m.ingest(s)
		var cmd tea.Cmd
		if !m.paused {
			cmd = scheduleTick(m.interval)
		}
		return m, cmd
	}
	return m, nil
}

// ingest records a sample: counters, event detection, persistence, charts.
func (m *model) ingest(s sample) {
	m.polls++
	if s.ok {
		m.okPolls++
	}
	m.detectEvents(s)

	m.samples = append(m.samples, s)
	if len(m.samples) > m.window {
		m.samples = m.samples[len(m.samples)-m.window:]
	}
	cp := s
	m.last = &cp

	if m.store != nil {
		_ = m.store.save(s)
	}
	m.redrawCharts()
}

func (m *model) detectEvents(s sample) {
	if m.last == nil {
		if s.ok {
			m.addEvent(evUp, fmt.Sprintf("online — %s, %d players", s.version, s.online))
		} else {
			m.addEvent(evDown, "unreachable: "+s.errMsg)
		}
		return
	}
	prev := *m.last
	switch {
	case prev.ok && !s.ok:
		m.addEvent(evDown, "went OFFLINE: "+s.errMsg)
	case !prev.ok && s.ok:
		m.addEvent(evUp, fmt.Sprintf("recovered — %s, %d players", s.version, s.online))
	}
	if s.ok && prev.ok {
		if s.version != prev.version && s.version != "" {
			m.addEvent(evVersion, fmt.Sprintf("version %q → %q", prev.version, s.version))
		}
		if s.motd != prev.motd && s.motd != "" {
			m.addEvent(evMotd, "MOTD changed: "+oneLine(s.motd))
		}
		if d := s.online - prev.online; d >= 5 || d <= -5 {
			m.addEvent(evPlayers, fmt.Sprintf("players %+d (now %d)", d, s.online))
		}
		// Latency spike: 3× the recent rolling average.
		if avg := m.recentLatAvg(); avg > 0 && s.latency > avg*3 && s.latency > 50 {
			m.addEvent(evSpike, fmt.Sprintf("latency spike %.0fms (avg %.0fms)", s.latency, avg))
		}
	}
}

func (m *model) recentLatAvg() float64 {
	n := 0
	var sum float64
	for i := len(m.samples) - 1; i >= 0 && n < 20; i-- {
		if m.samples[i].ok {
			sum += m.samples[i].latency
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}

func (m *model) addEvent(k eventKind, text string) {
	m.events = append(m.events, event{t: time.Now(), kind: k, text: text})
	if len(m.events) > 200 {
		m.events = m.events[len(m.events)-200:]
	}
}

// ---------------------------------------------------------------------------
// Chart wiring
// ---------------------------------------------------------------------------

// colInnerWidth is the content width available inside one bordered+padded box
// when two boxes sit side by side. The chart canvas must be built at exactly
// this width, otherwise lipgloss wraps the braille lines and the graph tears.
func (m *model) colInnerWidth() int {
	w := m.width/2 - boxStyle.GetHorizontalFrameSize()
	if w < 12 {
		w = 12
	}
	return w
}

// boxWidthArg is the value passed to boxStyle.Width(): lipgloss treats it as
// content+padding (border added outside), so width = colInnerWidth + padding(2)
// yields an inner text area of exactly colInnerWidth and a total of m.width/2.
func (m *model) boxWidthArg() int { return m.colInnerWidth() + 2 }

func (m *model) chartDims() (w, h int) {
	w = m.colInnerWidth()
	h = (m.height - 12) * 6 / 10
	if h < 4 {
		h = 4
	}
	return w, h
}

func (m *model) rebuildCharts() {
	w, h := m.chartDims()
	latStyle := lipgloss.NewStyle().Foreground(cCyan)
	playStyle := lipgloss.NewStyle().Foreground(cGreen)
	axis := lipgloss.NewStyle().Foreground(cGray)

	m.latChart = streamlinechart.New(w, h,
		streamlinechart.WithStyles(runes.ThinLineStyle, latStyle),
		streamlinechart.WithAxesStyles(axis, axis))
	m.playChart = streamlinechart.New(w, h,
		streamlinechart.WithStyles(runes.ThinLineStyle, playStyle),
		streamlinechart.WithAxesStyles(axis, axis))

	// Sparklines share the header line with their labels, so size them to half
	// the row minus label space to keep the header from wrapping.
	sw := m.width/2 - 12
	if sw < 8 {
		sw = 8
	}
	m.latSpark = sparkline.New(sw, 1, sparkline.WithStyle(latStyle))
	m.playSpark = sparkline.New(sw, 1, sparkline.WithStyle(playStyle))

	m.redrawCharts()
}

func (m *model) redrawCharts() {
	if !m.ready {
		return
	}
	m.latChart.ClearAllData()
	m.playChart.ClearAllData()
	m.latSpark.Clear()
	m.playSpark.Clear()

	var lats []float64
	maxPlayers := 1.0
	for _, s := range m.samples {
		lats = append(lats, s.latency)
		if float64(s.online) > maxPlayers {
			maxPlayers = float64(s.online)
		}
	}
	_, latMax, _ := minMaxAvg(lats)
	if latMax < 1 {
		latMax = 1
	}
	m.latChart.SetViewYRange(0, latMax*1.15)
	m.playChart.SetViewYRange(0, maxPlayers*1.15)

	for _, s := range m.samples {
		m.latChart.Push(s.latency)
		m.playChart.Push(float64(s.online))
		m.latSpark.Push(s.latency)
		m.playSpark.Push(float64(s.online))
	}
	m.latChart.Draw()
	m.playChart.Draw()
	m.latSpark.Draw()
	m.playSpark.Draw()
}

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

func (m model) View() string {
	if !m.ready {
		return "pulse — initializing… (resize terminal if this persists)"
	}
	header := m.viewHeader()
	charts := m.viewCharts()
	bottom := m.viewBottom()
	footer := footStyle.Render(
		"  [+/-] interval   [space] pause   [r] poll now   [c] clear events   [q] quit")
	out := lipgloss.JoinVertical(lipgloss.Left, header, charts, bottom, footer)

	// Safety net: never emit a line wider than the terminal. JoinVertical pads
	// every line to the widest one, so a single long line (e.g. the gauge row on
	// a narrow terminal) would otherwise force the terminal to wrap *all* rows —
	// tearing the graphs. Clamp each line to the exact terminal width.
	if m.width > 0 {
		clamp := lipgloss.NewStyle().MaxWidth(m.width)
		lines := strings.Split(out, "\n")
		for i, l := range lines {
			if lipgloss.Width(l) > m.width {
				lines[i] = clamp.Render(l)
			}
		}
		out = strings.Join(lines, "\n")
	}
	return out
}

func (m model) viewHeader() string {
	status := lipgloss.NewStyle().Foreground(cYellow).Render("WAITING")
	ping := "—"
	players := "—"
	version := "—"
	if m.last != nil {
		if m.last.ok {
			status = lipgloss.NewStyle().Bold(true).Foreground(cGreen).Render("● UP")
			ping = colorLatency(m.last.latency)
			players = fmt.Sprintf("%d/%d", m.last.online, m.last.max)
			version = m.last.version
		} else {
			status = lipgloss.NewStyle().Bold(true).Foreground(cRed).Render("● DOWN")
		}
	}
	uptime := "—"
	if m.polls > 0 {
		uptime = fmt.Sprintf("%.1f%%", 100*float64(m.okPolls)/float64(m.polls))
	}

	gauge := func(label, val string) string {
		return labelStyle.Render(label+" ") + lipgloss.NewStyle().Foreground(cWhite).Render(val)
	}
	row := strings.Join([]string{
		gauge("STAT", status),
		gauge("PING", ping),
		gauge("PLRS", players),
		gauge("VER", version),
		gauge("UP", uptime),
		gauge("EVERY", fmtDur(m.interval)),
		gauge("N", fmt.Sprintf("%d", len(m.samples))),
	}, dimStyle.Render(" │ "))

	title := titleStyle.Render("🩺 pulse") + " " +
		lipgloss.NewStyle().Bold(true).Foreground(cCyan).Render(m.display)
	if m.paused {
		title += " " + lipgloss.NewStyle().Foreground(cYellow).Render("[PAUSED]")
	}

	spark := labelStyle.Render("ping ") + m.latSpark.View() + "  " +
		labelStyle.Render("players ") + m.playSpark.View()

	body := lipgloss.JoinVertical(lipgloss.Left, row, spark)
	return lipgloss.JoinVertical(lipgloss.Left, title, body)
}

func (m model) viewCharts() string {
	// Inner area is pinned to colInnerWidth — exactly the chart canvas width —
	// so the braille lines fit without wrapping (which is what tore the graph).
	bw := m.boxWidthArg()
	lat := boxStyle.Width(bw).Render(
		lipgloss.NewStyle().Foreground(cCyan).Render("Ping latency (ms)") + "\n" + m.latChart.View())
	play := boxStyle.Width(bw).Render(
		lipgloss.NewStyle().Foreground(cGreen).Render("Players online") + "\n" + m.playChart.View())
	return lipgloss.JoinHorizontal(lipgloss.Top, lat, play)
}

func (m model) viewBottom() string {
	w := m.colInnerWidth()
	bw := m.boxWidthArg()
	h := (m.height - 12) * 4 / 10
	if h < 4 {
		h = 4
	}
	// Clamp both panels to the same total height so their borders line up
	// regardless of how much content each holds.
	box := boxStyle.Width(bw).Height(h).MaxHeight(h + boxStyle.GetVerticalFrameSize())
	hist := box.Render(m.viewHistogram(w, h))
	events := box.Render(m.viewEvents(w, h-1))
	return lipgloss.JoinHorizontal(lipgloss.Top, hist, events)
}

func (m model) viewHistogram(w, rows int) string {
	var lats []float64
	for _, s := range m.samples {
		if s.ok {
			lats = append(lats, s.latency)
		}
	}
	head := lipgloss.NewStyle().Foreground(cYellow).Render("Latency distribution")
	if len(lats) < 2 {
		return head + "\n" + dimStyle.Render("gathering samples…")
	}
	p50, p95, p99 := percentiles(lats)
	mn, mx, avg := minMaxAvg(lats)
	// Compact, single-line stat row (truncated to width so it never wraps and
	// steals a bucket's row).
	stat := dimStyle.Render(truncate(fmt.Sprintf(
		"min %.0f avg %.0f p50 %.0f p95 %.0f p99 %.0f max %.0f",
		mn, avg, p50, p95, p99, mx), w))

	// Fit the bucket count to the rows left after the header + stat lines.
	nb := rows - 2
	if nb < 3 {
		nb = 3
	}
	if nb > 6 {
		nb = 6
	}
	buckets := histogram(lats, nb)
	maxCount := 1
	for _, b := range buckets {
		if b.count > maxCount {
			maxCount = b.count
		}
	}
	barW := w - 20
	if barW < 4 {
		barW = 4
	}
	var sb strings.Builder
	for _, b := range buckets {
		fill := b.count * barW / maxCount
		bar := lipgloss.NewStyle().Foreground(cCyan).Render(strings.Repeat("█", fill)) +
			dimStyle.Render(strings.Repeat("░", barW-fill))
		fmt.Fprintf(&sb, "%5.0f-%-5.0f %s %d\n", b.lo, b.hi, bar, b.count)
	}
	return lipgloss.JoinVertical(lipgloss.Left, head, stat, strings.TrimRight(sb.String(), "\n"))
}

func (m model) viewEvents(w, rows int) string {
	head := lipgloss.NewStyle().Foreground(cYellow).Render("Events")
	if rows < 1 {
		rows = 1
	}
	if len(m.events) == 0 {
		return head + "\n" + dimStyle.Render("no events yet")
	}
	start := 0
	if len(m.events) > rows {
		start = len(m.events) - rows
	}
	var lines []string
	for _, e := range m.events[start:] {
		ts := dimStyle.Render(e.t.Format("15:04:05"))
		line := ts + " " + colorEvent(e.kind, e.text)
		lines = append(lines, truncate(line, w))
	}
	return lipgloss.JoinVertical(lipgloss.Left, append([]string{head}, lines...)...)
}

// ---------------------------------------------------------------------------
// Formatting helpers
// ---------------------------------------------------------------------------

func colorLatency(ms float64) string {
	c := cGreen
	switch {
	case ms >= 300:
		c = cRed
	case ms >= 100:
		c = cYellow
	}
	return lipgloss.NewStyle().Foreground(c).Render(fmt.Sprintf("%.0fms", ms))
}

func colorEvent(k eventKind, text string) string {
	c := cWhite
	tag := ""
	switch k {
	case evUp:
		c, tag = cGreen, "▲ "
	case evDown:
		c, tag = cRed, "▼ "
	case evVersion:
		c, tag = cCyan, "⟳ "
	case evMotd:
		c, tag = cCyan, "✎ "
	case evSpike:
		c, tag = cYellow, "⚡ "
	case evPlayers:
		c, tag = cGreen, "☺ "
	case evInfo:
		c, tag = cGray, "· "
	}
	return lipgloss.NewStyle().Foreground(c).Render(tag + text)
}

func stepInterval(d time.Duration, up bool) time.Duration {
	step := 250 * time.Millisecond
	switch {
	case d >= 10*time.Second:
		step = 5 * time.Second
	case d >= time.Second:
		step = time.Second
	}
	if up {
		d += step
	} else {
		d -= step
	}
	if d < 250*time.Millisecond {
		d = 250 * time.Millisecond
	}
	if d > 60*time.Second {
		d = 60 * time.Second
	}
	return d
}

func fmtDur(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d%time.Second == 0 {
		return fmt.Sprintf("%ds", int(d/time.Second))
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	return truncate(s, 40)
}

func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	// lipgloss.Width accounts for ANSI; use it for safe truncation.
	if lipgloss.Width(s) <= w {
		return s
	}
	return lipgloss.NewStyle().MaxWidth(w).Render(s)
}

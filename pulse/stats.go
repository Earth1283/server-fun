package main

import (
	"math"
	"sort"
	"time"
)

// sample is one SLP poll result.
type sample struct {
	t        time.Time
	ok       bool
	latency  float64 // ms (ping RTT)
	online   int
	max      int
	version  string
	protocol int
	motd     string
	errMsg   string
}

// event is a notable state change shown in the event log pane.
type event struct {
	t    time.Time
	kind eventKind
	text string
}

type eventKind int

const (
	evUp eventKind = iota
	evDown
	evVersion
	evMotd
	evSpike
	evPlayers
	evInfo
)

// percentiles returns p50/p95/p99 over the supplied (unsorted) latencies.
func percentiles(vals []float64) (p50, p95, p99 float64) {
	if len(vals) == 0 {
		return 0, 0, 0
	}
	s := append([]float64(nil), vals...)
	sort.Float64s(s)
	pick := func(p float64) float64 {
		idx := int(math.Ceil(p/100*float64(len(s)))) - 1
		if idx < 0 {
			idx = 0
		}
		if idx >= len(s) {
			idx = len(s) - 1
		}
		return s[idx]
	}
	return pick(50), pick(95), pick(99)
}

func minMaxAvg(vals []float64) (mn, mx, avg float64) {
	if len(vals) == 0 {
		return 0, 0, 0
	}
	mn = math.MaxFloat64
	var sum float64
	for _, v := range vals {
		if v < mn {
			mn = v
		}
		if v > mx {
			mx = v
		}
		sum += v
	}
	return mn, mx, sum / float64(len(vals))
}

// histogram buckets latencies into n equal-width bins between min and max.
type histBucket struct {
	lo, hi float64
	count  int
}

func histogram(vals []float64, n int) []histBucket {
	if len(vals) == 0 || n < 1 {
		return nil
	}
	mn, mx, _ := minMaxAvg(vals)
	if mx <= mn {
		mx = mn + 1
	}
	width := (mx - mn) / float64(n)
	buckets := make([]histBucket, n)
	for i := range buckets {
		buckets[i].lo = mn + float64(i)*width
		buckets[i].hi = mn + float64(i+1)*width
	}
	for _, v := range vals {
		idx := int((v - mn) / width)
		if idx >= n {
			idx = n - 1
		}
		if idx < 0 {
			idx = 0
		}
		buckets[idx].count++
	}
	return buckets
}

// Package format renders numbers, byte sizes, rates and durations in the
// compact, consistent style used across the dashboard.
package format

import (
	"math"
	"strconv"
	"time"
)

var siUnits = []string{"", "k", "M", "G", "T", "P"}

// Number formats a count compactly: 950, 12.4k, 3.1M.
func Number(v float64) string {
	return scaled(v, 1000, siUnits)
}

// Bytes formats a byte size with binary scaling: 512B, 12.4K, 3.1G.
func Bytes(v float64) string {
	return scaled(v, 1024, []string{"B", "K", "M", "G", "T", "P"})
}

// Bits formats a bit rate: 950b/s, 12.4kb/s, 3.1Mb/s.
func Bits(bps float64) string {
	return scaled(bps, 1000, []string{"b", "kb", "Mb", "Gb", "Tb", "Pb"}) + "/s"
}

// Percent formats a percentage: "4.2%" below ten, "42%" above.
func Percent(p float64) string {
	if math.IsNaN(p) || math.IsInf(p, 0) {
		return "-"
	}
	if math.Abs(p) < 10 && p != 0 {
		return strconv.FormatFloat(p, 'f', 1, 64) + "%"
	}
	return strconv.FormatFloat(math.Round(p), 'f', 0, 64) + "%"
}

func scaled(v, base float64, units []string) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return "-"
	}
	neg := v < 0
	v = math.Abs(v)
	i := 0
	for v >= base && i < len(units)-1 {
		v /= base
		i++
	}
	var s string
	switch {
	case i == 0:
		s = strconv.FormatFloat(math.Round(v), 'f', 0, 64)
	case v >= 100:
		s = strconv.FormatFloat(math.Round(v), 'f', 0, 64)
	default:
		s = strconv.FormatFloat(v, 'f', 1, 64)
	}
	if neg {
		s = "-" + s
	}
	return s + units[i]
}

// Duration formats a duration with its two most significant units:
// "12s", "3m 12s", "2h 5m", "4d 3h".
func Duration(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	if d < time.Second {
		if d == 0 {
			return "0s"
		}
		return strconv.FormatInt(d.Milliseconds(), 10) + "ms"
	}
	secs := int64(d / time.Second)
	days, secs := secs/86400, secs%86400
	hours, secs := secs/3600, secs%3600
	mins, secs := secs/60, secs%60
	pair := func(a int64, ua string, b int64, ub string) string {
		if b == 0 {
			return strconv.FormatInt(a, 10) + ua
		}
		return strconv.FormatInt(a, 10) + ua + " " + strconv.FormatInt(b, 10) + ub
	}
	switch {
	case days > 0:
		return pair(days, "d", hours, "h")
	case hours > 0:
		return pair(hours, "h", mins, "m")
	case mins > 0:
		return pair(mins, "m", secs, "s")
	default:
		return strconv.FormatInt(secs, 10) + "s"
	}
}

// Latency formats a round-trip time: "0.8ms", "42ms", "1.2s".
func Latency(d time.Duration) string {
	switch {
	case d <= 0:
		return "-"
	case d < 10*time.Millisecond:
		return strconv.FormatFloat(float64(d)/float64(time.Millisecond), 'f', 1, 64) + "ms"
	case d < time.Second:
		return strconv.FormatInt(d.Milliseconds(), 10) + "ms"
	default:
		return strconv.FormatFloat(d.Seconds(), 'f', 1, 64) + "s"
	}
}

// Ago formats the time elapsed since t: "now", "5s ago", "3m ago".
// A zero t yields "never".
func Ago(t, now time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := now.Sub(t)
	if d < time.Second {
		return "now"
	}
	secs := int64(d / time.Second)
	switch {
	case secs < 60:
		return strconv.FormatInt(secs, 10) + "s ago"
	case secs < 3600:
		return strconv.FormatInt(secs/60, 10) + "m ago"
	case secs < 86400:
		return strconv.FormatInt(secs/3600, 10) + "h ago"
	default:
		return strconv.FormatInt(secs/86400, 10) + "d ago"
	}
}

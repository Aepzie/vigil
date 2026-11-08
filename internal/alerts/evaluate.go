package alerts

import (
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/CraguS/vigil/internal/metrics"
	"github.com/CraguS/vigil/internal/sanitize"
)

// level orders "no alert" below the two severities.
type level int

const (
	levelNone level = iota
	levelWarn
	levelCrit
)

func levelOf(a *Alert) level {
	switch {
	case a == nil:
		return levelNone
	case a.Severity == SeverityCrit:
		return levelCrit
	default:
		return levelWarn
	}
}

var offlineKey = alertKey{kind: KindOffline}

func (e *Engine) evalOffline(ss *serverState, snap metrics.Snapshot, now time.Time) {
	ss.cpuWarnRun, ss.cpuCritRun = 0, 0
	if ss.firstOffline.IsZero() {
		ss.firstOffline = now
	}
	start := ss.firstOffline
	if !snap.LastSeen.IsZero() && snap.LastSeen.Before(start) {
		start = snap.LastSeen
	}
	offlineFor := now.Sub(start)
	if offlineFor < e.thresholds.OfflineGrace {
		return
	}

	reason := sanitize.Line(snap.Err)
	if reason == "" {
		reason = "unreachable"
	}
	msg := "offline: " + reason
	value, threshold := offlineFor.Seconds(), e.thresholds.OfflineGrace.Seconds()
	if a := ss.active[offlineKey]; a != nil {
		a.Value, a.Threshold, a.Message = value, threshold, msg
		return
	}
	e.fire(ss, offlineKey, SeverityCrit, value, threshold, msg, now)
}

func (e *Engine) evalOnline(ss *serverState, snap metrics.Snapshot, now time.Time) {
	ss.firstOffline = time.Time{}
	if ss.active[offlineKey] != nil {
		e.resolve(ss, offlineKey, now)
	}
	th := e.thresholds

	cpu := snap.CPU.Total
	ss.cpuWarnRun = nextRun(ss.cpuWarnRun, cpu, th.CPUWarn)
	ss.cpuCritRun = nextRun(ss.cpuCritRun, cpu, th.CPUCrit)
	allowed := levelNone
	if ss.cpuWarnRun >= SpikeSamples {
		allowed = levelWarn
	}
	if ss.cpuCritRun >= SpikeSamples {
		allowed = levelCrit
	}
	e.evalLevel(ss, alertKey{kind: KindCPU}, cpu, th.CPUWarn, th.CPUCrit, allowed, now)
	e.evalLevel(ss, alertKey{kind: KindMem}, snap.Mem.UsedPct(), th.MemWarn, th.MemCrit, levelCrit, now)

	mounts := make(map[string]float64, len(snap.Disks))
	for _, d := range snap.Disks {
		if d.Mount == "" {
			continue
		}
		mounts[d.Mount] = max(mounts[d.Mount], d.UsedPct())
	}
	for _, mount := range slices.Sorted(maps.Keys(mounts)) {
		e.evalLevel(ss, alertKey{kind: KindDisk, subject: mount}, mounts[mount], th.DiskWarn, th.DiskCrit, levelCrit, now)
	}
	var gone []alertKey
	for k := range ss.active {
		if _, present := mounts[k.subject]; k.kind == KindDisk && !present {
			gone = append(gone, k)
		}
	}
	slices.SortFunc(gone, func(a, b alertKey) int { return strings.Compare(a.subject, b.subject) })
	for _, k := range gone {
		e.resolve(ss, k, now)
	}
}

// nextRun returns the length of the run of consecutive samples at or
// above threshold after observing value.
func nextRun(run int, value, threshold float64) int {
	if threshold <= 0 || !(value >= threshold) {
		return 0
	}
	return run + 1
}

// targetLevel applies the thresholds with hysteresis relative to the
// current level.
func targetLevel(cur level, value, warn, crit float64) level {
	switch {
	case crit > 0 && value >= crit:
		return levelCrit
	case cur == levelCrit && crit > 0 && value >= crit-hysteresis(crit):
		return levelCrit
	case warn > 0 && value >= warn:
		return levelWarn
	case cur >= levelWarn && warn > 0 && value >= warn-hysteresis(warn):
		return levelWarn
	default:
		return levelNone
	}
}

func hysteresis(threshold float64) float64 {
	return min(Hysteresis, threshold/2)
}

// evalLevel moves the alert for key to the level the value calls for.
// allowed caps increases (the CPU spike filter); decreases are never
// capped.
func (e *Engine) evalLevel(ss *serverState, key alertKey, value, warn, crit float64, allowed level, now time.Time) {
	if math.IsNaN(value) {
		return
	}
	a := ss.active[key]
	cur := levelOf(a)
	target := targetLevel(cur, value, warn, crit)
	if target > cur && target > allowed {
		target = max(cur, allowed)
	}

	sev, threshold := SeverityWarn, warn
	if target == levelCrit {
		sev, threshold = SeverityCrit, crit
	}
	switch {
	case target == levelNone && a == nil:
	case target == levelNone:
		e.resolve(ss, key, now)
	case a == nil:
		e.fire(ss, key, sev, value, threshold, utilMessage(key, value, threshold), now)
	default:
		a.Value, a.Threshold, a.Message = value, threshold, utilMessage(key, value, threshold)
		if target > cur {
			a.Severity, a.Acked = sev, false
			e.emit(EventEscalated, *a, now)
			return
		}
		a.Severity = sev
	}
}

func (e *Engine) fire(ss *serverState, key alertKey, sev Severity, value, threshold float64, msg string, now time.Time) {
	a := &Alert{
		ID:         e.newID(ss, key, now),
		ServerID:   ss.id,
		ServerName: ss.name,
		Kind:       key.kind,
		Severity:   sev,
		Subject:    key.subject,
		Message:    msg,
		Value:      value,
		Threshold:  threshold,
		Since:      now,
	}
	ss.active[key] = a
	e.emit(EventFired, *a, now)
}

func (e *Engine) resolve(ss *serverState, key alertKey, now time.Time) {
	a := ss.active[key]
	if a == nil {
		return
	}
	delete(ss.active, key)
	a.Resolved, a.ResolvedAt = true, now
	e.resolved = append(e.resolved, *a)
	if over := len(e.resolved) - MaxResolved; over > 0 {
		e.resolved = append(e.resolved[:0], e.resolved[over:]...)
	}
	e.emit(EventResolved, *a, now)
}

// newID builds serverID:kind:subject:unixnano, adding a suffix in the
// unlikely case that a previous occurrence started at the same instant.
func (e *Engine) newID(ss *serverState, key alertKey, since time.Time) string {
	base := fmt.Sprintf("%s:%s:%s:%d", ss.id, key.kind, key.subject, since.UnixNano())
	id := base
	for n := 1; e.idInUse(id); n++ {
		id = base + "." + strconv.Itoa(n)
	}
	return id
}

func (e *Engine) idInUse(id string) bool {
	for _, a := range e.resolved {
		if a.ID == id {
			return true
		}
	}
	for _, ss := range e.servers {
		for _, a := range ss.active {
			if a.ID == id {
				return true
			}
		}
	}
	return false
}

// utilMessage renders e.g. "CPU 93% ≥ 80%" or, inside the hysteresis
// band, "CPU 77% (threshold 80%)".
func utilMessage(key alertKey, value, threshold float64) string {
	label := "CPU"
	switch key.kind {
	case KindMem:
		label = "Memory"
	case KindDisk:
		label = "Disk " + key.subject
	}
	shown, limit := formatPct(value, threshold), strconv.FormatFloat(threshold, 'f', -1, 64)
	if value >= threshold {
		return fmt.Sprintf("%s %s%% ≥ %s%%", label, shown, limit)
	}
	return fmt.Sprintf("%s %s%% (threshold %s%%)", label, shown, limit)
}

// formatPct truncates value so that it never displays as reaching a
// threshold it has not reached: whole percent for whole thresholds, one
// decimal otherwise.
func formatPct(value, threshold float64) string {
	if threshold == math.Trunc(threshold) {
		return strconv.FormatFloat(math.Floor(value), 'f', 0, 64)
	}
	return strconv.FormatFloat(math.Floor(value*10)/10, 'f', 1, 64)
}

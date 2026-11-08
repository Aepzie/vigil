package collector

import (
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/CraguS/vigil/internal/metrics"
)

// remoteCommand runs the poll script, sent on stdin, with a POSIX shell
// regardless of the user's login shell and with a C locale so numbers and
// headers are not localized.
const remoteCommand = "env LC_ALL=C LANG=C /bin/sh -s"

// markerPrefix starts every section marker line. The full marker is
// "@@vigil:<nonce>:<section>", where the per-poll random nonce prevents
// remote content (e.g. a process name) from forging a section.
const markerPrefix = "@@vigil:"

// Section names, in script order.
const (
	secStat     = "stat"
	secMeminfo  = "meminfo"
	secLoadavg  = "loadavg"
	secUptime   = "uptime"
	secNetDev   = "netdev"
	secDF       = "df"
	secUname    = "uname"
	secOS       = "osrelease"
	secHostname = "hostname"
	secPageSize = "pagesize"
	secClkTck   = "clktck"
	secOwners   = "owners"
	secPasswd   = "passwd"
	secPidStat  = "pidstat"
	secEnd      = "end"
)

// scriptBody is the POSIX sh poll script. df runs under timeout(1) when
// available because a stale network mount can hang it indefinitely.
const scriptBody = `s() { printf '\n` + markerPrefix + `%s:%s\n' "$N" "$1"; }
s ` + secStat + `; cat /proc/stat 2>/dev/null
s ` + secMeminfo + `; cat /proc/meminfo 2>/dev/null
s ` + secLoadavg + `; cat /proc/loadavg 2>/dev/null
s ` + secUptime + `; cat /proc/uptime 2>/dev/null
s ` + secNetDev + `; cat /proc/net/dev 2>/dev/null
s ` + secDF + `
if command -v timeout >/dev/null 2>&1; then T='timeout 3'; else T=''; fi
$T df -kPT 2>/dev/null || $T df -kP 2>/dev/null
s ` + secUname + `; uname -sr 2>/dev/null
s ` + secOS + `; cat /etc/os-release 2>/dev/null || cat /usr/lib/os-release 2>/dev/null
s ` + secHostname + `; hostname 2>/dev/null || cat /proc/sys/kernel/hostname 2>/dev/null
s ` + secPageSize + `; getconf PAGESIZE 2>/dev/null || echo 4096
s ` + secClkTck + `; getconf CLK_TCK 2>/dev/null || echo 100
s ` + secOwners + `; stat -c '%u %n' /proc/[0-9]* 2>/dev/null
s ` + secPasswd + `; cut -d: -f1,3 /etc/passwd 2>/dev/null
s ` + secPidStat + `; cat /proc/[0-9]*/stat 2>/dev/null
s ` + secEnd + `
exit 0
`

// newNonce returns a random token safe to embed unquoted in sh.
func newNonce() string {
	return strings.ToLower(rand.Text())
}

// pollScript returns the script for one poll.
func pollScript(nonce string) string {
	return "N=" + nonce + "\n" + scriptBody
}

// splitSections splits script output into named sections. Text before
// the first marker (e.g. from shell startup files) is ignored, as are
// markers with a different nonce.
func splitSections(out, nonce string) map[string]string {
	marker := "\n" + markerPrefix + nonce + ":"
	sections := make(map[string]string)
	text := "\n" + out
	for {
		i := strings.Index(text, marker)
		if i < 0 {
			return sections
		}
		text = text[i+len(marker):]
		name, rest, _ := strings.Cut(text, "\n")
		name = strings.TrimSpace(name)
		body := rest
		if j := strings.Index(rest, marker); j >= 0 {
			body = rest[:j]
		}
		if _, dup := sections[name]; !dup {
			sections[name] = body
		}
		text = rest
	}
}

// Errors describing unusable poll output.
var (
	errIncomplete = errors.New("incomplete output")
	errNoProc     = errors.New("no /proc (not Linux?)")
)

// sample is one parsed poll result including the raw cumulative
// counters needed for rate computation on the next poll.
type sample struct {
	at       time.Time
	uptime   float64
	cpu      cpuTimes
	cores    []coreTimes
	mem      metrics.Memory
	load     metrics.Load
	ifaces   map[string]ifaceCounters
	disks    []metrics.Disk
	procs    []pidStat
	owners   map[int]int
	users    map[int]string
	pageSize uint64
	clkTck   uint64
	hostname string
	osName   string
	system   string
	kernel   string
}

// parseSample parses the output of pollScript(nonce). It fails only when
// the output is truncated or the essential /proc files are missing;
// optional sections degrade to zero values.
func parseSample(out, nonce string, at time.Time) (*sample, error) {
	sec := splitSections(out, nonce)
	if _, ok := sec[secEnd]; !ok {
		if len(sec) == 0 {
			return nil, fmt.Errorf("%w: no section markers", errIncomplete)
		}
		return nil, errIncomplete
	}
	cpu, cores, err := parseStat(sec[secStat])
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errNoProc, err)
	}
	mem, err := parseMeminfo(sec[secMeminfo])
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errNoProc, err)
	}
	s := &sample{
		at:       at,
		cpu:      cpu,
		cores:    cores,
		mem:      mem,
		ifaces:   parseNetDev(sec[secNetDev]),
		disks:    parseDF(sec[secDF]),
		procs:    parsePidStats(sec[secPidStat]),
		owners:   parseOwners(sec[secOwners]),
		users:    parsePasswd(sec[secPasswd]),
		pageSize: parsePositiveInt(sec[secPageSize], 4096),
		clkTck:   parsePositiveInt(sec[secClkTck], 100),
		hostname: cleanString(parseFirstLine(sec[secHostname])),
		osName:   parseOSRelease(sec[secOS]),
	}
	s.load, _ = parseLoadavg(sec[secLoadavg])
	s.uptime, _ = parseUptime(sec[secUptime])
	s.system, s.kernel = parseUname(sec[secUname])
	return s, nil
}

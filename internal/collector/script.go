package collector

import (
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Aepzie/vigil/internal/metrics"
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
	secPasswd   = "passwd"
	secProcs    = "procs"
	secEnd      = "end"
)

// procsAWK turns `ls -ln <R>` into one line per process (see parseProcs).
// The pid comes from the directory name and the owner from ls; stat is
// read with getline, which skips processes that exited in the meantime,
// and its lines are joined so a newline in comm cannot start a record.
// comm lies between the first '(' and the last ')' because the fields
// after it are numeric. Only the fields vigil needs are printed, as the
// original strings so 64-bit counters keep their precision.
const procsAWK = `$1 ~ /^d/ && $NF ~ /^[0-9]+$/ {
	f = R "/" $NF "/stat"; r = ""; n = 0
	while ((getline l < f) > 0) r = (n++ ? r " " : "") l
	close(f)
	o = index(r, "(")
	if (o < 3 || !match(r, /\)[^)]*$/) || split(substr(r, RSTART + 1), t, " ") < 22) next
	print $NF, $3, substr(r, 1, o - 2), t[12], t[13], t[20], t[22], substr(r, o + 1, RSTART - o - 1)
}`

// procsCommand lists the processes under root (normally /proc) with
// procsAWK. Reading the pid list from ls instead of a /proc/[0-9]* glob
// keeps the command line short however many processes there are.
func procsCommand(root string) string {
	return "ls -ln " + root + " 2>/dev/null | awk -v R=" + root + " '" + procsAWK + "'"
}

// dfScript prints the filesystem table, at most one df run's worth.
//
// df -l reports local filesystems only, so a hung network mount (NFS,
// CIFS, sshfs) can neither stall the poll nor, because GNU df prints its
// table only at the end, wipe out the local disks; network shares are
// best monitored on their server. df still runs under timeout(1) when
// available, with SIGKILL since a process blocked on a hard mount
// ignores SIGTERM; busybox before 1.30 spells the duration "-t 3".
//
// busybox df has no -l and may lack -T. An unknown option makes df exit
// 1 without output, and only then is the next variant tried: a timeout
// (status 124 or 137) or a partial failure with output is final, so a
// poll never waits for more than one df.
const dfScript = `T=
if timeout -s KILL 3 true >/dev/null 2>&1; then T='timeout -s KILL 3'
elif timeout -s KILL -t 3 true >/dev/null 2>&1; then T='timeout -s KILL -t 3'; fi
D() { $T df "$@" 2>/dev/null; }
o=$(D -kPTl); r=$?
if [ $r -eq 1 ] && [ -z "$o" ]; then o=$(D -kPT); r=$?; fi
if [ $r -eq 1 ] && [ -z "$o" ]; then o=$(D -kP); fi
printf '%s\n' "$o"`

// scriptBody is the POSIX sh poll script.
var scriptBody = `s() { printf '\n` + markerPrefix + `%s:%s\n' "$N" "$1"; }
s ` + secStat + `; cat /proc/stat 2>/dev/null
s ` + secMeminfo + `; cat /proc/meminfo 2>/dev/null
s ` + secLoadavg + `; cat /proc/loadavg 2>/dev/null
s ` + secUptime + `; cat /proc/uptime 2>/dev/null
s ` + secNetDev + `; cat /proc/net/dev 2>/dev/null
s ` + secDF + `
` + dfScript + `
s ` + secUname + `; uname -sr 2>/dev/null
s ` + secOS + `; cat /etc/os-release 2>/dev/null || cat /usr/lib/os-release 2>/dev/null
s ` + secHostname + `; hostname 2>/dev/null || cat /proc/sys/kernel/hostname 2>/dev/null
s ` + secPageSize + `; getconf PAGESIZE 2>/dev/null || echo 4096
s ` + secClkTck + `; getconf CLK_TCK 2>/dev/null || echo 100
s ` + secPasswd + `; cut -d: -f1,3 /etc/passwd 2>/dev/null
s ` + secProcs + `; ` + procsCommand("/proc") + `
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
	users    map[int]string
	pageSize uint64
	clkTck   uint64
	hostname string
	osName   string
	system   string
	kernel   string

	// procsTruncated marks procs as partial: the host listed more than
	// maxProcRecords processes and only the first ones were kept. CPU
	// rates of the kept processes stay correct.
	procsTruncated bool
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
		procs:    parseProcs(sec[secProcs]),
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

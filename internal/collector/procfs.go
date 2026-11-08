package collector

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/CraguS/vigil/internal/metrics"
	"github.com/CraguS/vigil/internal/sanitize"
)

// Bounds applied to untrusted remote output.
const (
	maxStringLen = 256
	maxDisks     = 64
	maxCores     = 4096
)

// cpuTimes holds cumulative jiffies of one /proc/stat cpu line. Guest
// time is already included in user and nice and is therefore ignored.
type cpuTimes struct {
	user, nice, system, idle, iowait, irq, softirq, steal uint64
}

func (c cpuTimes) total() uint64 {
	return c.user + c.nice + c.system + c.idle + c.iowait + c.irq + c.softirq + c.steal
}

// coreTimes is one per-core line, identified by its cpuN index.
type coreTimes struct {
	id    int
	times cpuTimes
}

// parseStat parses /proc/stat into the aggregate cpu line and per-core
// lines (sorted by core index).
func parseStat(text string) (cpuTimes, []coreTimes, error) {
	var agg cpuTimes
	var cores []coreTimes
	found := false
	for line := range strings.Lines(text) {
		fields := strings.Fields(line)
		if len(fields) < 5 || !strings.HasPrefix(fields[0], "cpu") {
			continue
		}
		times, ok := parseCPUFields(fields[1:])
		if !ok {
			continue
		}
		if fields[0] == "cpu" {
			agg, found = times, true
			continue
		}
		id, err := strconv.Atoi(fields[0][3:])
		if err != nil || id < 0 || len(cores) >= maxCores {
			continue
		}
		cores = append(cores, coreTimes{id: id, times: times})
	}
	if !found {
		return cpuTimes{}, nil, errors.New("/proc/stat: no cpu line")
	}
	slices.SortStableFunc(cores, func(a, b coreTimes) int { return a.id - b.id })
	cores = slices.CompactFunc(cores, func(a, b coreTimes) bool { return a.id == b.id })
	return agg, cores, nil
}

// parseCPUFields reads user nice system idle [iowait irq softirq steal];
// older kernels print fewer columns, which count as zero.
func parseCPUFields(fields []string) (cpuTimes, bool) {
	var v [8]uint64
	for i := range min(len(fields), len(v)) {
		n, err := strconv.ParseUint(fields[i], 10, 64)
		if err != nil {
			return cpuTimes{}, false
		}
		v[i] = n
	}
	return cpuTimes{v[0], v[1], v[2], v[3], v[4], v[5], v[6], v[7]}, true
}

// parseMeminfo parses /proc/meminfo (values in kB) into bytes. Kernels
// before 3.14 lack MemAvailable; Free+Buffers+Cached approximates it.
func parseMeminfo(text string) (metrics.Memory, error) {
	vals := make(map[string]uint64)
	for line := range strings.Lines(text) {
		key, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		n, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			continue
		}
		if len(fields) > 1 && strings.EqualFold(fields[1], "kB") {
			n = satMul(n, 1024)
		}
		vals[strings.TrimSpace(key)] = n
	}
	total, ok := vals["MemTotal"]
	if !ok || total == 0 {
		return metrics.Memory{}, errors.New("/proc/meminfo: no MemTotal")
	}
	avail, ok := vals["MemAvailable"]
	if !ok {
		avail = satAdd(satAdd(vals["MemFree"], vals["Buffers"]), vals["Cached"])
	}
	avail = min(avail, total)
	swapTotal := vals["SwapTotal"]
	swapFree := min(vals["SwapFree"], swapTotal)
	return metrics.Memory{
		Total:     total,
		Used:      total - avail,
		Available: avail,
		Buffers:   vals["Buffers"],
		Cached:    vals["Cached"],
		SwapTotal: swapTotal,
		SwapUsed:  swapTotal - swapFree,
	}, nil
}

// parseLoadavg parses "0.52 0.58 0.59 2/1234 5678".
func parseLoadavg(text string) (metrics.Load, error) {
	fields := strings.Fields(text)
	if len(fields) < 3 {
		return metrics.Load{}, fmt.Errorf("/proc/loadavg: %d fields", len(fields))
	}
	var v [3]float64
	for i := range v {
		f, err := parseNonNegFloat(fields[i])
		if err != nil {
			return metrics.Load{}, fmt.Errorf("/proc/loadavg: %w", err)
		}
		v[i] = f
	}
	return metrics.Load{One: v[0], Five: v[1], Fifteen: v[2]}, nil
}

// parseUptime returns the first field of /proc/uptime in seconds.
func parseUptime(text string) (float64, error) {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return 0, errors.New("/proc/uptime: empty")
	}
	f, err := parseNonNegFloat(fields[0])
	if err != nil {
		return 0, fmt.Errorf("/proc/uptime: %w", err)
	}
	return f, nil
}

func parseNonNegFloat(s string) (float64, error) {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, err
	}
	if f < 0 || f != f || f > 1e15 {
		return 0, fmt.Errorf("value out of range: %q", s)
	}
	return f, nil
}

// ifaceCounters are cumulative byte counters of one interface.
type ifaceCounters struct {
	rx, tx uint64
}

// virtualIfacePrefixes are excluded from network totals: loopback and
// container/VM plumbing whose traffic is also counted on a real NIC.
// Tunnels (tun, wg) carry real traffic and are kept.
var virtualIfacePrefixes = []string{"veth", "docker", "br-", "virbr", "cni", "flannel", "cali", "kube-", "vnet"}

func countedIface(name string) bool {
	if name == "lo" || name == "" {
		return false
	}
	for _, p := range virtualIfacePrefixes {
		if strings.HasPrefix(name, p) {
			return false
		}
	}
	return true
}

// parseNetDev parses /proc/net/dev and returns the counters of the
// interfaces that count towards the network totals.
func parseNetDev(text string) map[string]ifaceCounters {
	out := make(map[string]ifaceCounters)
	for line := range strings.Lines(text) {
		i := strings.LastIndexByte(line, ':')
		if i < 0 {
			continue
		}
		name := strings.TrimSpace(line[:i])
		fields := strings.Fields(line[i+1:])
		if len(fields) < 9 || !countedIface(name) {
			continue
		}
		rx, err1 := strconv.ParseUint(fields[0], 10, 64)
		tx, err2 := strconv.ParseUint(fields[8], 10, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		out[name] = ifaceCounters{rx: rx, tx: tx}
	}
	return out
}

// pseudoFSTypes never represent real storage.
var pseudoFSTypes = map[string]bool{
	"tmpfs": true, "devtmpfs": true, "overlay": true, "squashfs": true,
	"proc": true, "sysfs": true, "cgroup": true, "cgroup2": true,
	"ramfs": true, "efivarfs": true, "nsfs": true, "tracefs": true,
	"autofs": true, "devpts": true, "securityfs": true, "pstore": true,
	"bpf": true, "debugfs": true, "mqueue": true, "hugetlbfs": true,
	"configfs": true, "fusectl": true, "binfmt_misc": true,
	"rpc_pipefs": true, "rootfs": true, "fuse.lxcfs": true,
	"fuse.gvfsd-fuse": true, "fuse.portal": true, "fuse.snapfuse": true,
}

// pseudoDevices identify pseudo filesystems when df has no type column.
var pseudoDevices = map[string]bool{
	"tmpfs": true, "devtmpfs": true, "overlay": true, "none": true,
	"udev": true, "shm": true, "rootfs": true, "proc": true, "sysfs": true,
}

// parseDF parses `df -kPT` (or `df -kP`, detected from the header) output.
// Pseudo filesystems are skipped and bind mounts of the same device are
// collapsed to the shortest mount point. Total is Used+Available, so
// UsedPct matches df's Capacity column (reserved blocks are not usable).
// Mount points may contain spaces. Each header line re-detects the
// format, because the script falls back to `df -kP` when `df -kPT` exits
// non-zero after printing partial output.
func parseDF(text string) []metrics.Disk {
	hasType, seenHeader := false, false
	best := make(map[string]metrics.Disk)
	for line := range strings.Lines(text) {
		line = strings.TrimRight(line, "\r\n")
		if fields := strings.Fields(line); len(fields) > 1 && fields[0] == "Filesystem" {
			hasType, seenHeader = fields[1] == "Type", true
			continue
		}
		if !seenHeader {
			continue
		}
		disk, ok := parseDFLine(line, hasType)
		if !ok {
			continue
		}
		if pseudoFSTypes[disk.FSType] || (!hasType && pseudoDevices[disk.Device]) {
			continue
		}
		if prev, dup := best[disk.Device]; dup && len(prev.Mount) <= len(disk.Mount) {
			continue
		}
		best[disk.Device] = disk
	}
	disks := make([]metrics.Disk, 0, len(best))
	for _, d := range best {
		d.Device = cleanString(d.Device)
		d.Mount = cleanString(d.Mount)
		d.FSType = cleanString(d.FSType)
		disks = append(disks, d)
	}
	slices.SortFunc(disks, func(a, b metrics.Disk) int { return strings.Compare(a.Mount, b.Mount) })
	if len(disks) > maxDisks {
		disks = disks[:maxDisks]
	}
	return disks
}

func parseDFLine(line string, hasType bool) (metrics.Disk, bool) {
	lead := 1
	if hasType {
		lead = 2
	}
	tokens, rest := cutFields(line, lead+4)
	if len(tokens) < lead+4 || rest == "" || !strings.HasSuffix(tokens[lead+3], "%") {
		return metrics.Disk{}, false
	}
	var n [3]uint64
	for i := range n {
		v, err := strconv.ParseUint(tokens[lead+i], 10, 64)
		if err != nil {
			return metrics.Disk{}, false
		}
		n[i] = v
	}
	used, avail := satMul(n[1], 1024), satMul(n[2], 1024)
	total := satAdd(used, avail)
	if n[0] == 0 || total == 0 {
		return metrics.Disk{}, false
	}
	d := metrics.Disk{Device: tokens[0], Mount: rest, Total: total, Used: used}
	if hasType {
		d.FSType = tokens[1]
	}
	return d, true
}

// cutFields splits off the first n whitespace-separated fields and
// returns the remainder with its inner spacing preserved.
func cutFields(line string, n int) ([]string, string) {
	var out []string
	rest := line
	for len(out) < n {
		rest = strings.TrimLeft(rest, " \t")
		if rest == "" {
			return out, ""
		}
		end := strings.IndexAny(rest, " \t")
		if end < 0 {
			out = append(out, rest)
			return out, ""
		}
		out = append(out, rest[:end])
		rest = rest[end:]
	}
	return out, strings.TrimLeft(rest, " \t")
}

// parseOSRelease returns PRETTY_NAME, falling back to NAME VERSION_ID.
func parseOSRelease(text string) string {
	vals := make(map[string]string)
	for line := range strings.Lines(text) {
		key, val, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || strings.HasPrefix(key, "#") {
			continue
		}
		vals[key] = unquoteShell(val)
	}
	if v := vals["PRETTY_NAME"]; v != "" {
		return cleanString(v)
	}
	return cleanString(strings.TrimSpace(vals["NAME"] + " " + vals["VERSION_ID"]))
}

// unquoteShell undoes the quoting allowed by os-release(5).
func unquoteShell(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'' {
		return s[1 : len(s)-1]
	}
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// parseUname splits `uname -sr` into system name and kernel release.
func parseUname(text string) (system, release string) {
	fields := strings.Fields(text)
	switch len(fields) {
	case 0:
		return "", ""
	case 1:
		return cleanString(fields[0]), ""
	default:
		return cleanString(fields[0]), cleanString(fields[1])
	}
}

// parseFirstLine returns the trimmed first non-empty line.
func parseFirstLine(text string) string {
	for line := range strings.Lines(text) {
		if s := strings.TrimSpace(line); s != "" {
			return s
		}
	}
	return ""
}

// parsePositiveInt parses the first line as a positive integer, or
// returns fallback.
func parsePositiveInt(text string, fallback uint64) uint64 {
	n, err := strconv.ParseUint(parseFirstLine(text), 10, 64)
	if err != nil || n == 0 {
		return fallback
	}
	return n
}

// pidStat holds the per-process fields vigil uses: the owner of
// /proc/<pid> and fields of /proc/<pid>/stat.
type pidStat struct {
	pid   int
	uid   int
	comm  string
	ticks uint64 // utime + stime
	start uint64 // starttime in clock ticks since boot
	rss   uint64 // resident pages
}

// procRecordFields is the number of fields before comm in a procs record.
const procRecordFields = 7

// parseProcs parses the procs section printed by procsCommand, one record
// per process:
//
//	<pid> <uid> <stat pid> <utime> <stime> <starttime> <rss> <comm>
//
// pid is the /proc directory name and uid its owner; the rest comes from
// /proc/<pid>/stat, whose newlines (comm may contain any byte but NUL)
// were replaced by spaces remotely, so a process cannot split its record
// or print extra ones. Records whose stat pid differs from the directory
// pid, malformed records and repeated pids are dropped regardless.
func parseProcs(text string) []pidStat {
	var out []pidStat
	seen := make(map[int]bool)
	for line := range strings.Lines(text) {
		p, ok := parseProcRecord(line)
		if !ok || seen[p.pid] {
			continue
		}
		seen[p.pid] = true
		out = append(out, p)
	}
	return out
}

func parseProcRecord(line string) (pidStat, bool) {
	tokens, comm := cutFields(strings.TrimRight(line, "\r\n"), procRecordFields)
	if len(tokens) < procRecordFields {
		return pidStat{}, false
	}
	pid, err := strconv.Atoi(tokens[0])
	if err != nil || pid <= 0 || tokens[2] != tokens[0] {
		return pidStat{}, false
	}
	uid, err := strconv.Atoi(tokens[1])
	if err != nil || uid < 0 {
		return pidStat{}, false
	}
	var v [4]uint64
	for i := range v {
		n, err := strconv.ParseUint(tokens[3+i], 10, 64)
		if err != nil {
			return pidStat{}, false
		}
		v[i] = n
	}
	return pidStat{pid: pid, uid: uid, comm: comm, ticks: satAdd(v[0], v[1]), start: v[2], rss: v[3]}, true
}

// parsePasswd parses "name:uid" lines (cut -d: -f1,3 /etc/passwd).
func parsePasswd(text string) map[int]string {
	out := make(map[int]string)
	for line := range strings.Lines(text) {
		name, uidStr, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok || name == "" {
			continue
		}
		uid, err := strconv.Atoi(uidStr)
		if err != nil || uid < 0 {
			continue
		}
		if _, dup := out[uid]; !dup {
			out[uid] = cleanString(name)
		}
	}
	return out
}

// cleanString sanitizes untrusted remote text into one bounded line.
func cleanString(s string) string {
	s = strings.TrimSpace(sanitize.Line(s))
	if len(s) <= maxStringLen {
		return s
	}
	r := []rune(s)
	if len(r) <= maxStringLen {
		return s
	}
	return string(r[:maxStringLen])
}

func satAdd(a, b uint64) uint64 {
	if a > ^uint64(0)-b {
		return ^uint64(0)
	}
	return a + b
}

func satMul(a, b uint64) uint64 {
	if a != 0 && b > ^uint64(0)/a {
		return ^uint64(0)
	}
	return a * b
}

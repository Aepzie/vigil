package collector

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/CraguS/vigil/internal/metrics"
)

func readFixture(t testing.TB, parts ...string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(append([]string{"testdata"}, parts...)...))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return string(raw)
}

func TestParseStat(t *testing.T) {
	tests := []struct {
		name      string
		text      string
		wantAgg   cpuTimes
		wantCores []int
		wantErr   bool
	}{
		{
			name:      "modern kernel",
			text:      readFixture(t, "poll1", "stat"),
			wantAgg:   cpuTimes{10000, 500, 3000, 80000, 1000, 0, 200, 100},
			wantCores: []int{0, 1, 2, 3},
		},
		{
			name:      "2.4 kernel with four columns",
			text:      readFixture(t, "stat_old_kernel"),
			wantAgg:   cpuTimes{user: 1000, system: 500, idle: 8000},
			wantCores: []int{0},
		},
		{
			name:      "cores out of order and offline gap",
			text:      "cpu 4 0 4 8\ncpu3 1 0 1 2\ncpu0 1 0 1 2\ncpu0 9 9 9 9\n",
			wantAgg:   cpuTimes{user: 4, system: 4, idle: 8},
			wantCores: []int{0, 3},
		},
		{name: "empty", text: "", wantErr: true},
		{name: "garbage numbers", text: "cpu a b c d\n", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agg, cores, err := parseStat(tt.text)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if agg != tt.wantAgg {
				t.Errorf("agg = %+v, want %+v", agg, tt.wantAgg)
			}
			var ids []int
			for _, c := range cores {
				ids = append(ids, c.id)
			}
			if !reflect.DeepEqual(ids, tt.wantCores) {
				t.Errorf("core ids = %v, want %v", ids, tt.wantCores)
			}
		})
	}
}

func TestParseMeminfo(t *testing.T) {
	const kb = 1024
	tests := []struct {
		name    string
		text    string
		want    metrics.Memory
		wantErr bool
	}{
		{
			name: "MemAvailable present",
			text: readFixture(t, "poll1", "meminfo"),
			want: metrics.Memory{
				Total: 8000000 * kb, Available: 5000000 * kb, Used: 3000000 * kb,
				Buffers: 200000 * kb, Cached: 3000000 * kb,
				SwapTotal: 2000000 * kb, SwapUsed: 500000 * kb,
			},
		},
		{
			name: "old kernel falls back to free+buffers+cached",
			text: readFixture(t, "meminfo_old_kernel"),
			want: metrics.Memory{
				Total: 2048000 * kb, Available: 896000 * kb, Used: 1152000 * kb,
				Buffers: 128000 * kb, Cached: 256000 * kb,
			},
		},
		{
			name: "available larger than total is clamped",
			text: "MemTotal: 100 kB\nMemAvailable: 500 kB\nSwapTotal: 10 kB\nSwapFree: 20 kB\n",
			want: metrics.Memory{Total: 100 * kb, Available: 100 * kb, SwapTotal: 10 * kb},
		},
		{name: "missing MemTotal", text: "MemFree: 1 kB\n", wantErr: true},
		{name: "empty", text: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseMeminfo(tt.text)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("got  %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

func TestParseLoadavgAndUptime(t *testing.T) {
	load, err := parseLoadavg(readFixture(t, "poll2", "loadavg"))
	if err != nil || load != (metrics.Load{One: 1.25, Five: 0.73, Fifteen: 0.63}) {
		t.Errorf("parseLoadavg = %+v, %v", load, err)
	}
	for _, bad := range []string{"", "1 2", "a b c", "NaN 1 1", "-1 1 1"} {
		if _, err := parseLoadavg(bad); err == nil {
			t.Errorf("parseLoadavg(%q) succeeded", bad)
		}
	}
	up, err := parseUptime(readFixture(t, "poll1", "uptime"))
	if err != nil || up != 12345.67 {
		t.Errorf("parseUptime = %v, %v", up, err)
	}
	for _, bad := range []string{"", "x", "Inf 1", "1e300"} {
		if _, err := parseUptime(bad); err == nil {
			t.Errorf("parseUptime(%q) succeeded", bad)
		}
	}
}

func TestParseNetDev(t *testing.T) {
	got := parseNetDev(readFixture(t, "poll1", "netdev"))
	want := map[string]ifaceCounters{
		"eth0": {rx: 1000000000, tx: 500000000},
		"wg0":  {rx: 2000000, tx: 3000000},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseNetDev = %+v, want %+v", got, want)
	}

	glued := "  eth1:123456789012 1 0 0 0 0 0 0 98765 1 0 0 0 0 0 0\n  eth2: bad 1 0 0 0 0 0 0 1 1 0 0 0 0 0 0\nshort: 1 2\n"
	got = parseNetDev(glued)
	if len(got) != 1 || got["eth1"] != (ifaceCounters{rx: 123456789012, tx: 98765}) {
		t.Errorf("glued/garbage parse = %+v", got)
	}
}

func TestCountedIface(t *testing.T) {
	tests := map[string]bool{
		"lo": false, "eth0": true, "ens3": true, "wg0": true, "tun0": true, "bond0": true,
		"veth0a1b": false, "docker0": false, "br-1234": false, "virbr0": false, "cni0": false,
		"flannel.1": false, "cali123": false, "kube-ipvs0": false, "vnet3": false, "": false,
	}
	for name, want := range tests {
		if got := countedIface(name); got != want {
			t.Errorf("countedIface(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestParseDF(t *testing.T) {
	const kb = 1024
	tests := []struct {
		name string
		text string
		want []metrics.Disk
	}{
		{
			name: "df -kPT",
			text: readFixture(t, "poll1", "df"),
			want: []metrics.Disk{
				{Device: "/dev/sda1", Mount: "/", FSType: "ext4", Total: 47500000 * kb, Used: 30000000 * kb},
				{Device: "/dev/sda15", Mount: "/boot/efi", FSType: "vfat", Total: 106859 * kb, Used: 6182 * kb},
				{Device: "/dev/sdb1", Mount: "/mnt/My  Backups", FSType: "xfs", Total: 100000000 * kb, Used: 90000000 * kb},
				{Device: "server:/export", Mount: "/mnt/nfs", FSType: "nfs4", Total: 200000000 * kb, Used: 100000000 * kb},
			},
		},
		{
			name: "busybox df -kP without type column",
			text: readFixture(t, "df_busybox"),
			want: []metrics.Disk{
				{Device: "/dev/root", Mount: "/", Total: 7156789 * kb, Used: 3456789 * kb},
				{Device: "/dev/mmcblk0p1", Mount: "/boot", Total: 258095 * kb, Used: 51418 * kb},
			},
		},
		{
			name: "hostile content",
			text: "Filesystem Type 1024-blocks Used Available Capacity Mounted on\n" +
				"/dev/x ext4 100 50 50 50% /mnt/\x1b]0;pwned\x07evil\n" +
				"/dev/y ext4 100 50 50 50%\n" +
				"/dev/z ext4 abc 50 50 50% /z\n" +
				"/dev/w ext4 0 0 0 0% /w\n",
			want: []metrics.Disk{
				{Device: "/dev/x", Mount: "/mnt/evil", FSType: "ext4", Total: 100 * kb, Used: 50 * kb},
			},
		},
		{
			name: "partial df -kPT followed by df -kP fallback",
			text: "Filesystem Type 1024-blocks Used Available Capacity Mounted on\n" +
				"/dev/sda1 ext4 100 40 60 40% /\n" +
				"Filesystem 1024-blocks Used Available Capacity Mounted on\n" +
				"/dev/sda1 100 40 60 40% /\n" +
				"tmpfs 100 0 100 0% /run\n" +
				"/dev/sdb1 200 50 150 25% /data dir\n",
			want: []metrics.Disk{
				{Device: "/dev/sda1", Mount: "/", FSType: "ext4", Total: 100 * kb, Used: 40 * kb},
				{Device: "/dev/sdb1", Mount: "/data dir", Total: 200 * kb, Used: 50 * kb},
			},
		},
		{name: "no header", text: "/dev/sda1 ext4 1 1 0 100% /\n", want: []metrics.Disk{}},
		{name: "empty", text: "", want: []metrics.Disk{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseDF(tt.text)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got:\n%+v\nwant:\n%+v", got, tt.want)
			}
		})
	}
}

func TestParseDFBoundsDiskCount(t *testing.T) {
	var b strings.Builder
	b.WriteString("Filesystem Type 1024-blocks Used Available Capacity Mounted on\n")
	for i := range maxDisks * 2 {
		b.WriteString("/dev/d" + strings.Repeat("x", i) + " ext4 100 1 99 1% /m" + strings.Repeat("x", i) + "\n")
	}
	if got := len(parseDF(b.String())); got != maxDisks {
		t.Errorf("disks = %d, want %d", got, maxDisks)
	}
}

func TestParseOSRelease(t *testing.T) {
	tests := []struct {
		name, text, want string
	}{
		{"ubuntu", readFixture(t, "poll1", "osrelease"), "Ubuntu 24.04.1 LTS"},
		{"no pretty name", "NAME=Alpine\nVERSION_ID=3.20.3\n", "Alpine 3.20.3"},
		{"single quotes", "PRETTY_NAME='Arch Linux'\n", "Arch Linux"},
		{"escapes", `PRETTY_NAME="Weird \"Quoted\" \$Name\\"` + "\n", `Weird "Quoted" $Name\`},
		{"control chars", "PRETTY_NAME=\"Evil\x1b[2J OS\"\n", "Evil OS"},
		{"comment and blank", "# PRETTY_NAME=x\n\nNAME=Debian\n", "Debian"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseOSRelease(tt.text); got != tt.want {
				t.Errorf("parseOSRelease = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseUname(t *testing.T) {
	tests := []struct{ in, sys, rel string }{
		{"Linux 6.8.0-45-generic\n", "Linux", "6.8.0-45-generic"},
		{"Linux\n", "Linux", ""},
		{"", "", ""},
	}
	for _, tt := range tests {
		sys, rel := parseUname(tt.in)
		if sys != tt.sys || rel != tt.rel {
			t.Errorf("parseUname(%q) = %q, %q", tt.in, sys, rel)
		}
	}
}

func TestParseProcs(t *testing.T) {
	procs := parseProcs(readFixture(t, "poll2", "procs"))
	byPID := make(map[int]pidStat)
	for _, p := range procs {
		byPID[p.pid] = p
	}
	if len(procs) != 19 || len(byPID) != len(procs) {
		t.Errorf("parsed %d processes (%d distinct), want 19", len(procs), len(byPID))
	}
	tests := []struct {
		pid  int
		want pidStat
	}{
		{1, pidStat{pid: 1, uid: 0, comm: "systemd", ticks: 402, start: 2, rss: 3000}},
		{2345, pidStat{pid: 2345, uid: 1000, comm: "weird) b", ticks: 100, start: 9100, rss: 1000}},
		{1500, pidStat{pid: 1500, uid: 4242, comm: "tmux: server", ticks: 20, start: 9300, rss: 2000}},
		{4567, pidStat{pid: 4567, uid: 0, comm: "kworker/0:1-events", ticks: 15, start: 3, rss: 0}},
		{8000, pidStat{pid: 8000, uid: 1000, comm: "evil name", ticks: 0, start: 9600, rss: 10}},
		{666, pidStat{pid: 666, uid: 1000, comm: "1 (sshd", ticks: 0, start: 9650, rss: 20}},
		{9001, pidStat{pid: 9001, uid: 1000, comm: "a (b) c", ticks: 0, start: 9660, rss: 30}},
	}
	for _, tt := range tests {
		if got := byPID[tt.pid]; got != tt.want {
			t.Errorf("pid %d = %+v, want %+v", tt.pid, got, tt.want)
		}
	}
	if _, ok := byPID[7777]; ok {
		t.Error("record whose stat pid differs from its /proc pid was accepted")
	}
	for _, bad := range []string{
		"",
		"garbage line",
		"7 0 7 1 2 3",
		"7 0 8 1 2 3 4 forged",
		"0 0 0 1 2 3 4 zero",
		"x 0 x 1 2 3 4 nan",
		"7 -1 7 1 2 3 4 neguid",
		"7 0 7 1 -2 3 4 negticks",
		"7 0 7 1 2 3 18446744073709551616 overflow",
	} {
		if p, ok := parseProcRecord(bad); ok {
			t.Errorf("parseProcRecord(%q) = %+v", bad, p)
		}
	}
	if p, ok := parseProcRecord("7 0 7 18446744073709551615 9 3 4\r\n"); !ok || p.comm != "" || p.ticks != ^uint64(0) {
		t.Errorf("empty comm / saturated ticks: %+v, %v", p, ok)
	}
}

func TestParsePasswd(t *testing.T) {
	users := parsePasswd("root:0\nalice:1000\ntoor:0\n:5\nbob:x\nevil\x1b[1m:7\n")
	want := map[int]string{0: "root", 1000: "alice", 7: "evil"}
	if !reflect.DeepEqual(users, want) {
		t.Errorf("users = %v, want %v", users, want)
	}
}

func TestParsePositiveInt(t *testing.T) {
	tests := []struct {
		in   string
		want uint64
	}{
		{"4096\n", 4096}, {"\n16384\n", 16384}, {"", 7}, {"0", 7}, {"-1", 7}, {"abc", 7},
	}
	for _, tt := range tests {
		if got := parsePositiveInt(tt.in, 7); got != tt.want {
			t.Errorf("parsePositiveInt(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestCleanString(t *testing.T) {
	long := strings.Repeat("ä", maxStringLen+10)
	if got := cleanString(long); len([]rune(got)) != maxStringLen {
		t.Errorf("cleanString did not bound length: %d runes", len([]rune(got)))
	}
	if got := cleanString("  a\tb\x00c\x1b[31m\n"); got != "a bc" {
		t.Errorf("cleanString = %q", got)
	}
	if got := cleanString("\xff\xfe"); !strings.Contains(got, "�") {
		t.Errorf("invalid UTF-8 not replaced: %q", got)
	}
}

func TestSaturatingMath(t *testing.T) {
	const maxU = ^uint64(0)
	if satMul(maxU/2, 4) != maxU || satMul(0, maxU) != 0 || satMul(3, 4) != 12 {
		t.Error("satMul")
	}
	if satAdd(maxU, 1) != maxU || satAdd(1, 2) != 3 {
		t.Error("satAdd")
	}
}

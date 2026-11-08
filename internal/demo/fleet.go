package demo

import (
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/Aepzie/vigil/internal/store"
)

const (
	kib = uint64(1) << 10
	mib = kib << 10
	gib = mib << 10
	tib = gib << 10
)

// profile describes the static character of a simulated server.
type profile struct {
	os     string
	kernel string
	cores  int
	uptime time.Duration

	// cpuBase is the mean CPU utilization, cpuNoise the random-walk step,
	// burstChance the per-sample probability of a load burst and cpuMin,
	// cpuMax clamp the result.
	cpuBase, cpuNoise, burstChance float64
	cpuMin, cpuMax                 float64
	// daySwing is the diurnal CPU amplitude in points (0 means 12% of
	// cpuBase).
	daySwing float64
	// ioWait is the share of busy CPU spent waiting for I/O; steal the
	// hypervisor steal share.
	ioWait, steal float64

	memTotal  uint64
	memBase   float64
	swapTotal uint64
	swapBase  float64

	disks []diskSpec

	rxBase, txBase float64
	latencyMs      float64

	procs []procSpec

	offline bool
	err     string
}

type diskSpec struct {
	device, mount, fstype string
	total                 uint64
	fill                  float64
	// growth is the fill increase per sample; minFill/maxFill clamp the
	// fill level (0 means 0.01/0.99).
	growth           float64
	minFill, maxFill float64
}

type procSpec struct {
	command string
	user    string
	share   float64
	rss     uint64
}

var commonProcs = []procSpec{
	{command: "/lib/systemd/systemd-journald", user: "root", share: 0.01, rss: 48 * mib},
	{command: "sshd: /usr/sbin/sshd -D", user: "root", share: 0.004, rss: 9 * mib},
	{command: "/lib/systemd/systemd --system", user: "root", share: 0.003, rss: 12 * mib},
	{command: "/usr/bin/node_exporter", user: "nobody", share: 0.006, rss: 22 * mib},
}

// sampleServers is the inventory added by SeedServers. Hosts are RFC 1918,
// RFC 5737 and RFC 3849 documentation addresses or example.internal names.
var sampleServers = []store.Server{
	{Name: "web-01", Host: "10.20.1.11", User: "deploy", Tags: []string{"prod", "web"}},
	{Name: "web-02", Host: "10.20.1.12", User: "deploy", Tags: []string{"prod", "web"}},
	{Name: "api-gateway", Host: "api-gw.example.internal", User: "ops", Tags: []string{"prod", "edge"}},
	{Name: "db-primary", Host: "10.20.2.10", User: "dba", Tags: []string{"prod", "db"}},
	{Name: "db-replica", Host: "10.20.2.11", User: "dba", Tags: []string{"prod", "db", "replica"}},
	{Name: "cache-01", Host: "192.168.40.21", User: "ops", Tags: []string{"prod", "cache"}},
	{Name: "worker-01", Host: "2001:db8:40::31", User: "deploy", Tags: []string{"staging", "worker"}},
	{Name: "ci-runner", Host: "ci-runner.example.internal", Port: 2222, User: "ci", Tags: []string{"staging", "ci"}},
	{Name: "backup-nas", Host: "192.0.2.50", User: "backup", Tags: []string{"backup", "storage"}},
}

func webProfile(cpu float64) profile {
	return profile{
		os: "Ubuntu 24.04.1 LTS", kernel: "6.8.0-49-generic", cores: 8, uptime: 41*24*time.Hour + 3*time.Hour,
		cpuBase: cpu, cpuNoise: 2.2, burstChance: 0.02, cpuMin: 2, cpuMax: 97, ioWait: 0.03, steal: 0.01,
		memTotal: 16 * gib, memBase: 0.58, swapTotal: 2 * gib, swapBase: 0.04,
		disks: []diskSpec{
			{device: "/dev/sda1", mount: "/", fstype: "ext4", total: 80 * gib, fill: 0.42, growth: 2e-7},
			{device: "/dev/sdb1", mount: "/var", fstype: "xfs", total: 200 * gib, fill: 0.61, growth: 1e-6},
		},
		rxBase: 9.5 * float64(mib), txBase: 24 * float64(mib), latencyMs: 1.8,
		procs: []procSpec{
			{command: "nginx: worker process", user: "www-data", share: 0.22, rss: 96 * mib},
			{command: "nginx: worker process", user: "www-data", share: 0.18, rss: 94 * mib},
			{command: "node /srv/app/server.js", user: "deploy", share: 0.38, rss: 1400 * mib},
			{command: "node /srv/app/worker.js", user: "deploy", share: 0.09, rss: 620 * mib},
		},
	}
}

// knownProfiles are the hand-tuned profiles of the sample servers.
var knownProfiles = map[string]func() profile{
	"web-01": func() profile { return webProfile(34) },
	"web-02": func() profile {
		p := webProfile(28)
		p.uptime = 12*24*time.Hour + 7*time.Hour
		p.memBase = 0.52
		return p
	},
	"api-gateway": func() profile {
		return profile{
			os: "Debian GNU/Linux 12 (bookworm)", kernel: "6.1.0-27-amd64", cores: 4, uptime: 97 * 24 * time.Hour,
			cpuBase: 42, cpuNoise: 3, burstChance: 0.035, cpuMin: 4, cpuMax: 98, ioWait: 0.01, steal: 0.02,
			memTotal: 8 * gib, memBase: 0.47, swapTotal: 1 * gib, swapBase: 0.02,
			disks: []diskSpec{
				{device: "/dev/vda1", mount: "/", fstype: "ext4", total: 40 * gib, fill: 0.37, growth: 3e-7},
			},
			rxBase: 31 * float64(mib), txBase: 29 * float64(mib), latencyMs: 0.6,
			procs: []procSpec{
				{command: "envoy -c /etc/envoy/envoy.yaml", user: "envoy", share: 0.61, rss: 310 * mib},
				{command: "/usr/bin/prometheus --config.file=/etc/prometheus.yml", user: "prometheus", share: 0.08, rss: 820 * mib},
			},
		}
	},
	"db-primary": func() profile {
		return profile{
			os: "Rocky Linux 9.4 (Blue Onyx)", kernel: "5.14.0-427.42.1.el9_4.x86_64", cores: 32, uptime: 203 * 24 * time.Hour,
			cpuBase: 47, cpuNoise: 1.8, burstChance: 0.015, cpuMin: 8, cpuMax: 94, ioWait: 0.12,
			memTotal: 128 * gib, memBase: 0.76, swapTotal: 8 * gib, swapBase: 0.11,
			disks: []diskSpec{
				{device: "/dev/nvme0n1p2", mount: "/", fstype: "xfs", total: 100 * gib, fill: 0.31, growth: 1e-7},
				{device: "/dev/nvme1n1", mount: "/data", fstype: "xfs", total: 2 * tib, fill: 0.71, growth: 4e-7},
			},
			rxBase: 42 * float64(mib), txBase: 66 * float64(mib), latencyMs: 0.4,
			procs: []procSpec{
				{command: "postgres: checkpointer", user: "postgres", share: 0.05, rss: 2 * gib},
				{command: "postgres: walwriter", user: "postgres", share: 0.04, rss: 512 * mib},
				{command: "postgres: app app 10.20.1.11(51234) SELECT", user: "postgres", share: 0.31, rss: 3 * gib},
				{command: "postgres: app app 10.20.1.12(48810) idle", user: "postgres", share: 0.18, rss: 3 * gib},
				{command: "/usr/pgsql-16/bin/postgres -D /data/pg16", user: "postgres", share: 0.07, rss: 32 * gib},
				{command: "pgbouncer /etc/pgbouncer/pgbouncer.ini", user: "pgbouncer", share: 0.06, rss: 48 * mib},
			},
		}
	},
	"db-replica": func() profile {
		return profile{
			os: "Rocky Linux 9.4 (Blue Onyx)", kernel: "5.14.0-427.42.1.el9_4.x86_64", cores: 16, uptime: 58 * 24 * time.Hour,
			cpuBase: 22, cpuNoise: 1.5, burstChance: 0.01, cpuMin: 3, cpuMax: 90, ioWait: 0.18,
			memTotal: 64 * gib, memBase: 0.69, swapTotal: 8 * gib, swapBase: 0.06,
			disks: []diskSpec{
				{device: "/dev/nvme0n1p2", mount: "/", fstype: "xfs", total: 100 * gib, fill: 0.29, growth: 1e-7},
				{device: "/dev/nvme1n1", mount: "/data", fstype: "xfs", total: 1 * tib, fill: 0.961, growth: 2e-7, minFill: 0.958, maxFill: 0.968},
			},
			rxBase: 61 * float64(mib), txBase: 3.5 * float64(mib), latencyMs: 0.5,
			procs: []procSpec{
				{command: "postgres: walreceiver streaming 1A/3F2B9C40", user: "postgres", share: 0.21, rss: 256 * mib},
				{command: "postgres: startup recovering 000000010000001A0000003F", user: "postgres", share: 0.34, rss: 1 * gib},
				{command: "/usr/pgsql-16/bin/postgres -D /data/pg16", user: "postgres", share: 0.05, rss: 16 * gib},
			},
		}
	},
	"cache-01": func() profile {
		return profile{
			os: "Debian GNU/Linux 12 (bookworm)", kernel: "6.1.0-27-amd64", cores: 4, uptime: 311 * 24 * time.Hour,
			cpuBase: 14, cpuNoise: 1.2, burstChance: 0.01, cpuMin: 1, cpuMax: 80, ioWait: 0.01,
			memTotal: 64 * gib, memBase: 0.73, swapTotal: 0,
			disks: []diskSpec{
				{device: "/dev/sda1", mount: "/", fstype: "ext4", total: 50 * gib, fill: 0.23, growth: 1e-7},
			},
			rxBase: 18 * float64(mib), txBase: 22 * float64(mib), latencyMs: 0.9,
			procs: []procSpec{
				{command: "/usr/bin/redis-server 0.0.0.0:6379", user: "redis", share: 0.72, rss: 44 * gib},
				{command: "/usr/bin/redis-sentinel *:26379 [sentinel]", user: "redis", share: 0.03, rss: 14 * mib},
			},
		}
	},
	"worker-01": func() profile {
		return profile{
			os: "Ubuntu 22.04.5 LTS", kernel: "5.15.0-125-generic", cores: 12, uptime: 6*24*time.Hour + 19*time.Hour,
			cpuBase: 52, cpuNoise: 3.5, burstChance: 0.03, cpuMin: 5, cpuMax: 97, ioWait: 0.04, steal: 0.03,
			memTotal: 32 * gib, memBase: 0.66, swapTotal: 4 * gib, swapBase: 0.09,
			disks: []diskSpec{
				{device: "/dev/sda1", mount: "/", fstype: "ext4", total: 60 * gib, fill: 0.48, growth: 3e-7},
				{device: "/dev/sdb1", mount: "/var", fstype: "ext4", total: 120 * gib, fill: 0.57, growth: 2e-6},
			},
			rxBase: 6 * float64(mib), txBase: 4 * float64(mib), latencyMs: 12,
			procs: []procSpec{
				{command: "java -Xmx12g -jar /opt/jobs/worker.jar", user: "jobs", share: 0.64, rss: 11 * gib},
				{command: "/usr/bin/dockerd -H fd://", user: "root", share: 0.04, rss: 180 * mib},
				{command: "/usr/bin/containerd", user: "root", share: 0.02, rss: 72 * mib},
			},
		}
	},
	"ci-runner": func() profile {
		return profile{
			os: "Ubuntu 24.04.1 LTS", kernel: "6.8.0-49-generic", cores: 16, uptime: 2*24*time.Hour + 5*time.Hour,
			cpuBase: 89, cpuNoise: 0.6, cpuMin: 84, cpuMax: 93.5, daySwing: 1.5, ioWait: 0.05,
			memTotal: 32 * gib, memBase: 0.71, swapTotal: 4 * gib, swapBase: 0.18,
			disks: []diskSpec{
				{device: "/dev/nvme0n1p2", mount: "/", fstype: "ext4", total: 500 * gib, fill: 0.74, growth: 1e-5, maxFill: 0.84},
			},
			rxBase: 14 * float64(mib), txBase: 2 * float64(mib), latencyMs: 38,
			procs: []procSpec{
				{command: "cc1plus -O2 -o build/obj/engine.o", user: "ci", share: 0.29, rss: 1200 * mib},
				{command: "cc1plus -O2 -o build/obj/render.o", user: "ci", share: 0.27, rss: 980 * mib},
				{command: "/usr/bin/dockerd -H fd://", user: "root", share: 0.06, rss: 210 * mib},
				{command: "gitlab-runner run --working-directory /home/ci", user: "ci", share: 0.03, rss: 64 * mib},
			},
		}
	},
	"backup-nas": func() profile {
		return profile{
			os: "Debian GNU/Linux 12 (bookworm)", kernel: "6.1.0-26-amd64", cores: 4, uptime: 64 * 24 * time.Hour,
			memTotal: 16 * gib, memBase: 0.4,
			disks: []diskSpec{
				{device: "tank/backups", mount: "/srv/backups", fstype: "zfs", total: 16 * tib, fill: 0.67},
			},
			latencyMs: 4,
			offline:   true,
			err:       "connection timed out",
		}
	},
}

// genericRoles back servers that are not part of the sample fleet.
var genericRoles = []string{"web-01", "api-gateway", "worker-01", "cache-01"}

// profileFor returns the profile of a sample server, or a jittered copy of
// a generic role chosen by rng.
func profileFor(name string, rng *rand.Rand) profile {
	if mk, ok := knownProfiles[name]; ok {
		return mk()
	}
	p := knownProfiles[genericRoles[rng.IntN(len(genericRoles))]]()
	p.cpuBase *= 0.6 + 0.6*rng.Float64()
	p.cores = []int{2, 4, 8, 16, 32}[rng.IntN(5)]
	p.memTotal = []uint64{4, 8, 16, 32, 64, 128}[rng.IntN(6)] * gib
	p.uptime = time.Duration(1+rng.IntN(300)) * 24 * time.Hour
	p.latencyMs = 0.4 + rng.Float64()*20
	return p
}

// SeedServers adds a fixed set of sample servers to st when it is empty.
func SeedServers(st *store.Store) error {
	if st == nil {
		return fmt.Errorf("demo: nil store")
	}
	if len(st.Servers()) > 0 {
		return nil
	}
	for _, srv := range sampleServers {
		srv.Tags = append([]string(nil), srv.Tags...)
		if _, err := st.Add(srv); err != nil {
			return fmt.Errorf("demo: seed %s: %w", srv.Name, err)
		}
	}
	return nil
}

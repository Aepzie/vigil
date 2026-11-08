// Package collector polls every managed server over SSH (reading /proc,
// no agent on the target) and keeps the latest snapshot and history per
// server.
//
// # Collection
//
// A Hub runs one poller goroutine per server in the store. Each poller
// keeps a persistent *ssh.Client and, once per poll interval:
//
//  1. measures latency as the round trip of a "keepalive@openssh.com"
//     global request, and
//  2. opens one session that runs "env LC_ALL=C LANG=C /bin/sh -s" and
//     feeds it a small POSIX sh script on stdin. The script prints
//     sections separated by marker lines "@@vigil:<nonce>:<name>":
//     /proc/stat, /proc/meminfo, /proc/loadavg, /proc/uptime,
//     /proc/net/dev, df -kPT, uname -sr, os-release, hostname, the page
//     size and clock tick rate, /etc/passwd names and, via awk, one
//     line per process with its pid, owner and the /proc/<pid>/stat
//     fields vigil uses (newlines in process names are replaced, so a
//     process cannot hide or forge records).
//
// Sending the script on stdin makes it independent of the user's login
// shell; the random per-poll nonce keeps remote content such as process
// names from forging sections, and a trailing "end" section detects
// truncated output. Output is capped at 4 MiB and every poll is bounded
// by a timeout of twice the interval (5s to 15s); on timeout the client
// is closed, which unblocks any pending I/O.
//
// # Rates
//
// /proc exposes cumulative counters, so CPU, network and per-process
// figures are deltas against the previous poll of the same boot: the
// first poll after start (or after a reboot) reports them as zero.
// Network rates are in bits per second over all interfaces except
// loopback and container/VM plumbing (veth, docker, br-, virbr, cni,
// flannel, cali, kube-, vnet), with per-interface counter resets
// ignored. Process CPU is a percentage of one core, like top.
//
// # Failures
//
// When a server cannot be reached the poller still publishes an offline
// snapshot with a short reason every interval, so the UI and alerts stay
// current, and redials with exponential backoff (1s to 30s, jittered).
// PollNow skips the backoff. Every string read from a server is
// sanitized before it is stored in a snapshot.
package collector

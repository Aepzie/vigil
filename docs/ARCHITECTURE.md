# Architecture

vigil is a single Go binary. One process polls the fleet, evaluates
alerts and serves a separate Bubble Tea program to every SSH session.
This document maps the packages and follows the data from a monitored
server to a pixel in an administrator's terminal.

## Packages

| Package | Role |
| --- | --- |
| `cmd/vigil` | CLI: `serve`, `local`, `demo`, `render`, `user`, `pubkey`, `version`. Wires the services below together. |
| `internal/server` | The SSH application (Wish). Authentication, rate limiting, session limits, idle timeout, and one Bubble Tea program per PTY session with input and resize handling that keeps working while a remote shell runs. |
| `internal/auth` | Dashboard logins: bcrypt hashes in `users.json`, OpenSSH keys in `authorized_keys`, both re-read when they change. |
| `internal/store` | The server inventory (`servers.json`) and manager settings (`settings.json`), validated and written atomically. A `Version` counter changes on every mutation. |
| `internal/sshclient` | Outgoing SSH: the manager key (`id_ed25519`), trust-on-first-use `known_hosts`, dialing with classified errors, and the interactive remote shell used from the dashboard. |
| `internal/collector` | The `Hub`: one poller per server running the poll script over SSH, parsing `/proc` output into snapshots, computing rates and keeping a bounded history per server. |
| `internal/metrics` | The data model: `Snapshot` (one poll of one server) and `Series` (fixed-size history). |
| `internal/alerts` | The alert `Engine`: thresholds, hysteresis, CPU spike filtering, acknowledgement, and an event log that sessions poll for toasts. |
| `internal/demo` | A synthetic `collector.Source` with an evolving fleet for `vigil demo`, `vigil render` and tests. |
| `internal/tui` | The dashboard shell: header, tabs, footer, toasts, command palette, help, splash screen and compatibility mode. |
| `internal/tui/ui` | The contract between the shell and the screens: the `Screen` interface, shared `Deps`, the `Fleet` view model and messages. |
| `internal/tui/screens` | The tabs: Overview, Servers, Detail, Alerts, Settings. |
| `internal/widgets` | Pure render functions (panels, tables, charts, bars, big numbers, overlays) that return blocks of exact width and height. |
| `internal/theme` | The color palette and Lip Gloss styles; nothing else hard-codes colors. |
| `internal/compat` | Rewrites finished frames for limited fonts (blocks or ASCII) without changing cell widths. |
| `internal/sanitize` | Strips control characters and escape sequences from untrusted text. |
| `internal/format`, `internal/fuzzy` | Number/rate/duration formatting and palette fuzzy matching. |
| `internal/svgshot` | Converts an ANSI frame into an SVG terminal screenshot (`vigil render --format svg`). |

## Data flow

```
 monitored servers                     manager process
 ─────────────────          ─────────────────────────────────────────────────
                            store.Store ── servers.json, settings.json
                                 │ Version, Servers, Settings
                                 ▼
  /proc, df, ...   ◄─ssh──  collector.Hub ── one poller per server
                                 │ Snapshot ──► alerts.Engine.Evaluate
                                 │ (latest + history)        │ Active, Recent,
                                 ▼                           ▼ EventsSince
                            ┌───────────────────────────────────────────┐
  ssh admin@manager ──────► │ server: one tea.Program per SSH session   │
                            │   tick every second → build ui.Fleet      │
                            │   → ui.FleetMsg to every screen → frame   │
                            └───────────────────────────────────────────┘
```

### 1. Collection

`collector.Hub.Run` starts one poller goroutine per server in the store
and checks `Store.Version` every second, adding, removing or restarting
pollers when the inventory or the poll interval changes.

Each poller keeps a persistent `*ssh.Client` from `sshclient.Dialer`.
Once per poll interval it measures latency with a keepalive request and
opens a single session that runs `env LC_ALL=C LANG=C /bin/sh -s`, feeding
the poll script on stdin. The script prints `/proc/stat`,
`/proc/meminfo`, `/proc/loadavg`, `/proc/uptime`, `/proc/net/dev`, `df`,
`uname`, `os-release`, the hostname and every `/proc/<pid>/stat`, each
section introduced by a marker line with a random per-poll nonce.

The output is parsed into a `metrics.Snapshot`. Counters such as CPU
time, network bytes and process CPU are turned into rates against the
previous sample of the same boot. Every string from the server is
sanitized. The snapshot is published as the server's latest state and
appended to its history, sized from the `history_minutes` setting.

Unreachable servers still publish an offline snapshot every interval and
are redialed with jittered exponential backoff. `PollNow` skips the
backoff, for example after a server is edited.

### 2. Alerts

The Hub calls `OnSnapshot` after every poll. `cmd/vigil` wires it to
`alerts.Engine.Evaluate` after refreshing the thresholds from the current
settings. The engine keeps active and recently resolved alerts and
appends an event to a sequence-numbered log whenever an alert fires,
escalates or resolves. Removing a server calls `OnRemove`,
which resolves its alerts.

### 3. Sessions

`internal/server` accepts SSH connections with Wish, authenticates them
against `internal/auth` and requires a PTY. For each session it creates
a `tui.Model` with its own `ui.Deps` (shared store, Hub, engine and
dialer, plus the session's user and terminal) and runs it as a separate
Bubble Tea program. Sessions share no UI state.

Every second, each program's tick runs a command that builds a
`ui.Fleet` off the update loop: the servers and settings from the store,
the latest snapshot and history of every server from the Hub, the active
and recent alerts, and the alert events newer than the session's last
sequence number. The fleet is broadcast to all screens as `ui.FleetMsg`;
the events become toasts. Screens never touch the Hub directly; they
render from the latest fleet and talk to the shell through `ui`
messages.

### 4. Rendering

The active screen draws its content with `internal/widgets`; the shell
adds header, tabs, footer and overlays (toasts, palette, help). The
frame is fitted to exactly the terminal size and passed through
`compat.Apply`, which replaces glyphs according to the session's
compatibility mode. `vigil render` uses the same path with the demo
source to print one frame, optionally converted to SVG by
`internal/svgshot`.

### 5. Shells

Pressing `enter` on a server sends `ui.OpenShellMsg`. The shell runs an
`sshclient.Shell` through `tea.Exec`: the program releases the terminal,
the remote login shell gets a PTY of the session's size, and the
dashboard resumes when the shell exits. While the shell runs, the server
package hands keyboard input to it and forwards window size changes
directly, because the program's event loop is blocked.

## Other entry points

- `vigil local` wires the same store, Hub, engine and dialer to a single
  program in the current terminal instead of an SSH server.
- `vigil demo` replaces the Hub with `demo.Source`, which generates the
  fleet in memory and feeds the same alert engine; `--serve` serves it
  over SSH without a dialer, so no shells can be opened.

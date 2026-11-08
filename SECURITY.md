# Security policy

## Reporting a vulnerability

Please report vulnerabilities privately through GitHub's
[private vulnerability reporting](https://github.com/Aepzie/vigil/security/advisories/new)
("Report a vulnerability" on the Security tab). Do not open a public
issue.

Include the vigil version (`vigil version`), how vigil is deployed, and
steps to reproduce. You can expect an acknowledgement within a few days
and a fix or mitigation plan once the report is confirmed. Only the latest
release receives security fixes.

## Threat model

vigil runs on a central manager host. Administrators reach the dashboard
with an SSH client; the manager in turn connects to every monitored server
over SSH with its own key and reads `/proc`. Anyone who can log in to the
dashboard can open shells on every monitored server **as the configured
remote user**, so dashboard accounts are as sensitive as that access.

### Dashboard logins

- Passwords are stored in `<data>/users.json` as bcrypt hashes (cost 12).
  Unknown user names are checked against a dummy hash, so response times
  do not reveal which accounts exist.
- Failed password attempts are delayed (~300-500 ms each) and limited per
  client IP; repeated failures temporarily block password logins from
  that IP. New sessions are also rate limited per IP.
- `<data>/authorized_keys` uses the OpenSSH format. **A listed key may log
  in under any user name**; the name is only used for display. Key
  options (`from=`, `command=`, `restrict`, ...) are not supported, so
  lines with options are ignored rather than silently granting broader
  access.
- On first start, when no users or keys exist, vigil creates the user
  `admin` with a random password and prints it once to standard output.
  On systemd hosts this ends up in the journal: change it right away with
  `vigil user passwd admin`, or create users with `vigil user add` before
  the first start.
- Prefer public keys over passwords where possible.
- `vigil demo --serve --public` lets anyone log in. It refuses to run
  with access to real servers (no shells, synthetic data only); do not
  confuse it with `vigil serve`.

### Host keys (trust on first use)

- The manager's SSH host key is generated on first start at
  `<data>/host_ed25519`. Clients see it the first time they connect;
  verify its fingerprint out of band (`ssh-keygen -lf
  <data>/host_ed25519.pub`) before trusting it.
- The manager verifies monitored servers trust-on-first-use against
  `<data>/known_hosts`. The first connection to a server records its key;
  a later mismatch is refused. Pre-populate `known_hosts` if you cannot
  trust the network during the first connection.

### Files and permissions

- The data directory (`/var/lib/vigil` for the systemd unit) holds the
  host key, the manager's private key (`id_ed25519`), `users.json`,
  `authorized_keys`, `known_hosts` and the server inventory. It is created
  with `0700`; secret files are written with `0600` and replaced
  atomically. vigil tightens the host key to `0600` if it finds it more
  permissive.
- Run vigil as a dedicated unprivileged user. The provided systemd unit
  does that and enables systemd sandboxing (`ProtectSystem=strict`,
  `NoNewPrivileges`, ...).
- Anyone who can read the data directory can impersonate the manager on
  every monitored server. Back it up like any other private key.

### Terminal output

Text that comes from monitored servers (host names, process names, mount
points) and from SSH clients (user names, terminal type) is stripped of
control characters and escape sequences before it is rendered or logged,
so a compromised server cannot inject terminal escape sequences into an
administrator's terminal through the dashboard. Interactive shells opened
from the dashboard are, by design, raw terminal sessions to that server.

# Contributing to vigil

Thanks for helping! This document covers the workflow; the README covers
what vigil is and how to run it.

## Getting started

You need Go 1.26 or newer. `make` is optional but convenient.

```bash
git clone https://github.com/Aepzie/vigil
cd vigil
make test        # go test ./...
make run-demo    # the dashboard with a synthetic fleet, no servers needed
```

Useful targets: `make build`, `make lint` (go vet + gofmt check),
`make race`, `make cover`, `make screenshots`, `make cross`. Run
`make help` for the list.

Without `make`, the equivalents are `go test ./...`, `go vet ./...`,
`gofmt -l .` and `go run ./cmd/vigil demo`.

## Branches

Branch off `main` and name the branch after the kind of change:

- `feature/<short-name>` for new functionality, e.g. `feature/disk-alerts`
- `fix/<short-name>` for bug fixes, e.g. `fix/resize-during-shell`
- `docs/<short-name>` for documentation only

Keep branches focused; several small pull requests are easier to review
than one large one.

## Commits

Commit messages follow [Conventional Commits](https://www.conventionalcommits.org/):

```
<type>(<optional scope>): <summary in the imperative, lower case>

<optional body: what and why, wrapped at 72 columns>
```

Types used here: `feat`, `fix`, `docs`, `test`, `refactor`, `perf`,
`build`, `ci`, `chore`. Scopes are usually package names (`server`,
`auth`, `collector`, `tui`, ...). Examples:

```
feat(alerts): escalate disk alerts to critical
fix(server): forward window changes while a shell runs
docs: explain the authorized_keys format
```

Mark breaking changes with `!` after the type (`feat(store)!: ...`) and
explain the migration in the body.

## Pull requests

Before opening a pull request:

1. `make lint test` passes (CI also runs on Windows and macOS, and with
   the race detector on Linux).
2. New behavior has tests; table-driven tests are preferred.
3. Exported identifiers have doc comments.
4. User-visible changes are reflected in the README.

Describe what changed and why, and how you tested it. Screenshots from
`vigil render` are welcome for UI changes.

## Code guidelines

- Standard library first; new dependencies need a good reason.
- Text from remote servers or SSH clients is untrusted: pass it through
  `internal/sanitize` before rendering or logging it.
- Files written by vigil that contain secrets are created with `0600`
  permissions and replaced atomically.
- No global mutable state; pass dependencies explicitly.

## Reporting security issues

Please do not open public issues for vulnerabilities; see
[SECURITY.md](SECURITY.md).

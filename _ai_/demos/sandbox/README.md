# Wavez sandbox spike

Proves the v0.1 Seatbelt design: `wavez.sb`, a `run.sh` wrapper, `tests.sh` probes. Tested
on macOS 26.6.1 (Darwin 25.6.0), Apple Silicon, mise Go 1.26.5.

## Files

- `wavez.sb`: profile, loopback restricted to port 11434 (`wavez-loopback-open.sb` variant below).
- `run.sh [PROJECT_ROOT] -- cmd...`: creates a session tmp dir, redirects `GOCACHE`,
  `GOMODCACHE`, `GOTMPDIR` into it, resolves paths, runs `sandbox-exec`.
- `tests.sh`: 9 probes, PASS/FAIL table.
- `proj/`: minimal stdlib-only Go module (`greet` package + test) used as the build target.

## sandbox-exec status

`man sandbox-exec` marks it `DEPRECATED` (no replacement CLI ships). It still runs with no
runtime warning on stdout/stderr. Claude Code and Codex both rely on it: no supported
successor exists for ad hoc, non-app-bundle sandboxing.

## Two non-obvious findings

1. **Path params must be canonical.** `/tmp`, `/var`, `/etc` are symlinks into `/private`
   on macOS. Seatbelt's `subpath` match is a literal prefix match against the resolved
   path, so an unresolved `$TMPDIR` (`/var/folders/...`) makes every write inside it
   silently deny. `run.sh` and `tests.sh` both `realpath` `PROJECT_ROOT` and `SESSION_TMP`
   before passing them as `-D` params.
2. **`/dev/null` needs an explicit write allow.** With `file-write*` denied by default,
   `git` (invoked internally by `go build` for VCS stamping) fails with `could not open
   '/dev/null'`. The profile allows `(literal "/dev/null")` and `(literal "/dev/tty")`
   alongside the two writable roots.

## Go caches

Confirmed empirically: `go build`/`go test` need write access to three places, not one.

| Cache | Default location | Needed | Fix |
|---|---|---|---|
| Build cache | `~/Library/Caches/go-build` | yes, compiled object output | `GOCACHE` into `SESSION_TMP` |
| Module cache | `~/go/pkg/mod` | only when downloading new modules (unused here) | `GOMODCACHE` into `SESSION_TMP` |
| Build work dir | `$TMPDIR` | yes, staging dir for every build | `GOTMPDIR` into `SESSION_TMP` |

Redirecting all three keeps the writable surface to exactly the two roots in the design;
the profile never grants write under `$HOME/Library/Caches` or `$HOME/go/pkg/mod`.

## Network: what Seatbelt can and can't express

Seatbelt filters `network-outbound`/`network-inbound` by remote/local IP and port
(`(remote ip "localhost:11434")`), and separately by local filesystem path for Unix
sockets. It has no hostname-aware rule: DNS resolution happens inside the sandboxed
process, so the kernel only ever sees an IP and port at connect time. A "small allowlist
of hosts" therefore can't be expressed as a Seatbelt rule directly. Two ways to get it in
practice:

- Deny `network-outbound` except loopback (`wavez-loopback-open.sb`), then run a local
  proxy on a loopback port that itself enforces the host allowlist by SNI/Host header.
  The sandbox only ever needs to permit the one loopback port to the proxy.
- Pin IP literals for each allowed host. Fragile: most hosts serve from CDN IP ranges that
  rotate, needing re-checks whenever the allowlist changes.
  `wavez.sb` implements the simplest case the probes need: loopback:11434 only.

## Probe results

9/9 pass, run via `./tests.sh`. Ollama was running locally, so that probe ran for real.

| Probe | Expected | Got | Status |
|---|---|---|---|
| write inside project | ok | ok | PASS |
| write outside project (`$TMPDIR` sibling) | denied | denied | PASS |
| read `~/.ssh` | denied | denied | PASS |
| curl `127.0.0.1:11434` (Ollama up) | ok | ok | PASS |
| curl `https://example.com` | denied | denied | PASS |
| `go build ./...` | ok | ok | PASS |
| `go test ./...` | ok | ok | PASS |
| `python3 -c 'print(1)'` | ok | ok | PASS |
| `rm -rf` outside project | denied | denied | PASS |

## Destructive-command guard (layer 2)

Seatbelt sees paths and sockets, not intent: `rm -rf $PROJECT_ROOT/build` and
`rm -rf $PROJECT_ROOT/..` look identical to it once both resolve inside the writable
root, and `sudo` or `git reset --hard` aren't filesystem/network operations it can key on.
Wavez needs a command-pattern guard in front of the shell tool. Patterns to block or
prompt on before exec:

1. `rm -rf` (or `-fr`) where the target resolves outside `PROJECT_ROOT`
2. `git push --force` / `--force-with-lease` to a shared branch
3. `git reset --hard`
4. `chmod -R 777` (or any recursive world-writable grant)
5. `curl | sh` / `curl | bash` / `wget -O- | sh` (pipe-to-shell)
6. `dd` with `of=` pointing at a block device
7. `mkfs*`
8. `kill -9 -1` (kills every process the user owns)
9. `docker system prune` (`-a`/`-f`, and other bulk-destroy Docker/container commands)
10. `sudo` (any use, since it steps outside the sandbox's UID scope entirely)

Both layers are needed: Seatbelt stops a compromised process from touching secrets or
files outside its roots even if the guard misses a pattern, and the guard stops a
destructive-but-permitted-by-path command (`rm -rf` inside the project root, `sudo`) that
the profile would otherwise allow.

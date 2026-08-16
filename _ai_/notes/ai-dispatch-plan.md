# Remote AI Starter (`ai-dispatch`)

## Context

When you are away from the laptop you can already *continue* a Claude Code session from your phone, because `remoteControlAtStartup: true` is set in `~/.claude/settings.json` and every local `claude` process registers itself with claude.ai/code. What you cannot do is *start* one. Remote Control is per-directory and only reaches directories where a session already exists, so if you did not leave one running in the right repo before closing the lid, you are stuck. You also have no way to see what state your repos are in (uncommitted work, unpushed commits) without a terminal.

`ai-dispatch` fills exactly those two gaps: launch a Claude Code session in an allowlisted repo with a prompt and a model, and read git state. Everything after the launch is handed off to Remote Control, which already works.

The hard part is not the feature. It is that a network-reachable "start a process in a directory" service is remote code execution by construction, and you want those sessions to run without permission prompts, which removes the layer that would normally bound them. Most of this plan is about putting a different bound in place.

## What already exists (so we do not build it)

- `remoteControlAtStartup: true` in `~/.claude/settings.json`. A plain `claude` process started anywhere on this machine appears in the mobile app on its own. No integration with `claude remote-control` is needed.
- `claude [prompt]` takes a positional prompt and starts interactive. So a dispatch is one `tmux new-session` away.
- Claude Code has an OS-enforced sandbox (macOS Seatbelt) configured through `sandbox.*` in settings, independent of the permission system, and settings passed with `--settings` outrank a repo's own `.claude/settings.json` for the keys that matter.
- Syncthing is running and `~/Sync/yak-shears/` is a live folder shared with your Hetzner VPS. Notes are plain djot files at `<category>/<ISO-timestamp>.dj`, categories are directories registered in `.yak-shears/categories.json`. Writing a note from the yak-shears web app puts a file on your laptop within seconds, with no code change to yak-shears.
- `tailscale` CLI and `tailscaled` are installed and running.

## Counter-proposal on `--dangerously-skip-permissions`

You asked for dispatched sessions to run with `--dangerously-skip-permissions`, plus extra secrecy to compensate. I want to split that request, because the two halves have very different answers.

The secrecy half is right, and the plan implements it properly (see Authenticity below). A shared "hidden phrase" embedded in the message is a bearer secret: it replays if anyone ever sees one message, and it sits in plaintext in a note file on a public VPS. An HMAC over the request costs the same effort and has neither problem.

The `--dangerously-skip-permissions` half I want to talk you out of, and I think you get a *better* result. Per the docs, that flag's row in the "what replaces the prompt" table reads, literally, "Nothing." Protected-path checks are skipped too. A dispatched session could read `~/.ssh/id_ed25519` and `curl` it anywhere, and no amount of authentication on the dispatch channel prevents that, because the request that did it was legitimately yours. Authentication controls *who dispatches*. It does nothing about *what the session then does*, including when the model simply misreads your prompt.

The replacement that gets you the same "it never stops and asks me" experience:

```
--permission-mode acceptEdits  +  sandbox.enabled with auto-allow
```

Sandbox auto-allow runs Bash commands without prompting because the OS boundary contains them, and `acceptEdits` covers file writes. Prompts effectively go to zero. The difference is that writes are confined to the working directory, reads of `~/.ssh` and `~/.aws` are denied by `sandbox.credentials`, and network egress is limited to an allowlist. That boundary holds regardless of what the model decided to run.

The plan therefore ships a hardened settings file that `ai-dispatch` owns and passes with `--settings` on every dispatch, so a repo cannot weaken it. `--dangerously-skip-permissions` stays reachable but only via an explicit `allow_dangerous = true` on an individual repo entry in the config, intended for a scratch repo. If you read the above and still want it as the default, say so and I will flip the default; the mechanism is the same either way.

## Architecture

One Go daemon, one action registry, two front doors.

```
phone ──(a) HTTPS over Tailscale ──► tailscale serve ──► 127.0.0.1:7433  ┐
                                                                         ├─► action registry ─► tmux / git / tailscale
phone ──(b) yak-shears web app ──► VPS ──► Syncthing ──► dispatch/*.dj  ┘
                                  ◄── replies written back as .dj notes ◄┘
```

Channel (a) is the good one: instant, an HTML page you can browse, carries a verifiable Tailscale identity. Channel (b) is the one that answers your objection, because it needs no inbound connectivity at all. It works whenever the laptop is awake and online, which is a strictly weaker precondition than Tailscale being up. Latency is seconds, and it is fire-and-forget rather than interactive, which is fine for "start a session" and fine for "send me repo status".

Crucially, one of the actions available on channel (b) is `tailscale.up`. If you left the VPN off, you dispatch that from a note, wait a few seconds, and channel (a) comes back. That is the recovery path for the failure mode you named.

Both channels feed the same registry and the same validation. Channel (b) does not get a weaker check; it gets a stronger one, because it cannot rely on transport identity.

## Security model

Threats, in the order I actually worry about them:

| Threat | Control |
| --- | --- |
| Someone learns your yak-shears password and writes a dispatch note | HMAC signature over the envelope. The VPS never holds the key, so a logged-in attacker can write notes but cannot produce a valid one |
| A signed request is captured and replayed | Timestamp window plus a persisted nonce set |
| Another Syncthing peer, or the folder at rest, leaks a request | Nothing secret is in the file. Signatures are not reusable |
| A process on your Mac hits the local port | Bearer token in a file at mode 0600, constant-time compared |
| A tailnet peer, or a device you shared with, hits the service | Bind `127.0.0.1` only, never a tailnet address. `tailscale serve` proxies and injects `Tailscale-User-Login`, which is checked against your login. The header is unforgeable precisely because nothing but serve can reach the socket |
| Accidental public exposure | Refuse to start if `tailscale funnel status` shows the port is funneled. Never call funnel |
| A dispatched session exfiltrates credentials or writes outside the repo | The sandbox settings file: filesystem isolation on, `credentials.files` denying `~/.ssh` and `~/.aws`, `credentials.envVars` denying token vars, `network.allowedDomains` allowlist, `allowUnsandboxedCommands: false`, `allowAppleEvents` unset |
| Path traversal into a non-allowlisted directory | Repos are chosen by ID from config. The request never carries a path. Configured paths are resolved through symlinks at startup and containment is rechecked |
| Command injection through the prompt | `exec.Command` with an argv slice. No `sh -c`, ever. The prompt is one argv element |
| A loop or a bug forks bombs | Max concurrent sessions, minimum interval between dispatches, and a daily cap |
| You do not notice a dispatch you did not make | Append-only JSONL audit log, and every executed action also writes a reply note, so unexpected activity shows up in your notes app on the phone |
| The whole thing needs to stop right now | A `~/.config/ai-dispatch/DISABLED` file halts all actions, and it is itself settable through the file channel |

### Authenticity, concretely

The unit of work on both channels is the same signed envelope:

```json
{
  "v": 1,
  "id": "01J...",
  "ts": "2026-07-28T14:03:00Z",
  "nonce": "base64-16-bytes",
  "action": "dispatch",
  "params": {"repo": "ai-dispatch", "model": "opus", "prompt": "..."},
  "sig": "base64-hmac-sha256"
}
```

`sig` is HMAC-SHA256 over a canonical serialization of every field except `sig`. The key is 32 random bytes generated on the laptop, stored in the macOS Keychain, and never written to the synced folder or the VPS. Rejection is on any of: bad signature, `ts` skew over 10 minutes, a nonce already in the seen set, an unknown action, or params that fail the action's schema.

The remaining question is where the phone signs. Both realistic answers put a small static page (`web/signer.html`, WebCrypto HMAC, key in localStorage, works offline once loaded) somewhere, which then POSTs the finished envelope into yak-shears as an ordinary note. Where you host that page decides one thing: whether a VPS compromise can serve you malicious JS and steal the key. Hosting it on a static host separate from the VPS (GitHub Pages, or saved to Files and opened from there) removes that path. Hosting it inside yak-shears is more convenient and accepts it. I will build the page host-agnostic and leave the choice to you; the daemon does not care.

## Components

New repo at `/Users/kyleking/Developer/local-code/ai-dispatch`, Go 1.26, no non-stdlib dependencies except a TOML parser, a filesystem watcher, and `golang.org/x/crypto` if needed.

```
cmd/ai-dispatch/main.go            wiring, startup safety checks
internal/config/                   TOML config, repo allowlist, symlink resolution
internal/envelope/                 canonical JSON, HMAC verify, nonce store, keychain access
internal/action/                   the registry and the five v1 actions
internal/gitstat/                  read-only git queries
internal/httpsrv/                  loopback server, identity check, HTML templates
internal/filedrop/                 yak-shears watcher and reply writer
internal/audit/                    JSONL log
web/signer.html                    offline HMAC signer
dispatch-sandbox.json              settings handed to claude via --settings
launchd/me.kyleking.ai-dispatch.plist
config.example.toml
```

The registry is the extensibility surface. An action is a name, a params struct, a validator, and a handler; adding one is a single file and a registry line. No action ever accepts a shell string. When you move to a different TUI later, that is a new action, not a rewrite.

v1 actions:

- `dispatch` runs `tmux new-session -d -s dispatch-<id> -c <repo> -- claude --settings <sandbox.json> --model <model> --permission-mode acceptEdits <prompt>`. Returns the tmux session name. Remote Control picks it up on its own.
- `repos.status` returns, per allowlisted repo, the branch, ahead/behind counts, `git status --porcelain=v2` summarized to counts and paths, unpushed commit subjects from `git log @{u}..`, and the stash count. Read-only is enforced by an argv allowlist of git invocations, all with `--no-optional-locks`, so no subcommand that mutates anything can be issued.
- `sessions.list` and `sessions.kill` cover tmux sessions this tool started.
- `tailscale.up` runs `tailscale up`, so channel (b) can restore channel (a).

A background ticker writes the `repos.status` result into `~/Sync/yak-shears/dispatch/` as a note every few minutes, so requirement 2 is answered on your phone with no request at all, VPN or no VPN.

The HTML surface is one server-rendered mobile page: repo list with dirty and ahead counts, tap a repo, type a prompt, pick a model from the configured allowlist, submit. No build step, no framework. The same handlers expose JSON for scripting.

## Phases

1. Skeleton, config, envelope verification, audit log, and `repos.status` behind the loopback HTTP server. Verify the read-only path end to end before anything can start a process.
2. The sandbox settings file and `dispatch`, tested locally first, then over Tailscale.
3. `tailscale serve` wiring, identity check, funnel refusal, and the HTML page.
4. The yak-shears file channel: watcher, reply notes, status snapshot ticker, and `signer.html`.
5. launchd job, kill switch, and a README covering key rotation.

## Verification

- Config and startup: point at two repos, one a symlink, confirm both resolve and that a repo outside the allowlist is unreachable by any request shape.
- Read-only claim: run `repos.status` against a repo with staged, unstaged, untracked, stashed, and unpushed work, then confirm `git status` and the reflog are unchanged and no `.git/index.lock` was created.
- Envelope: valid signature accepted; flipped byte rejected; replayed nonce rejected; stale timestamp rejected; unknown action rejected.
- Loopback binding: `lsof -nP -iTCP -sTCP:LISTEN | grep 7433` shows `127.0.0.1` only. `curl` from the tailnet IP fails. `curl` with a forged `Tailscale-User-Login` from another machine fails because it cannot reach the socket.
- Dispatch: fire one from the phone over Tailscale, confirm the tmux session exists and the session appears in the Claude mobile app, take it over, and confirm the handoff is seamless.
- Sandbox boundary: dispatch a prompt asking it to read `~/.ssh/id_ed25519` and to write to `~/Desktop`, and confirm both are denied by the OS rather than by a prompt. This is the test that decides whether the counter-proposal above actually holds; if it does not, the dangerous-flag question is worth reopening.
- File channel, the real one: turn Tailscale off on the laptop, write a signed `tailscale.up` note from the yak-shears web app on your phone over cellular, and confirm the VPN comes back and the HTML page is reachable again. Then repeat with a `dispatch` note.
- Negative: write an unsigned note into the dispatch category by hand and confirm it is rejected and logged.
- Kill switch: touch the DISABLED file and confirm both channels refuse everything.

## Open items

- Whether dispatched sessions should run in a `git worktree` (via `claude -w`) rather than the working repo. Better isolation, but it interacts with the sandbox's worktree handling and with your `worktree.baseRef: "fresh"` setting. I will test it in phase 2 and report before committing to a default.
- The `network.allowedDomains` starting list for dispatched sessions. I will propose one after seeing what a real session needs.
- Whether `--settings` merges with or replaces your user settings for the sandbox keys. Verified empirically in phase 2 before `dispatch` is enabled.

# ai-writing-styles: architecture and roadmap

## Problem

AI-drafted text for Pylon replies and Linear tickets reads as generic "AI slop" instead of
sounding like me (or, for team-facing docs, like how our team actually writes). Pylon's
Message Copilot and Linear's AI fields only support a static tone setting, not a learned,
evolving voice with a fast human-in-the-loop feedback cycle. This repo builds that missing
piece as a Claude Code Skill plus a small companion CLI.

## Repo layout

```
ai-writing-styles/
  skill/               Claude Code Skill: the voice guidance Claude actually reads
    SKILL.md
    exemplars/
      personal/        Samples I wrote: iMessages, Mail, my own Linear comments/tickets
      company/         Samples teammates wrote in Linear: team norm baseline, not my voice
    corrections.md      Running log of AI-draft -> my-edit diffs, periodically folded into SKILL.md
  voice-cli/           Go CLI, own go.mod. Logically a separate project; lives here for
                       now, splits into its own repo + Homebrew tap once stable (see below).
  PLAN.md              This document
```

Two-tier corpus, not one. `personal/` is what sounds like me: tone, rhythm, phrasing habits.
`company/` is what sounds like the team: field structure, terminology, review-culture
norms, pulled from other people's Linear tickets. SKILL.md states precedence explicitly:
personal voice wins on tone and word choice; company norm wins on structural conventions
(required fields, ticket format) where personal exemplars are silent. This keeps "sound
like me" from silently overriding "follow the team's ticket template."

## voice-cli

Go, scaffolded from the local `my_go_template` copier template (`project_type: cli`).
Chosen over a Python CLI specifically because `my_go_template` already wires up
goreleaser and a `Formula/` directory for Homebrew tap distribution — the same binary
this repo builds now is meant to grow directly into the #3 homebrew-installed CLI later,
not be rewritten.

### Subcommands (v1)

- `voice-cli import linear` — GraphQL API (`api.linear.app/graphql`, personal API key via
  `LINEAR_API_KEY` env var, no `Bearer` prefix). Pulls issues/comments, partitions by
  `author.id == me` into `personal/` vs `company/`.
- `voice-cli import imessage` — reads `~/Library/Messages/chat.db` read-only (requires
  Full Disk Access granted to the terminal running it), filtered to `is_from_me = 1`.
- `voice-cli import mail` — reads Apple Mail's local `.emlx` files under
  `~/Library/Mail/V*/.../Sent Messages.mbox/**/Messages/*.emlx` (first line = byte count
  of the raw RFC822 message, rest is a trailing plist we discard).

### Privacy-review TUI (the "filter" gate)

Nothing from iMessage or Mail reaches `skill/exemplars/` un-reviewed. Every candidate
message flows through a small bubbletea TUI, one at a time:

- `k` keep as-is
- `e` edit/redact inline (strip names, numbers, anything sensitive) before keeping
- `s` skip
- `q` quit, saving progress so far (resumable)

Linear import gets a lighter-weight version of the same review step (auto-partitioned by
author, but still shown before writing, since tickets can contain customer PII too).

Output is a JSON corpus file per source (schema: `id, source, author (me|team), context,
timestamp, redacted, text, tags`) that a small transform step folds into
`skill/exemplars/{personal,company}/*.md` for Claude to actually read.

## Other data sources (not built yet, roadmap)

Ranked by signal-to-effort:

1. **Git commit messages + PR descriptions** — zero new auth, already structured, pulled via
   `git log` / `gh pr list`. Good "how I explain a change" signal, easy first add.
2. **Slack** — DMs and channels I post in; needs a Slack app + user token scoped to
   `channels:history`/`im:history`; same review-TUI gate as iMessage.
3. **Pylon** — ironic that it's the target surface but not yet a source: worth checking if
   Pylon exposes a REST/GraphQL export of my own past replies once this is proven out on
   Linear.
4. **Notion / Google Docs** — higher-effort auth (OAuth), lower urgency; revisit if the
   corpus feels thin after Linear + iMessage + Mail + git.

Each new source should just implement the same `Source` interface (`Fetch() []Candidate`)
and reuse the existing review TUI and corpus writer — that's the extensibility point.

## Evolution to #3 (future: standalone homebrew CLI + web review app)

`voice-cli` gains a `serve` subcommand that reads the corpus JSON and exposes it over a
small localhost API. A separate lightweight web app (Deno + Hono, matching the local
`app-template` conventions) renders the side-by-side draft/exemplar/annotate UI against
that API. Distribution: split `voice-cli` into its own repo, tag releases with the
existing goreleaser config, publish via a `kyleking/homebrew-tap` formula
(`brew install kyleking/tap/voice-cli`).

## Future: adversarial auto-mode

Today's loop is always human-reviewed (drift log + corrections.md). A later "auto" mode
that lets Claude send a Pylon reply or file a Linear ticket without a human reading it
first must not simply trust the first draft. Before anything ships unattended, run it
through an adversarial critic pass modeled on the multi-skeptic verify pattern: several
independent critics each try to find a reason the draft still reads as AI-generated or
violates a SKILL.md rule (banned phrases, structure, tone), and the draft only auto-ships
if a majority find nothing wrong; otherwise it escalates back to a human review, same as
today. This keeps "auto" from meaning "unreviewed," just "reviewed by critics instead of me."

## Future: eval harness with pydantic-evals

As `corrections.md` grows and SKILL.md gets edited in response, there's a real risk of
regressions: a fix for one banned phrase accidentally reintroduces another. Plan: a Python
eval suite using [pydantic-evals](https://ai.pydantic.dev/evals/) (`Dataset`/`Case` pairs
of context -> approved output, scored with built-in evaluators plus a case-specific
`LLMJudge` rubric such as "does this sound like Kyle, not generic AI text"). Run against
every SKILL.md change to catch drift before it reaches production drafts. This lives
alongside `voice-cli` as a small `evals/` component (Python via `uv`, matching
`calcipy_template` conventions) rather than inside the Go binary — different language is
fine here since it's a standalone CI check, not part of the CLI's runtime path.

## Status

- [ ] `skill/` scaffolding (SKILL.md, exemplars structure, corrections.md)
- [ ] `voice-cli` scaffolded via copier
- [ ] `voice-cli import linear`
- [ ] `voice-cli import imessage` + review TUI
- [ ] `voice-cli import mail`
- [ ] git commit history split rules -> company exemplars
- [ ] adversarial auto-mode (future)
- [ ] pydantic-evals harness (future)

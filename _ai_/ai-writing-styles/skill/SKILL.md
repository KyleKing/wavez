---
name: voice-check
description: Match Kyle's personal writing voice (and, where personal voice is silent, the team's Linear conventions) when drafting Pylon replies, Linear tickets, PR descriptions, or other written output. Use before sending or committing any AI-drafted text that will be read by another person, especially on Pylon and Linear.
---

# voice-check

Two-tier voice guide: `exemplars/personal/` is what sounds like Kyle. `exemplars/company/`
is what sounds like the team, pulled from Linear tickets other people wrote. Precedence:

- Tone, word choice, sentence rhythm, opinions on structure -> personal voice wins.
- Required fields, ticket template shape, terminology for this org -> company norm wins,
  but only where personal exemplars don't already say something about it.

## Before drafting

1. Skim 2-3 relevant files under `exemplars/personal/` (same rough context: a Pylon-style
   reply, a Linear ticket, a Slack-length note) and, if writing a Linear ticket, 1-2 files
   under `exemplars/company/` for structural convention.
2. Check `corrections.md` for any standing rule that applies to this kind of output. Rules
   there override anything below if they conflict.
3. Draft in that voice. Don't announce that you're doing this ("Based on your writing
   style...") — just write like it.

## Hard rules

- No bulleted list item starts with a bolded lead-in phrase followed by a colon.
- No trailing period on bulleted list items.
- No em/en dashes. Parentheses for asides, "because"/"which"/"where" for causal or
  relative clauses, a period or comma at a list's end.
- No semicolons joining independent clauses: split into two sentences, or move the second
  clause into a parenthetical if it's a short aside.
- Keep emojis and filler enthusiasm to near-zero. Don't pad with "I hope this helps!" or
  "Let me know if you have any questions!" closers unless the exemplars show Kyle actually
  writes those.
- Prefer concrete specifics (what changed, what's next) over vague reassurance.

These are starting rules, not the full picture — the exemplars carry more signal than any
rule list can. When a rule and an exemplar seem to conflict, trust the exemplar and flag
the conflict in `corrections.md` for review.

## After drafting: the review loop

1. Show the draft as-is, don't silently apply changes.
2. If the user edits it before sending, that's a signal: append the before/after to
   `corrections.md` (see its header for the format) rather than silently accepting the
   edit and moving on. Corrections accumulate; periodically they get folded back into the
   "Hard rules" section above or into new exemplars.
3. Never mark something as matching Kyle's voice based on general politeness or
   "professional tone" heuristics — only based on the actual exemplars in this repo.

## Building and refreshing the corpus

Exemplars come from `voice-cli import {linear,imessage,mail}` (see `voice-cli/README.md`),
which gates everything through a review step before it lands in `exemplars/`. Don't invent
exemplars or paraphrase what you think Kyle's voice is like — if `exemplars/personal/` is
thin for a given context (e.g. no Pylon-reply-length samples yet), say so rather than
guessing, and suggest running an import.

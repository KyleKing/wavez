# How wavez compares (2026-08-29)

[existing-alternatives-landscape.md](existing-alternatives-landscape.md) asked
whether to build or fork, in August before there was anything to compare. This
asks the question the other way: the thing exists, so where does it actually
differ, and where is it behind.

Facts come from the GitHub REST API and from the projects' own READMEs fetched
today. Claims about what a project does not have are marked as such, because
absence is the hardest thing to verify and the earlier doc's convention is
worth keeping.

## The field has not moved much in seventeen days

| project | stars | last push | language | license |
| --- | --- | --- | --- | --- |
| anomalyco/opencode | 202,413 | 2026-08-30 | TypeScript | MIT |
| openai/codex | 119,865 | 2026-08-30 | Rust | Apache-2.0 |
| cline/cline | 67,144 | 2026-08-30 | TypeScript | Apache-2.0 |
| aaif-goose/goose | 53,671 | 2026-08-29 | Rust | Apache-2.0 |
| Aider-AI/aider | 48,587 | **2026-05-22** | Python | Apache-2.0 |
| charmbracelet/crush | 27,795 | 2026-08-30 | Go | FSL-1.1-MIT |

Aider has not been pushed in over three months. The earlier doc called its
momentum the clearest signal in the survey and that call held.

## Where wavez actually differs

**Verification is the harness's job, not the model's.** Crush's README
describes no automatic linter, build, or test after an edit: the model reaches
for those through the shell, and hooks are "preliminary". Its LSP integration
feeds context rather than post-edit diagnostics. Every workflow guide written
about the 2026 agent loop describes the same shape, an agent that "runs
existing test suites, analyzes failure logs, fixes, re-runs". Claude Code's
`PostToolUse` hooks are the closest real analogue and they are genuinely
change-triggered, so this is a difference of degree rather than of kind.

The degree is the part with a number on it. wavez runs the build and the
changed packages' tests on every change and hands the run what they found at
the start of its next turn, and it attributes a failure to the files the run
touched, abstains rather than reporting when a gate examined nothing, records
a gate that failed and then passed over the same change set as a false alarm,
and escalates a tier that has failed the same way three rounds running. What
that buys, measured today over eight lanes alternating a control built from
the same tree: 8 non-targeted `go build`/`go test` sweeps through the shell on
the control side and 0 on the treatment side, at 16 tokens of preamble. The
control side spent those turns rediscovering what it was about to be told.

**Every tool result carries a cause, and the causes are counted.** A failed
call is `no_match`, `bad_input`, `ambiguous`, `malformed`, `repeat`,
`refused`, `io`, `conflict`, or `upstream`, and `wavez -stats-corpus` reports
the rate per tool per cause across every recorded run. That is what turns "the
edit tool feels unreliable" into "`str_replace` runs 94 calls at 7% since
08-28, `no_match` 5 and `ambiguous` 2". I found no equivalent in the surveyed
tools, and record that as an absence I could not verify rather than as a
claim.

**The harness measures itself against its own changes.** `wavez -replay <task>`
runs a fixed task in a fresh workspace and appends a record carrying the tier,
the model that actually served it, the checks, the tool counts, and the spend,
which is what makes a change to the harness an A/B rather than an opinion.
Aider's benchmark culture is real and it measures models. Nothing in the
survey measures its own tool surface.

The academic field arrived at the same premise from the other end.
[Harness-Bench](https://arxiv.org/abs/2605.27922) (arXiv 2605.27922) varies
harness configuration across model backends under shared tasks and budgets,
and its headline is that "agent capability should be reported at the
model-harness configuration level rather than attributed to the base model
alone". It names execution-alignment failures, where plausible reasoning comes
loose from tool feedback and workspace state, as the recurring shape. That is
the same finding as this project's gate false alarms and its `repeat` cause,
reached with a much larger sample.

## Where wavez is behind, and it is not close

Every project above supports more providers, more surfaces, and more people
than this one. Crush auto-discovers local models from Ollama, llama.cpp, LM
Studio, and OMLX; wavez has three tiers wired by hand. Cline ships a VS Code
extension, a JetBrains plugin, a CLI, an SDK, and a web board. Goose sits
under a Linux Foundation-adjacent foundation with 70-plus MCP extensions, and
wavez speaks no MCP at all. None of them would notice if this project stopped.

The honest framing is that wavez is not competing with these. It is one
person's harness for one repository on one laptop, and the things it does
differently are things that only pay off when the harness and the codebase it
edits are the same codebase. The measurement loop is the product; the agent
loop underneath is the ordinary one everybody has.

## What this suggests

Two of the four gaps the August doc named are still unclaimed and still
unbuilt here: a mobile companion with real push, and hybrid routing that
decides per request rather than per thread. The gap that has actually been
filled is one that doc did not name, which is the harness measuring its own
tool surface, and the field's own literature now says that is where the
variance lives.

The open question is whether any of this generalizes. Every number in this
repository was measured on this repository, by a harness whose tasks it wrote
itself. Harness-Bench exists, the task sets exist, and running wavez against a
corpus it did not author is the only thing that would say whether the gates
and the cause taxonomy are worth anything to anyone else.

## Sources

- [api.github.com](https://api.github.com/repos/charmbracelet/crush) for each
  repository above, checked 2026-08-29
- [Crush README](https://raw.githubusercontent.com/charmbracelet/crush/main/README.md),
  checked 2026-08-29
- [Harness-Bench: Measuring Harness Effects across Models in Realistic Agent
  Workflows](https://arxiv.org/abs/2605.27922), checked 2026-08-29
- [Confident and Wrong: Silent Semantic Failures in Coding
  Agents](https://arxiv.org/pdf/2603.25764), surfaced but not read
- [existing-alternatives-landscape.md](existing-alternatives-landscape.md),
  2026-08-13, for everything this doc does not re-derive

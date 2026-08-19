# Efficiency frontier, 2026-08-18

Does the design still lead on the four boundaries the owner set for wavez, against
what exists in August 2026? The four: (1) fewest tokens in and out per action, (2)
scheduling of threads and checks, (3) the smallest effective context per request,
(4) the thread abstraction and its UX. Written from `DESIGN.md`, `docs/glossary.md`,
`_ai_/bench/audit-2026-08-18.md`, the code in this tree, and a web research pass
dated 2026-08-18. Every number is labeled **measured** (a method and a sample size
are published), **claimed** (asserted without one), or **documented** (a shipped
default, price, or limit stated in vendor docs). Sources that read as programmatic
content are named at the end and were not used for numbers.

## Verdicts

**Tokens in and out per action: leads on structure, trails on proof.** No harness
found ships the combination wavez already has: gates that return nothing on pass, a
ledger derived from events with no model prose in it, `@symbol` expanded to a
signature rather than a body, thinking off by default (79 tokens to 2 on "OK",
measured), and a turn that talks instead of acting failed by the harness. The
Caveman ecosystem's headline numbers do not transfer: JetBrains measured the skill at
8.5% fewer output tokens on 86 agentic tasks against a 65% chat-style claim, and the
skill costs 1 to 1.5k input tokens per turn to load. What does transfer is the
warning from the same lab's rtk study: compressing tool output a harness already
truncates moved input tokens by a flat +3.2% and cost +13.8% more turns at low
effort. Wavez trims tool output by rule (20 head, 20 tail lines) and has never
measured whether that trimming costs turns. Two published numbers the design does
not record: turning thinking off costs Qwen3-8B 8 to 9 points on BFCL v3 (68.1 to
60.2), and prompt wording alone moves that model's BFCL v4 score by 16.5 points, so
the 30x token saving was bought with an accuracy cost that the 2/10 loop measurement
never separated out. The one place the design trails is
output tokens on the edit itself: `str_replace` at 190 tokens per edit lands 2 of 10
locally, and hashline, which independent 2026 measurements put at 20% fewer output
tokens on hosted models, measured worse on qwen3:8b. Modifiers and intent edits are
still M3.

**Scheduling of threads and checks: leads on the design, nothing shipped to compare.**
Every harness surveyed isolates parallel agents by git worktree (Claude Code
`--worktree` and `claude agents`, Cursor, Zed, Codex app, claude-squad, ccmanager,
Vibe Kanban) and none coordinates writes by path inside one checkout: Claude Code
blocks edits from an isolated session into the main checkout and tells agent-team
users that "two teammates editing the same file leads to overwrites", OpenHands
serializes all tools by default, and mux shows the divergence afterwards. Advisory
path leases exist only in the owner's `agent-locks`, `mcp_agent_mail`, and
`luohoa97/agent-locks`, none with a scheduler behind them. Change-triggered checks
selected by coverage exist as CI tools (testmon, Nx affected at 75 to 92% fewer
projects, measured) and in no coding agent: Aider runs the whole test command after
each edit, Claude Code offers a `FileChanged` hook, and Crush and Zed feed LSP
diagnostics only. Memory-aware admission exists nowhere: Ollama queues on
`OLLAMA_MAX_LOADED_MODELS`, LM Studio estimates before load, mlx wires 75% of RAM
and kernel-panicked a 96 GB Mac Studio in an agentic session. Wavez's design covers
all three, and M2 has shipped none of it, so the lead is on paper. One concrete gap
in what has shipped: `llama-server` runs with `-np 1`, one KV slot, so two threads
sharing the local model evict each other's prefix on every switch, and the design
never says how many slots M2 needs.

**Smallest effective context per request: matches the field on mechanism, ahead on
one decision, behind on cache economics.** Append-only trimming, tool-result
clearing before summarization, and a typed ledger instead of a narrative are now
what Claude Code (microcompact plus `cache_edits`), Codex (`/responses/compact`),
Cursor (RL-trained 1k-token summaries), and Anthropic's own context-editing API do,
and the 2025 observation-masking paper found masking halves cost against a raw agent
and matches LLM summarization on SWE-bench Verified. Wavez's "re-derive over
remember" (2.4% of a transcript measured non-reproducible) is more aggressive than
any of them and has a July 2026 paper on its side (addressable recall compaction on
Qwen3-8B). Where the design is behind: it has no repo map yet (Aider's has shipped
since 2023, and Cursor's dynamic context discovery measured a 46.9% token reduction
from loading MCP tools on demand), the stable prefix at 1.7 to 1.9k tokens sits
under Haiku 4.5's 4,096-token caching minimum, and the local prefix-hit ratio the
diagnostics panel promises has never been read because llama-server's `timings`
block is unparsed. Nobody in the field publishes a repo-map benchmark, so wavez's
M2 repo map would be measured against nothing.

**Thread abstraction and UX: matched in August 2026, and the reference has moved.**
Claude Code shipped `claude agents`: one screen of background sessions grouped by
Needs input, Working, Ready for review, and Completed, each in its own worktree,
where `Space` peeks at the exact question and answers it without attaching. That is
the Home screen `DESIGN.md` describes, including the inline answer, minus the fleet
across repos, spend per row, the memory badge, sub-thread indent, and the schedule.
Zed's threads sidebar, Amp's threads, Devin's Agents tab, Warp's agent panel,
herdr's working, blocked, and idle panes, ccmanager's waiting, busy, and idle
states, and mux's status sidebar converge on the same row. What none of them ship:
an inbox that answers prompts across many sessions from one list, a fork that
inherits the change set and not the transcript, undo through a VCS operation log
with a loss report first (Claude Code's snapshots document that bash-made and
subagent edits are not restored), a schedule view naming lock holders, and memory
headroom on the dashboard. Those are the differentiators, all designed and none
shipped past the mock, and every tool above renders a failed state where the shipped
Home does not (audit P0-1). Two ideas worth taking: gh-repo-dashboard's `!` verb
menu for the selection, and Zed's follow mode over the event stream.

## 1. Tokens in and out per action

### The Caveman ecosystem

The owner named four projects. Read against wavez, which already trims model prose to
a typed ledger and ships no model-authored task list (Decisions in `DESIGN.md`).

| Project | What it does | Measured claim | Source | What wavez does today | Gap |
|---|---|---|---|---|---|
| `caveman` (skill, 98.9k stars) | Rewrites the model's own prose in telegraphic grammar: drop articles, filler, hedging, tool-call narration, keep code and error strings verbatim, "fire direct, no preamble between calls" | **claimed** 65% output saving (1,214 to 294 tokens) over 9 chat tasks. **measured** by JetBrains at 8.5% output saving (592k to 542k) over 86 SkillsBench tasks, Sonnet 5 low effort, 3 runs, quality delta 0.015 (p 0.82). The project's own `HONEST-NUMBERS.md` says the skill costs 1 to 1.5k input tokens per turn and one Cursor case cost 4.3M tokens with it against 1M without | [repo](https://github.com/JuliusBrussee/caveman), [HONEST-NUMBERS](https://raw.githubusercontent.com/JuliusBrussee/caveman/main/docs/HONEST-NUMBERS.md), [JetBrains](https://blog.jetbrains.com/ai/2026/07/speak-to-ai-agents-like-cavemen-tosave-tokens/) | Prose is not carried: the ledger is derived from events, gates return nothing on pass, and a turn that narrates instead of acting is failed by `talkednotacted.go`. `BaseSystem` is 614 bytes and says nothing about style | None to close. Wavez already gets the "no preamble" rule as a harness check, which costs zero prompt tokens, where the skill pays 1 to 1.5k per turn to ask for it. Style rules for the small model's own prose are the one thing not measured here (see Spikes) |
| Caveman Proxy | Sits between Claude Code and the provider, re-encodes JSON tool results as TOON, structural compression per content type, byte-exact recovery on demand | **measured** 33.2% fewer input tokens (591,673 vs 885,793), CI 14.6 to 48.5%, 6 synthetic 60 to 95 KB tool-output workloads, 3 runs per arm, 18/18 exact-answer checks. HTML case regressed 9.9% | [WRAP-BENCHMARK](https://raw.githubusercontent.com/JuliusBrussee/caveman/main/docs/WRAP-BENCHMARK.md) | Tool results are trimmed at the tool (`trimOutput`, 20 head and 20 tail lines) and again by compaction (`TruncateToolOutput`, `DropOldToolResults`, `DedupeToolReads`). Gate output is parsed to failing names and frames, so the "structured test output" case never reaches the model as text | The benchmark's inputs are pathological by design (60 to 95 KB per call). Wavez's gates never emit that shape. The transferable idea is the recovery handle: a trimmed result that names how to get the rest, which `trimOutput` already does with `[N lines omitted]` but without a way to fetch them |
| `cavekit` (frozen) | Spec-driven build over one `SPEC.md` in caveman grammar with `§G §C §I §R §V §T §B` sections, `/ck:spec`, `/ck:build`, `/ck:check` | **claimed** ~75% fewer tokens than prose, and 1.1k context for nine skill descriptions against spec-kit's 18.6k. No method | [repo](https://github.com/JuliusBrussee/cavekit) | Declined by design: no model-authored task list, Cycles carry a typed ledger and the harness evaluates the exit condition. The 1.1k vs 18.6k comparison is about skill descriptions in the prefix, and wavez ships no skills at all | The `§T` task list is the model-maintained checklist `DESIGN.md` argues against. Nothing to adopt. The one honest reading is that cavekit measures its own prefix cost, which wavez has not (the 1.7 to 1.9k estimate in the audit is from source bytes, not a tokenizer) |
| `cavemem` (frozen, core now in `caveman`) | Session-boundary hooks compress observations in caveman grammar into SQLite with FTS5 and optional vectors, MCP `search`, `timeline`, `get_observations` | **claimed** ~75% fewer prose tokens, code and paths byte-exact. Hook handlers under 150 ms | [repo](https://github.com/JuliusBrussee/cavemem) | "Re-derive over remember": ledger plus change set, everything else re-read on demand, measured at 2.4% of a transcript being non-reproducible | Opposite bets. cavemem compresses prose memory and carries it, wavez carries structure and reads the tree. Neither side has measured the other's failure mode (stale carried memory vs missing carried judgment) |
| `cavegemma` | QLoRA on Gemma 4 31B, 1,750 pairs, so the compression is in the weights and no prompt is needed | **measured** 27% fewer output tokens on 193 held-out pairs, 0.91 to 0.98 MiniLM cosine, code fences byte-exact 96 to 100%. No coding or tool-call benchmark | [repo](https://github.com/JuliusBrussee/cavegemma) | Local model is qwen3:8b with thinking off. No fine-tune | The idea (bake the style in, save the prompt tokens) is the only Caveman piece that addresses a small local model's own cost, and it was done on 31B with no measure of tool-call fidelity. A 27% output cut on prose is small next to thinking-off's 30x on short turns |
| `caveman-browse` | tree-sitter queries over the DOM in place of a Playwright ARIA dump | **claimed** ("inferred" per the README) 121 tokens vs 15,704 on a 200-row table, median of five runs, and worse (67 to 111) on a tiny form | [README](https://raw.githubusercontent.com/JuliusBrussee/caveman/main/README.md) | Recordings and browser are M2/M5 | Worth reading when the browser backend lands. Not on the four boundaries |

The second JetBrains study is the one that speaks to wavez directly. rtk is a
PreToolUse hook that rewrites shell commands so their output is compressed
(`git status` from eleven lines to three). Over 425 billed trials on the same 86
tasks, the input-token class it compresses moved **+3.2% (p 0.23)**, cost went
**+7.6% (p 0.004)** with **+13.8% more turns (p 0.03)** at low effort, and the
tool's own dashboard reported 96.2 million tokens saved. The explanation was that
Claude Code already truncates the pathological outputs, so the hook saw a fifth of
tool output, and that lossy summaries cost the model a turn to recover what it
needed
([JetBrains rtk](https://blog.jetbrains.com/ai/2026/07/rtk-claude-code-token-savings/)).
Wavez's `trimOutput` and `TruncateToolOutput` are exactly this class of transform,
and the number to watch is turns per landed edit, which the benchmark harness (M3)
records and nothing today does.

### Everything else on output tokens

| Project | What it does | Measured claim | Source | What wavez does today | Gap |
|---|---|---|---|---|---|
| Thinking off on Qwen3 | Per-request `enable_thinking: false` | **measured** here: 79 tokens to 2 on "OK", 119 s to 14 s on an edit. **measured** upstream: the Qwen3 technical report puts BFCL v3 at 68.1 thinking vs 60.2 non-thinking for Qwen3-8B, 70.4 vs 61.5 for 14B ([arXiv 2505.09388](https://arxiv.org/html/2505.09388)). A user saw Qwen3-32B with thinking fabricate tool calls 3 of 5 and `/no_think` land 5 of 5 ([QwenLM/Qwen3#1817](https://github.com/QwenLM/Qwen3/issues/1817), five samples) | `DESIGN.md` Table stakes, `_ai_/bench/dogfood.md` | Shipped, per request, and the design records only the token saving | The 8 to 9 point BFCL cost of thinking off is not in `DESIGN.md`. On this laptop the trade was 30x tokens on short turns against a single-call accuracy loss, and the loop measurement (2/10 either way) never separated the two. Nemotron-Nano-9B-v2's `max_thinking_tokens` budget ([model card](https://huggingface.co/nvidia/NVIDIA-Nemotron-Nano-9B-v2)) is the middle option nobody here has tried |
| Chain of Draft, concise CoT | "Think in drafts of five words" | **measured** on GPT-4o and Claude 3.5 Sonnet: GSM8K 91.1% at 43.9 tokens vs CoT 95.4% at 205.1, and it collapses on small models: Qwen2.5-3B 59.1% to 43.1%, Llama 3.2-3B 70.7% to 52.5% ([arXiv 2502.18600](https://arxiv.org/html/2502.18600)). Concise CoT on GPT-3.5: 48.7% shorter, 27.69 points worse on math ([arXiv 2401.05618](https://arxiv.org/abs/2401.05618)). The overthinking study over 4,018 SWE-bench Verified trajectories: more overthinking, lower success, and choosing the low-overthinking sample gave ~30% better at 43% less compute ([arXiv 2502.08235](https://arxiv.org/abs/2502.08235)) | | Not applicable with thinking off | The evidence says terse reasoning costs accuracy on the small tier and that unbounded reasoning costs it on the agentic task. A budget, not a switch, is what both point at |
| Constrained output | Grammar-forced JSON or tool calls | Disputed. "Let Me Speak Freely" **measured** LLaMA-3-8B GSM8K 74.7% text vs 48.9% JSON-mode ([arXiv 2408.02442](https://arxiv.org/abs/2408.02442)). Dottxt re-ran it with grammar decoding on Llama-3-8B and got 0.77 vs 0.78, 0.73 vs 0.77, 0.41 vs 0.44 ([dottxt](https://blog.dottxt.ai/say-what-you-mean.html), library vendor). JSONSchemaBench on Llama-3.1-8B: llama.cpp grammar covers 95% of GlaiveAI schemas and 39% of GitHub-hard, GSM8K 80.1% unconstrained vs 82.4% under the llama.cpp grammar ([arXiv 2501.10868](https://arxiv.org/html/2501.10868v1)). Output-side notation compression is the one that hurts: TOON and TRON cut tokens 18 to 27% at 9 to 14 point tool-accuracy cost on 17 to 32B models, and parallel calls "collapse to near zero", while input-side compression held accuracy ([arXiv 2605.29676](https://arxiv.org/html/2605.29676v1)) | | `json_schema` for the diff review (12/12 rehearsed) and planned for intent holes. Tool calls use the native template. Tool results are plain text | The design's split (grammar for judgments with a fixed shape, native calls, no compressed notation on the output side) matches the evidence. Whether a grammar on the *hole* of an intent edit costs correctness on qwen3:8b is unmeasured |
| Hashline edits | Line-anchored `N#hash` edit ops | **measured** on 16 hosted models, 180 tasks x 3 runs: hashline matched or beat `str_replace` on 14 of 16, +15 points average, output tokens −20% median and −61% best, DeepSeek V3.2 the one regression ([stencil.so](https://stencil.so/blog/the-harness-problem)). A partial replication on three hosted models found it hurt Python and was neutral on TypeScript and Rust ([nwyin](https://nwyin.com/blogs/hashline-vs-replace-edit-bench.html)) | | `str_replace` 190 tokens vs hashline measured 1/10 on qwen3:8b, `DESIGN.md` Edits | Both public studies are hosted models. Wavez's local number stands, and the design keeps hashline as a candidate for a stronger local tier, which the M4 Pro probe would be the place to re-run |
| Aider edit formats | `whole` for small models because they cannot hold a diff format | **measured**: Qwen2.5-Coder-7B 57.9% correct at 100% correct format with `whole`, Llama-3.1-8B 37.6% ([leaderboard](https://aider.chat/docs/leaderboards/edit.html)) | | `str_replace` for local and hosted alike | Aider's answer for the 7B tier is whole-file at 3x the tokens, which wavez measured at 605 tokens and 37.7 s. The design's answer is to not emit text (Modifiers, intents), which no other harness attempts |
| Cline diff edits | Order-invariant multi-diff apply, per-model markers | **measured** `diffEditSuccess` +10% average, +25% Sonnet 3.5, sample size undisclosed ([Cline](https://cline.bot/blog/improving-diff-edits-by-10)) | | Fuzzy whitespace fallback and line numbers on non-unique match | Cline's per-model marker choice is a cheap idea for the hosted tier (V4A for GPT, str_replace for Claude is already the design) |
| Fast-apply models (Relace Apply 3, Morph) | A small hosted model merges a lazy diff at 10k tok/s | **claimed** state of the art on 500 reviewed examples ([Relace](https://relace.ai/blog/relace-apply-3)) | | Declined in `DESIGN.md` Edits: paid dependency, no open small apply model | Still true. n-gram speculation (85 vs 20 tok/s on copy-heavy output, measured here) is the local stand-in |
| Claude Code system prompt | 80% removed for Claude 5 models | **claimed** "no measurable loss on our coding evaluations", no benchmark named ([Claude blog](https://claude.com/blog/the-new-rules-of-context-engineering-for-claude-5-generation-models)) | | `BaseSystem` at 614 bytes, and a measured reason for it (prose about tools pushed a 30B model into writing calls as prose, 0 to 4 of 15 vs 15 of 15) | Wavez got there first and with a number. The gap is that the number is for one hosted model and the prefix has never been tokenized |
| Local tool-call reliability | | **measured**: Qwen3-8B F1 0.933 and Qwen3-14B 0.971 on 3,570 cases across 21 models, quantization made no difference ([Docker](https://www.docker.com/blog/local-llm-tool-calling-a-practical-evaluation/)). BFCL v4 live board: Qwen3-8B (FC) 42.57% overall, multi-turn 41.75%, and in prompt mode a 16.5-point max delta from wording alone, 14.0 for Qwen3-14B ([BFCL CSV](https://gorilla.cs.berkeley.edu/data_overall.csv)). Qwen3-8B BFCL v3 66.3% and Nemotron-Nano-9B-v2 66.9% ([model card](https://huggingface.co/nvidia/NVIDIA-Nemotron-Nano-9B-v2)). Qwen3.5-9B self-reports BFCL-V4 66.1 ([model card](https://huggingface.co/Qwen/Qwen3.5-9B)), unexplained against the board's 42.57 for Qwen3-8B. Terminal-Bench 2.0: Qwen3.5-9B 9.2% ±2.4, Qwen3.6-35B-A3B 24.6% ([leaderboard](https://www.tbench.ai/leaderboard/terminal-bench/2.0)) | | qwen3:8b: 3/3 well-formed calls in the spike, 2/10 landed edits in the loop. The diff-review prompt flipped from 1 of 5 to 5 of 5 on wording alone | Single-call benchmarks put the 8 to 9B tier at two thirds accuracy, the harness-level number is under 10%, and prompt wording moves it by 14 to 18 points. That is the ceiling any output-token trick on this tier operates under, and it says the edit path (not the prose) is where tokens are lost, and that any style rule has to be measured across wordings, not once |
| ACON | Learned compression of tool observations and history | **measured** 26 to 54% lower peak tokens with success up on AppWorld and OfficeBench, distilled into small models ([arXiv 2510.00615](https://arxiv.org/abs/2510.00615)) | | Rule-based trimming | A learned compressor is the field's answer where the rule runs out. Not for M1 to M3 |

## 2. Scheduling of threads and checks

| Project | What it does | Measured claim | Source | What wavez does today | Gap |
|---|---|---|---|---|---|
| Claude Code subagents | Up to 20 concurrent (`CLAUDE_CODE_MAX_CONCURRENT_SUBAGENTS`), 3 levels deep, worktree isolation optional per subagent | **documented** | [sub-agents](https://code.claude.com/docs/en/sub-agents) | One level of delegation, sub-threads M2 | Claude Code caps by count, wavez plans to cap by memory headroom and leases. Neither is measured against the other |
| Claude Code `claude agents` and worktrees | Background sessions each moved into `.claude/worktrees/<id>` before editing, no hard concurrency limit documented, `bgIsolation: none` to opt out | **documented** | [agent-view](https://code.claude.com/docs/en/agent-view), [worktrees](https://code.claude.com/docs/en/worktrees) | Directories, not worktrees, with subtree leases (design), measured 6.8% of writes leave the cwd | Isolation by copy vs coordination by lease. Wavez's argument (`_ai_/notes/worktrees-vs-directories.md`) is unchanged and untested at M2 scale |
| Claude Code worktree enforcement | While a session is isolated, edits into the main checkout are blocked, Bash whose cwd resolves there is blocked, `git -C` and `GIT_DIR` redirects into it are blocked, commands it cannot trace statically (brace expansion, unquoted heredocs) are refused, `git worktree lock` is held while the agent runs, `worktree.baseRef` fresh or head | **documented** | [worktrees](https://code.claude.com/docs/en/worktrees) | Run scope: edits outside what a run read or created are recorded and refused under `-strict-scope`. The guard resolves scripts and expands `$HOME` and friends before judging | The closest thing to a write fence in a shipped harness, and it fences the *other* checkout, not paths inside one. Wavez's scope check and lease design fence paths in the same tree, which is the harder problem and the one the design bets on |
| Claude Code agent teams | Separate full sessions in one directory, task claiming "uses file locking to prevent race conditions", "two teammates editing the same file leads to overwrites, break the work so each owns a different set of files", 3 to 5 recommended | **documented**, experimental | [agent-teams](https://code.claude.com/docs/en/agent-teams) | Leases on the write target so two threads never write the same subtree, contention from a dependency map | Anthropic locks the task list and tells the user to partition files by hand. That is the exact job the scheduler takes over |
| Claude Code hooks | 31 events including `FileChanged` on a watched file, `PostToolBatch`, `TeammateIdle`, command hooks default 600 s timeout and run in parallel | **documented** | [hooks](https://code.claude.com/docs/en/hooks) | Two hooks, and gates run from change events inside the harness | `FileChanged` is the closest thing to a change-triggered check in a hosted harness, and it hands the event to a user script rather than a runner with selection, debounce, and locks |
| OpenHands SDK | `tool_concurrency_limit` default 1, docs warn that concurrency races on tools writing the same files | **documented** | [parallel tool execution](https://docs.openhands.dev/sdk/guides/parallel-tool-execution) | Edit and execute phases in the scheduler | Serialize everything is the field's answer. Wavez's phases serialize writes per subtree, not globally |
| Codex subagents | `agents.max_concurrent_threads_per_session`, default chosen by Codex. Docs warn that parallel writes "can create conflicts" | **documented** | [subagents](https://learn.chatgpt.com/docs/agent-configuration/subagents.md) | | No coordination offered |
| Zed threads | Independent threads, worktree picker when two might touch the same files, running threads cannot be archived | **documented** | [parallel agents](https://zed.dev/docs/ai/parallel-agents) | | Same shape as the rest: isolate by copy, merge by hand |
| Cursor | Worktrees per agent, 25 per machine before auto-cleanup, `/best-of-n` in separate worktrees, `/apply-worktree` to bring changes back, cloud agents per VM. "Eight parallel agents" appears only on third-party pages | **documented** (25), **claimed** (8) | [worktrees](https://cursor.com/docs/configuration/worktrees) | | Prevention only |
| Devin | "Devin manages Devins" (March 2026), each in its own VM, Agents tab for child sessions | **documented** | [release notes](https://docs.devin.ai/release-notes) | | Isolation by VM |
| Jules | 5 concurrent, `--parallel` up to 5 | **documented** | [changelog](https://jules.google/docs/changelog/) | | Count cap |
| Symphony (OpenAI) | `agent.max_concurrent_agents` default 10, per-state overrides, one workspace per issue with a hashed key | **documented** | [SPEC.md](https://raw.githubusercontent.com/openai/symphony/main/SPEC.md) | | The nearest thing to a scheduler with admission, and it admits by count |
| Gas Town | Scheduler with `max_polecats`, three-tier watchdog, Bors-style bisecting merge queue, "20 to 30 agents" | **claimed** | [repo](https://github.com/gastownhall/gastown) | | The merge queue is the one idea here wavez has no equivalent for: it resolves conflicts after the fact where leases prevent them before |
| herdr, claude-squad, ccmanager, mux, Vibe Kanban, Conductor | Multiplexers over tmux or worktrees with a status per pane. Mux adds a git-divergence view across workspaces to spot conflicts after the fact | **documented**, none publish resource limits | [herdr](https://github.com/herdrdev/herdr), [claude-squad](https://github.com/smtg-ai/claude-squad), [ccmanager](https://github.com/kbwo/ccmanager), [mux](https://github.com/coder/mux), [Vibe Kanban](https://www.vibekanban.com/) | | None schedules anything. They list |
| Advisory path leases | `mcp_agent_mail` (`file_reservation_paths` with TTL and exclusive flag, conflicts returned beside grants), `luohoa97/agent-locks` (locks under `.git/agents-locks/`, static-prefix overlap, informational only, no TTL) | **documented** | [mcp_agent_mail](https://github.com/Dicklesworthstone/mcp_agent_mail), [agent-locks MCP](https://glama.ai/mcp/servers/luohoa97/agent-locks) | `_ai_/notes/agent-lock-coordination.md`, `../agent-locks`: subtree leases keyed on write target, TTL, commit downgrades to rebase-risk | Wavez's lease design is the most complete of the three and the only one that a scheduler would consume rather than a model. It has not run under M2 |
| jj workspaces for parallel agents | One workspace per agent, merge by revset | **claimed** in a practitioner post (700M tokens, 150 commits over five days) ([geirsson.com](https://geirsson.com/jj-workspaces)) | | jj workspaces already used for the mutation and fail-to-pass gates (0.31 s to add) | The Cycles section wants independent experiments in their own workspace. The primitive is in the repo |
| Aider `--auto-lint`, `--auto-test` | Lint edited files and run the whole test command after each edit, feed failures back | **documented** | [lint-test](https://aider.chat/docs/usage/lint-test.html) | Change-triggered gates with coverage selection (12 of 356 tests measured, vs 210 at importer level) | Aider runs everything, wavez selects. Nobody else runs tests without the model asking |
| Crush LSP, Zed diagnostics | LSP context and diagnostics available to the agent | **documented** | [Crush README](https://github.com/charmbracelet/crush/blob/main/README.md), [Zed agent panel](https://zed.dev/docs/ai/agent-panel) | LSP diagnostics gate after format and rules, filtered to changed files, 1.5 to 5 ms warm | Matches on the LSP piece. Nobody else runs tests or mutation from the change |
| testmon, jest `--onlyChanged`, vitest `--changed`, gotestsum `--watch` | Coverage or module-graph test selection in the test runner | **documented**, testmon publishes no ratio. Nx affected measured 62 to 13 and 62 to 6 projects at LeanIX ([LeanIX](https://engineering.leanix.net/blog/smarter-nx-affected-checks/)) | [testmon](https://testmon.org/), [jest](https://jestjs.io/docs/cli), [vitest](https://vitest.dev/guide/cli), [gotestsum](https://github.com/gotestyourself/gotestsum) | Own line-to-test map for Go, coverage.py contexts for Python, importer fallback | Wavez's selection is in the harness rather than one runner, which is what lets it feed a scheduler. Its measured ratios (356 to 12, 522 to 3 to 5) are in line with Nx's |
| Ollama admission | `OLLAMA_MAX_LOADED_MODELS` (3 per GPU), `OLLAMA_NUM_PARALLEL` 1, queue 512, 503 when full, unload idle models to fit a new one | **documented** | [FAQ](https://docs.ollama.com/faq) | Supervisor starts one `llama-server` with `-np 1`. Memory-aware admission M2 | Ollama admits by model count, not headroom, and does not know about a test suite. Nothing else does either |
| LM Studio `lms load --estimate-only` and guardrails | Estimate before load, refuse on guardrail | **documented** | [lms load](https://lmstudio.ai/docs/cli/local-models/load) | Model management screen (M2) shows what a load leaves free | Same idea, one step earlier |
| mlx `set_wired_limit` | Wires ~75% of RAM, no KV cap. Kernel panic at 80 GB wired on a 96 GB M3 Ultra during an agentic session | **measured** in the issue ([mlx-lm#883](https://github.com/ml-explore/mlx-lm/issues/883)) | | llama.cpp on Metal, `recommendedMaxWorkingSetSize`, `iogpu.wired_limit_mb` swaps rather than refuses ([discussion](https://github.com/ggml-org/llama.cpp/discussions/2182)) | The failure mode wavez's admission is meant to prevent is real and documented on the other runtime |
| llama-server slots | `-np N` parallel slots each with its own KV, `--cache-reuse`, slot save and restore to disk | **documented** ([server README](https://github.com/ggml-org/llama.cpp/blob/master/tools/server/README.md)). LM Studio's KV checkpoints on disk: four parallel chats 37.6 to 16.8 s on Qwen3.6-27B, M3 Max ([LM Studio](https://lmstudio.ai/blog/mlx-engine-agentic-workloads), vendor measurement) | | `-np 1` in `internal/runtime/process.go` | With one slot, every thread switch on the local model re-prefills the other thread's prefix. M2's fleet needs either N slots (N x KV memory against the 16 GB ceiling) or slot save and restore keyed by thread, and the design says neither |

## 3. Smallest effective context per request

| Project | What it does | Measured claim | Source | What wavez does today | Gap |
|---|---|---|---|---|---|
| Aider repo map | Tree-sitter tags, PageRank over the dependency graph, `--map-tokens` 1k default, expands 2x with no files in chat | **documented**, no benchmark of map quality ever published | [repomap](https://aider.chat/docs/repomap.html) | Planned M2, "under 1k tokens by default", plus a `context` bundle with one-hop neighbours and covering tests | Wavez's bundle is a superset (coverage and callers) and unmeasured, as Aider's is |
| Cursor dynamic context discovery | Tool outputs to files, chat history as a searchable file, MCP tools loaded on demand | **measured** 46.9% token reduction on runs that call MCP tools, A/B, "high variance" ([Cursor](https://cursor.com/blog/dynamic-context-discovery)) | | MCP on demand from an allowlist (M3), tool schemas always in the prefix (seven tools, ~5.5 to 6 KB estimated) | The seven-tool prefix is fixed and small. Deferring nothing is right until the tool count grows |
| Claude Code context | No repo map, CLAUDE.md, auto memory (first 200 lines or 25 KB), MCP schemas deferred by default, `BASH_MAX_OUTPUT_LENGTH` 30,000 chars middle-truncated, tool results cleared before summarization, `cache_edits` so clearing keeps the prefix, ~40k recent tokens kept | **documented**. Thresholds served remotely, so only known from source reading | [context window](https://code.claude.com/docs/en/context-window), [prompt caching](https://code.claude.com/docs/en/prompt-caching), [issue 42542](https://github.com/anthropics/claude-code/issues/42542) | Append-only trimming at 75% of the local budget, `DedupeToolReads` by content hash, `DropOldToolResults`, `TruncateToolOutput` | Same mechanism family. Claude Code's `cache_edits` is server-side and does what wavez does client-side |
| Codex CLI | Head-and-tail truncation (was 256 lines or 10 KiB, moving to token-based), stateless requests, stable tool order for prefix caching, `/responses/compact` returning an encrypted item | **documented** ([Codex loop](https://openai.com/index/unrolling-the-codex-agent-loop/), [issue 6426](https://github.com/openai/codex/issues/6426)) | | Same head-and-tail shape (20 and 20 lines) | Codex's own issue names the problem with line-based limits: 256 short lines is 1 to 2k tokens, 100 long lines is 10k. Wavez's limit is line-based |
| OpenCode | `compaction.prune` off by default, keeps 40k tokens of recent tool output, edit requires a prior read | **documented** ([config](https://opencode.ai/docs/config/), [source](https://raw.githubusercontent.com/sst/opencode/dev/packages/opencode/src/session/compaction.ts)) | | | Match |
| Cline | Duplicate reads replaced by a notice before truncation, MCP docs out of the system prompt (was 30% of it) | **claimed** no measurement ([Cline](https://cline.bot/blog/inside-clines-framework-for-optimizing-context-maintaining-narrative-integrity-and-enabling-smarter-ai)) | | `DedupeToolReads` | Match, and neither side has measured what dedupe saves |
| Cursor self-summarization | RL-trained summaries ~1k tokens vs 5k prompted, halves compaction error on CursorBench | **measured**, internal ([Cursor](https://cursor.com/blog/self-summarization)) | | Residue summarized by the local model, user can edit | Wavez's residue is small by construction. Cursor's number says a trained summarizer beats a prompted one, and wavez has no summarizer benchmark |
| Anthropic context editing and compaction | `clear_tool_uses` at 100k, keep 3, `compact_20260112` at 150k, memory tool | **measured** 84% token reduction on a 100-turn web-search eval, +39% with memory ([Anthropic](https://claude.com/blog/context-management)). Docs [context editing](https://platform.claude.com/docs/en/build-with-claude/context-editing) | | Client-side equivalents | The hosted tier through OpenRouter cannot use these. Fine at wavez's spend (under $1 a day in the audit) |
| Manus | Append-only context, mask tools rather than remove them, file system as memory, todo recitation | **claimed** 100:1 input to output, ~50 calls per task ([Manus](https://manus.im/blog/Context-Engineering-for-AI-Agents-Lessons-from-Building-Manus)) | | Append-only, tools narrowed by registry not by list | Same principles, and wavez's registry narrowing is stricter (a named tool not offered is refused) |
| Observation masking | Replace old tool observations with a placeholder | **measured**: halves cost vs raw agent, matches or beats LLM summarization, SWE-bench Verified, 5 configs ([arXiv 2508.21433](https://arxiv.org/abs/2508.21433)). A 2026 follow-up finds it hurts saturated models ([arXiv 2606.00408](https://arxiv.org/abs/2606.00408)) | | `DropOldToolResults` | The first paper is the strongest external support for deterministic-first compaction. The second says the local tier is where it helps most, which is wavez's tier |
| Addressable recall compaction | Append-only, ID-addressable observation log, older observations replaced by citations, on Qwen3-8B and 32B | **measured** NIAH 99.40% vs 88.12% ([arXiv 2607.25066](https://arxiv.org/abs/2607.25066)) | | `[N lines omitted]` markers with no handle, `DedupeToolReads` leaves a hash reference | The paper is wavez's compaction on wavez's model, plus the one thing missing: a handle the model can cite to get the content back |
| Anthropic caching | 5m write 1.25x, 1h write 2x, read 0.1x, minimum 512 (Fable 5, Opus 5), 1,024 (Sonnet 5), 4,096 (Haiku 4.5) | **documented** ([docs](https://platform.claude.com/docs/en/build-with-claude/prompt-caching)) | | Prefix 1.7 to 1.9k estimated, so under Haiku's minimum, and caching worth ~18% on wavez's shape per the audit | The design's caching line ("native wire format and a pinned provider") is half right: OpenRouter forwards `cache_control` ([OpenRouter](https://openrouter.ai/docs/features/prompt-caching)), a pinned provider is still needed |
| OpenAI caching | 1,024 min, reads 0.1x on GPT-5.6+, writes 1.25x, 30-minute TTL option, `prompt_cache_key` | **documented** ([OpenAI](https://developers.openai.com/api/docs/guides/prompt-caching)) | | Default hosted is gpt-5-mini | Automatic. Nothing to build |
| llama.cpp prefix reuse | `--cache-reuse N`, slot save and restore, n-gram speculation | **measured** here: append to a 3k prefix 0.2 s, mid-edit 5 to 7x. Upstream self-speculation 181 to 445 tok/s on gpt-oss-120b in favorable cases ([PR 18471](https://github.com/ggml-org/llama.cpp/pull/18471)). An independent run on Qwen3.6-35B-A3B found n-gram −3.4% to −12.3% ([thc1006](https://github.com/thc1006/qwen3.6-speculative-decoding-rtx3090)) | | Shipped with `--cache-reuse` and `ngram-simple`, prefix hit ratio unread | Two gaps: `timings` unparsed (audit recommendation 4), and the MoE result says n-gram speculation may not carry to a 30B-A3B tier on the M4 Pro |
| MLX prompt cache, LM Studio KV checkpoints | Prompt cache to disk, 256-token checkpoints with LRU | **measured** by their authors: replay prefill 1,955 to 209 ms (M2 Ultra) ([mlx-lm PR 1405](https://github.com/ml-explore/mlx-lm/pull/1405)). Parallel chats 37.6 to 16.8 s ([LM Studio](https://lmstudio.ai/blog/mlx-engine-agentic-workloads)) | | Nothing on disk, one slot | The persistence idea is the answer to the `-np 1` gap above |
| Anthropic multi-agent research | Subagents return 1 to 2k token summaries, agents ~4x chat tokens, multi-agent ~15x | **measured** internal eval ([Anthropic](https://www.anthropic.com/engineering/multi-agent-research-system)) | | One level of delegation, sub-threads carry the ledger | The 15x is the number the "no hierarchies past one level" decision defends against |
| Ledger and typed state | LedgerAgent renders a structured state ledger into the prompt | **measured** on customer-service pass^k, numbers not in the abstract ([arXiv 2606.20529](https://arxiv.org/abs/2606.20529)) | | `LedgerSummary` is files changed, turns, gates run, and the Cycles hypothesis ledger is designed at 360 tokens | Wavez's ledger has three fields today. The Cycles ledger is the interesting one and is M2 |

## 4. Thread abstraction and UX

| Project | What it does | Measured claim | Source | What wavez does today | Gap |
|---|---|---|---|---|---|
| Claude Code `claude agents` | One screen for background sessions grouped Pinned, Ready for review (open PR), Needs input, Working, Completed. States Working, Needs input, Idle, Completed, Failed, Stopped. Row shows name, current activity, age. `Space` peeks and shows the exact question, and a reply or a number for a predefined choice answers it without attaching. `Ctrl+S` groups by state or directory. `claude --bg "…"` dispatches from the shell. Subagents are not rows | **documented** | [agent-view](https://code.claude.com/docs/en/agent-view) | Home: glyph, name, step in words, age, spend, `v` peek with the live prompt row answered by `y`, `n`, `a`, or typed text. Designed, and the shipped Home has no error state (audit P0-1) | The two Home screens are the same design. Wavez adds spend per row, a memory badge, sub-thread indent, and a fleet across repos, and lacks Failed as a rendered state. "Answer from the list without opening" is no longer a differentiator |
| Claude Code sessions | `--continue`, `--resume` picker with name, age, branch, size, `Ctrl+A` across projects, `Ctrl+W` across worktrees, `bg` marker. `/branch` copies the transcript to a new session, `--fork-session` to a new process. `/rename` and Haiku-generated titles. `SendMessage` and `@name` between sessions (2.1.224+) | **documented** | [sessions](https://code.claude.com/docs/en/sessions), [CHANGELOG](https://raw.githubusercontent.com/anthropics/claude-code/main/CHANGELOG.md) | Resume exists, no "continue latest" (audit gap 14). Fork inherits the change set and none of the transcript | Claude Code's fork copies the transcript. Wavez's fork is the opposite bet, argued from the 97.6% re-derivable measurement, and no tool ships it |
| Claude Code todo, tasks, recap | `Ctrl+T` todo of up to five items (off by default on Opus 4.8, Sonnet 5, Fable 5), `/tasks` for shells and subagents, a one-line recap after 3 minutes away | **documented** | [interactive mode](https://code.claude.com/docs/en/interactive-mode) | No model-authored list by decision. The ledger row is the recap | Anthropic turned the model-authored todo off by default on the newest models, which is the direction wavez's decision took |
| Claude Code checkpoints | `/rewind` or double `Esc`: restore code, conversation, or both, or summarize from here. 100 snapshots kept, survive resume. Bash-made changes, most subagent edits, and external edits are not restored | **documented** | [checkpointing](https://code.claude.com/docs/en/checkpointing) | `u` reports what the working copy would lose against the jj checkpoint and restores on confirmation, `wavez -undo <op>` from the shell | jj's op log covers what Claude Code's snapshots document as gaps (bash-made and external edits). No mainstream agent ships jj-based undo. The loss report before restore is unshipped anywhere else |
| Claude Code permission prompts | Yes, yes and do not ask again for this pattern, no with instructions. Session-scoped answers including denies for background subagents (2.1.234) | **documented** | [interactive mode](https://code.claude.com/docs/en/interactive-mode) | `y`, `n`, `a` for the thread. Allow-always does not survive a daemon restart (audit P2-2) | Match on shape. Persistence is the open choice below |
| Claude Code web, desktop, mobile | Sessions sidebar with archive, share, `+42 −18` diff indicator opening a diff with inline comments sent to Claude, `--teleport` one way from cloud to CLI, `/tasks` then `t` | **documented** | [on the web](https://code.claude.com/docs/en/claude-code-on-the-web) | Mobile is M4: PWA over Tailscale, inbox as the landing view, diff with Ask-a-line | The bar `DESIGN.md` names. Handoff between surfaces is by branch fetch there, and by the same daemon here, which is the M4 argument |
| Codex CLI | `codex resume` by recent or ID, `/permissions`, `/status` with context usage | **documented**, thin (docs moved to learn.chatgpt.com and deep pages 404) | [Codex CLI](https://learn.chatgpt.com/docs/codex/cli) | | Nothing here wavez lacks |
| OpenCode | `/sessions`, `/undo` reverts the last user message and its file changes (git snapshot), `/redo`, `/share` to a public URL, child sessions navigable with leader plus arrows | **documented** | [TUI](https://opencode.ai/docs/tui/), [share](https://opencode.ai/docs/share/), [agents](https://opencode.ai/docs/agents/) | Sub-threads indent under the parent on Home | Match. OpenCode's child navigation from inside a thread is the one thing the thread view lacks (`[` and `]` move between siblings, not into children) |
| Crush | Sessions per project, `IsBusy` and `AttachedClients` per session, per-tool allowlist, `--yolo` | **documented** | [README](https://raw.githubusercontent.com/charmbracelet/crush/main/README.md) | Daemon and TUI split, one socket API | Crush's multi-client attach is the same shape as the daemon design |
| Amp | Threads by URL, visibility levels, `/handoff` extracts context into a new thread (Oct 2025), then auto compaction returned and handoff went (May 2026), agents spawn and message agents across threads (Jul 2026), multiplayer on one thread | **documented** | [manual](https://ampcode.com/manual), [handoff](https://ampcode.com/news/handoff), [neo](https://ampcode.com/news/neo), [agent to agent](https://ampcode.com/news/from-agent-to-agent) | Threads are the unit, one level of delegation | Amp reversed on handoff versus compaction with no published measurement. Wavez's fork-with-change-set is a third answer and also unmeasured |
| Zed agent panel | Threads sidebar across projects, `Ctrl+Tab` cycles, "New From Summary", follow mode jumps to each touched file, `Ctrl+Shift+R` multi-buffer to accept or reject each hunk, "Restore Checkpoint" after each edit, per-tool confirm, always allow, or deny, ACP thread import | **documented** | [agent panel](https://zed.dev/docs/ai/agent-panel), [external agents](https://zed.dev/docs/ai/external-agents) | Diff pane with real hunks on demand, Ask-a-line, hunk accept or reject from Neovim (M3) | Zed's per-hunk accept or reject in the editor is the review flow `wavez.nvim` plans. Follow mode has no equivalent and is cheap given the event stream |
| Warp agent management panel | Rows are interactive conversations or cloud runs, states Working, Blocked, Canceled, Failed, Success, source column (CLI, API, Slack, Linear, Scheduled), filters | **documented** | [Warp](https://docs.warp.dev/platform/managing-cloud-agents/) | Home sorts needs-input first, `/` filters | The source column (what started this) is a small idea worth taking once routines and schedules can start threads |
| Cursor agents window | Local, cloud, and SSH agents in one window, worktree per task, diffs view, `/in-cloud`, move an agent from cloud to local | **documented** | [agents window](https://cursor.com/docs/agent/agents-window) | | |
| Conductor, Vibe Kanban, claude-squad, ccmanager, mux | Worktree per task, a status per row (ccmanager: Waiting, Busy, Idle with per-project counts and status-change hooks), diff review, some with mobile web | **documented** | [Conductor](https://www.conductor.build/docs/), [Vibe Kanban](https://raw.githubusercontent.com/BloopAI/vibe-kanban/main/README.md), [claude-squad](https://raw.githubusercontent.com/smtg-ai/claude-squad/main/README.md), [ccmanager](https://raw.githubusercontent.com/kbwo/ccmanager/main/README.md), [mux](https://raw.githubusercontent.com/coder/mux/main/README.md) | | ccmanager's status-change hooks are the notification path wavez defers to M2 |
| gh-repo-dashboard | Every git and jj repo under a directory with PR and CI state, `v` expands in place, `!` lists verbs for the selection, `?` keymap, opens the repo directly when run inside one, confirmation before anything that reaches the remote or destroys work | **documented** | [README](https://raw.githubusercontent.com/KyleKing/gh-repo-dashboard/main/README.md) | Home copies expand, scope resolution, and footer priority (the last is in the source, not the README) | Same author, same shape. The `!` verb menu for the selection is not in `DESIGN.md`'s Home and is a cheaper discoverability layer than the palette for L2 verbs |

Patterns in three or more tools: worktree per task as the isolation unit, a row
vocabulary with a distinct needs-input state, fork or branch into a new session ID,
non-git local snapshots for undo (Claude Code, Cursor, Zed) with OpenCode on git,
shareable session URLs, diff review with comments sent back to the agent, and a
session name plus a generated title.

Nothing found ships: an inbox that answers prompts across many sessions from one
list (Claude Code's peek answers one at a time), a fork that carries the change set
and drops the transcript, undo through a VCS operation log with a loss report first,
a schedule view naming lock holders, memory headroom on the dashboard, or spend per
thread on the list. Those are the differentiators, all designed and none shipped past
the mock, and every tool above renders a failed state, which the shipped Home does
not (audit P0-1).

## Spikes

Each is a claim in `DESIGN.md` or in the verdicts above that has no precedent or
measurement on the web, proposed in the shape of the existing `_ai_/demos/`: a
README with the question, the method, and the numbers, plus the scripts that
produced them. Effort is one person on this laptop.

| Spike | Question | Method | The number that decides it | Effort |
|---|---|---|---|---|
| `demos/trim-turns/` | Does head-and-tail trimming of tool output (20 and 20 lines, and compaction's `TruncateToolOutput`) cost turns the way rtk did? Nobody has measured a trim rule on turns rather than tokens for a local model | Replay the dogfood tasks and the fail-to-pass corpus through `wavez -p` with JSON output in three arms: trimming as shipped, trimming off, and a token-based limit in place of a line-based one (Codex's own issue [6426](https://github.com/openai/codex/issues/6426) names the mismatch). Same model, same seeds, three runs each | Turns per landed edit and total input tokens per arm. rtk's result was a flat null on tokens and +13.8% turns, and that is the failure to rule out | 1 day. `-p` JSON with turn count exists |
| `demos/recall-handle/` | Does a fetchable handle on omitted output reduce turns against `[N lines omitted]`? [Addressable recall compaction](https://arxiv.org/abs/2607.25066) measured it on Qwen3-8B for long-context QA, not for tool output in an agent loop | Write each trimmed tool result to the session directory under an id, add the id to the marker, and let the model fetch a range through `read` (no new tool). Run the tasks whose gate or shell output exceeds 40 lines in both arms | Turns and landed edits on the long-output tasks, and how often the handle is followed. If the model never fetches, the handle is prefix cost for nothing, which is the scratchpad result again | 1 to 2 days |
| `demos/kv-slots/` | With `-np 1`, what does a second thread cost the first? The design's M2 fleet has no number for KV slots, and the only published parallel-chat measurement is LM Studio's on a 36 GB machine ([mlx-engine](https://lmstudio.ai/blog/mlx-engine-agentic-workloads)) | Parse `timings` (`cache_n`, `prompt_ms`) first, which is audit recommendation 4. Then two scripted threads with distinct 3k prefixes alternate turns under `-np 1`, `-np 2`, and `-np 1` with slot save and restore keyed by thread, at 8k and 16k served context, with qwen3:8b resident and `go test` running | `prompt_ms` per turn per arm, and resident memory per slot against the admission threshold. The design needs to say whether M2 buys slots with memory or with disk | Half a day after `timings` lands |
| `demos/prefix-tokens/` | How large is the stable prefix, in the served model's tokens? The 1.7 to 1.9k figure is from source bytes at four characters a token, and the caching decision (Haiku's 4,096 minimum, Sonnet's 1,024) and the 8k budget share both hang on it | POST the assembled system prefix plus tool schemas plus a typical `context` list to llama-server's `/tokenize`, and count with the hosted tokenizer through a dry-run request's `usage.prompt_tokens` for gpt-5-mini and Sonnet 5 | Prefix tokens per model, and the share of the 8k window it takes. Under 1,024 on Sonnet means no hosted caching at all | Hours. Local half measured 2026-08-18: about 2,430 qwen3:8b tokens on this repo, chars/4 within 1% (`_ai_/demos/prefix-tokens/`) |
| `demos/terse-prefix/` | Does a one-line terse-output instruction (not the 1 to 1.5k caveman skill) change qwen3:8b's native tool-call rate, output tokens, or landed edits? JetBrains measured the skill on Sonnet 5, nobody has measured any style rule on an 8B model in a loop, and this repo already measured that prose about tools in the prefix pushed a 30B model into writing calls as prose | The five-task, 20-run edit harness that produced the 2/10 number, in three arms: `BaseSystem` as shipped, plus one line ("Answer in fragments. No preamble."), plus the full caveman `SKILL.md`. Count native calls, calls written as prose (`looksLikeToolCallText`), output tokens per turn, and landed edits | Native tool-call rate first, output tokens second. A drop in the first at any saving in the second ends it, since the design's own measurement says the prefix steers call shape | Half a day |
| `demos/thinking-budget/` | Did thinking off cost landed edits, or only tokens? The decision was measured on tokens (79 to 2) and wall time (119 s to 14 s), and Qwen's own report says it costs 8 to 9 BFCL points at 8B. Nobody has measured a thinking *budget* on an 8B model in an edit loop | The same five-task, 20-run harness in three arms: thinking off (shipped), thinking on, and thinking capped (llama-server's reasoning budget, or Nemotron-Nano-9B-v2's `max_thinking_tokens` as a second model), recording landed edits, native call rate, output tokens, and wall time | Landed edits per arm at what wall time. If a 200-token budget lands more edits than off at under 2x the wall time, the switch becomes a budget | Half a day, shares the harness with `terse-prefix` |
| `demos/fork-shape/` | Does a fork that inherits the change set and none of the transcript produce work as good as one that copies the transcript (Claude Code `/branch`) or hands off a summary (Amp, since reversed)? The 97.6% re-derivable measurement bounds this and does not answer it, and it is the same open question the Cycles ledger carries | Five mid-task forks from real threads with a "try the other approach" prompt, three arms (change set only, transcript copy, one-paragraph handoff the local model writes), qwen3:8b and gpt-5-mini | First-turn prompt tokens and landed edits per arm. If change-set-only lands as often, the decision stands and the Cycles ledger question closes with it | 1 day |
| `demos/bundle-value/` | Does the `context` bundle (repo map, one-hop neighbours, covering tests) reduce turns for a small model against no bundle and against an Aider-style map alone? No vendor has published a repo-map benchmark, so the M2 repo map would ship against nothing | Ten tasks from the intent-edit corpus, three arms, qwen3:8b, `-p` JSON | Turns to the first correct edit and prefix tokens per arm. The bundle has to buy back its own tokens in fewer search turns or it stays off by default | 1 day. `context` exists |
| `demos/admission-line/` | Where is the memory line at which qwen3:8b plus a Go test run starts to swap, and does 16k or 32k served context cross it? The design cites 31% free with the 8B loaded and no number for the KV at a larger window, and the field's only data is a kernel panic on mlx | `llama-server` at 8k, 16k, and 32k with `-np 1` and `-np 2`, `go test ./... -p 8` on this repo, `vm_stat` and `memory_pressure` sampled once a second, decode tok/s during the run | Peak wired plus RSS against 16 GB, and tok/s under the test run. The admission threshold becomes a measured constant instead of a guess | Hours |

`demos/lease-vs-worktree/` (two threads on overlapping subtrees under leases against
two worktrees and a merge, counting conflicts and rebase risk) is the spike that
proves the scheduling bet, and it needs M2's leases running first, so it is named
here and not scoped.

## Questions for the owner

Each is a choice the research cannot make. Options and the tradeoff, no
recommendation.

**Slots for the local model in M2.** (a) `-np N` with one slot per active thread,
paying N times the KV memory against the 16 GB ceiling and lowering the served
window to fit. (b) One slot plus llama-server's slot save and restore to disk keyed
by thread, paying a restore on every thread switch and owning the slot files. (c)
One slot and the scheduler serializes local-model turns across threads, so a second
thread queues rather than evicts. (a) is fastest and costs memory, (b) costs disk
and code, (c) costs nothing and makes the fleet slower than one thread on the local
tier, which is the tier the thesis is about.

**Prefix size against hosted caching minimums.** The prefix is small by design
(estimated under 2k). (a) Keep it, accept no caching on Haiku 4.5 (4,096 minimum) and
roughly 18% on Sonnet 5, and route on reliability alone as the audit argues, since
the hosted tier costs under a dollar a day. (b) Let a project's `context` list pad
the prefix past the minimum when the hosted model is Claude, trading tokens on every
request for a cache read price. The `prefix-tokens` spike gives the number, and the
choice is whether hosted cost is worth any prefix growth at all.

**A recovery handle for trimmed output.** (a) Keep `[N lines omitted]` with no way
back, so a model that needs the middle re-runs the command. (b) Write trimmed output
to the session directory and let `read` fetch it by id, which is a marker change and
no new tool. (c) An eighth tool, `read_output`, which is clearer to a model and grows
the prefix on every request of every thread. The scratchpad decision already found
that a line in the prefix the model ignores costs tokens for nothing, so (c) carries
that risk and (b) does not.

**Persisting allow-always across daemon restarts** (audit P2-2). (a) Keep it
per process, so every restart re-asks about `go test`. (b) Persist per project into
`.wavez/`, so an unattended run after a restart can do whatever was allowed once,
which widens what a compromised run does without asking. Claude Code persists
allow rules at the settings level. The tradeoff is friction against blast radius,
and it is a security choice.

**Fork semantics before or after the spike.** `DESIGN.md` ships the fork with the
change set only. (a) Build it as designed in M2 and run `fork-shape` afterwards
against real forks. (b) Run the spike first on hand-made forks and pick the default
from the number. (a) gets the fleet sooner and may ship the wrong default, (b)
delays a screen for a measurement whose corpus is thin (five forks).

**Home's scope now that `claude agents` exists.** (a) Keep Home as designed: wavez
threads only, fleet across repos, spend, memory, schedule. (b) List sessions of
other agents on this machine too (herdr and ccmanager detect Claude Code and Codex
state from their panes), which makes Home the one dashboard and makes it a
multiplexer, which the design says it is not. (a) leaves the owner running two
dashboards on days that mix tools, (b) reopens a decision.

## Sources set aside as programmatic content

techtimes.com, thepromptshelf.dev, coddykit.com, qwe.edu.pl, mingooland.com,
lobehub.com skill listings, claudelog.com, claude-wiki.com, claudefa.st,
gradually.ai, theagenttimes.com, runaihome.com, byteiota.com, alphasignal.ai,
appscale.blog, mightybot.ai, smartscope.blog, promptquorum.com, agentpatterns.ai,
gitworktree.org, agentmarketcap.ai, techsy.io, medium and DeepWiki summaries of the
above, and codex.danielvaughan.com (secondary, its Codex defaults are unverified
against source). Each restates a repo README or a vendor post with numbers rounded or
inflated. Where one of them was the only carrier of a number, the number is not in
this file.

# Coding-agent benchmark suites for offline, repeatable evaluation

Research date: 2026-08-12/13. Scope: benchmark suites usable to compare Claude Code against other coding agents (OpenCode, Aider, Cursor CLI, Codex CLI, etc.) offline and repeatably, plus how to instrument a harness for apples-to-apples metrics.

**Method note.** This doc was built from a mix of dispatched research agents and direct `WebFetch` against primary sources (GitHub repos, official leaderboards, arXiv papers). The session's `WebSearch` budget (200 calls, shared across the session) was exhausted partway through, so several sections rely on direct URL fetches rather than open-ended search, and a few JavaScript-rendered leaderboard pages (LiveCodeBench, some SWE-bench Pro leaderboard tables) did not yield table data through `WebFetch`'s markdown conversion. Gaps are flagged `[unverified]` or noted explicitly rather than filled from memory.

## 1. SWE-bench and variants

All SWE-bench variants share the same core mechanic: give the agent a real GitHub issue and a repo checkout, require a patch, and check it against `FAIL_TO_PASS`/`PASS_TO_PASS` test sets. The harness is the `swebench` Python package, now maintained at [github.com/swe-bench/SWE-bench](https://github.com/swe-bench/SWE-bench) (checked 2026-08-12) — whether `princeton-nlp/SWE-bench` still redirects there or is a stale fork is `[unverified]`.

### SWE-bench (original / Full)

- Measures: issue resolution across 12 popular Python repos. 2,294 test instances (one source shows 2,290; 21,527 total across train/dev/test splits). Published 2023-10-10, paper [arXiv:2310.06770](https://arxiv.org/abs/2310.06770).
- Source: [huggingface.co/datasets/princeton-nlp/SWE-bench](https://huggingface.co/datasets/princeton-nlp/SWE-bench), [swebench.com](https://www.swebench.com) (both checked 2026-08-12).

### SWE-bench Lite

- 300 test instances + 23 dev instances, released alongside the main set as a cheaper/faster subset. Source: [huggingface.co/datasets/princeton-nlp/SWE-bench_Lite](https://huggingface.co/datasets/princeton-nlp/SWE-bench_Lite) (checked 2026-08-12). Current top-of-leaderboard score for Lite specifically was not retrieved this session `[unverified]`.

### SWE-bench Verified

- A 500-instance, human-filtered subset built with OpenAI: annotators confirmed each problem statement is clear, the test patch is correct, and the task is actually solvable. Source: [swebench.com/verified.html](https://www.swebench.com/verified.html) (checked 2026-08-12).
- The public leaderboard's default view evaluates all models with **mini-SWE-agent** (a minimal bash-only ReAct loop, no specialized scaffolding); other scaffolds (live-SWE-agent, TRAE, Sonar Foundation Agent, Atlassian Rovo Dev, ACoder, EPAM AI/Run) are submitted alongside.
- Top scores extracted from the rendered leaderboard table (most recent visible date **2026-02-26**, so not guaranteed to be the literal current-day SOTA):

  | Scaffold | Model | Score | Cost | Date |
  | --- | --- | --- | --- | --- |
  | live-SWE-agent | Claude 4.5 Opus (medium) | 79.20% | — | 2025-12-15 |
  | Sonar Foundation Agent | Claude 4.5 Opus | 79.20% | — | 2025-12-05 |
  | TRAE | Doubao-Seed-Code | 78.80% | — | 2025-09-28 |
  | live-SWE-agent | Gemini 3 Pro Preview | 77.40% | — | 2025-11-20 |
  | Atlassian Rovo Dev | (multiple models) | 76.80% | — | 2025-09-02 |
  | mini-SWE-agent | Claude 4.5 Opus (high) | 76.80% | $0.75 | 2026-02-17 |
  | mini-SWE-agent | Gemini 3 Flash (high) | 75.80% | $0.36 | 2026-02-17 |
  | mini-SWE-agent | MiniMax M2.5 (high) | 75.80% | $0.07 | 2026-02-17 |

  The table's rank ordinals showed apparent duplicates in extraction (likely two sub-tables — agentic-scaffold tab vs. mini-SWE-agent-only tab — concatenated by the render), so treat exact rankings as `[unverified]`; the scores/dates were read directly off the page.

- A secondary, non-primary source (byteiota.com) claims OpenAI abandoned SWE-bench Verified after finding ~59% of failing test cases flawed. **`[unverified]`** — no primary OpenAI statement located to confirm this.

### SWE-bench Multimodal

- Extends the issue-resolution format to visual/frontend domains (e.g. Chart.js-style UI issues). Dataset card states 617 instances; the HF dataset viewer showed 102 dev + 510 test = 612 rows — a minor discrepancy between the two views of the same primary source, not resolved. Source: [huggingface.co/datasets/princeton-nlp/SWE-bench_Multimodal](https://huggingface.co/datasets/princeton-nlp/SWE-bench_Multimodal) (checked 2026-08-12).
- Integrated into the leaderboard 2025-01-13 alongside `sb-cli`, a cloud evaluation tool for the held-out test set (predictions are submitted rather than scored locally, since gold labels for the true test split are withheld even though instance metadata is publicly browsable). Current top scores not retrieved this session `[unverified]`.

### SWE-bench Pro (Scale AI) — confirmed to exist

- Paper: [arXiv:2509.16941](https://arxiv.org/abs/2509.16941), submitted 2025-09-21, revised 2025-11-14. Leaderboards: [labs.scale.com/leaderboard/swe_bench_pro_public](https://labs.scale.com/leaderboard/swe_bench_pro_public) and a separate `_private` page. Open-source companion: [github.com/scaleapi/SWE-bench_Pro-os](https://github.com/scaleapi/SWE-bench_Pro-os).
- Measures long-horizon, enterprise-scale engineering tasks, explicitly designed to resist the contamination/leakage criticism leveled at the original SWE-bench: solutions average 107.4 lines of code across 4.1 files, framed as tasks taking hours to days rather than minutes.
- **1,865 total problems** across **41 actively maintained repos**, split into a public set (11 open-access repos), a held-out set (12 repos, restricted), and a commercial set (18 proprietary repos from partner startups). The leaderboard pages showed some conflicting repo-count labels across public/held-out/commercial splits — flagged `[unverified]` rather than reconciled.
- Reference harness is SWE-Agent (git submodule); a `mini-swe-agent` option was added 2025-10-28.
- Docker images published on DockerHub under `jefzda/sweap-images`, per-instance. Size and arm64/x86_64 availability were not documented in the fetched README — `[unverified]`.
- Baseline scores from the founding paper (not a live Aug-2026 snapshot — the live leaderboard table did not render through available fetch tools): public set, GPT-5 ~23.3%, Claude Opus 4.1 ~23.1%; private/commercial splits, Claude Opus 4.1 17.8-22.7% (inconsistent across two leaderboard pages), GPT-5 14.9%, GPT-4o 4.9%, Qwen-3 32B 3.4%. **A live, dated August 2026 SOTA figure for SWE-bench Pro was not confirmed this session — `[unverified]`.**

### Offline runnability across the SWE-bench family

Running the standard harness requires internet access for three things by default:

1. Pulling pre-built Docker images from DockerHub (namespace `swebench`, per-instance tags, one image per task so the exact commit/environment is reproducible)
2. Downloading the dataset via HuggingFace `load_dataset`
3. (For Multimodal's held-out test set) submitting to Scale's/the maintainers' cloud eval service, `sb-cli`, since gold labels aren't distributed

All three can be pre-cached for offline repeat runs: pull every needed image once and let Docker's local layer cache hold it, snapshot the HF dataset locally, and rely on the harness's own result cache (keyed by `run_id` + `instance_id`) to skip re-running identical instances. Local image builds are possible with `--namespace ''`, which is also the documented workaround for Arm/Apple Silicon, since **x86_64 is the primary supported architecture and arm64 is explicitly called "experimental."** Documented resource requirement for a full run: **at least 120GB free storage, 16GB RAM, 8 CPU cores**. Source: [github.com/swe-bench/SWE-bench](https://github.com/swe-bench/SWE-bench) (checked 2026-08-12).

### Known issues, cross-cutting

- Contamination/leakage concerns are widely discussed in secondary sources (blog posts, not peer-reviewed primaries): the argument is that much of SWE-bench performance reflects training-data leakage since most issues predate model knowledge cutoffs, citing large score drops (e.g. "70%+ down to ~23%") when moving to a contamination-resistant benchmark like SWE-bench Pro. These sources (agentmarketcap.ai, osmondvanhemert.nl, a Substack post) are secondary/opinion, not independently verified here.
- SWE-bench Pro's own design is explicitly a response to this criticism: longer-horizon tasks and held-out/commercial splits withheld from public training corpora.

## 2. Terminal-bench, Aider polyglot, LiveCodeBench, HumanEval(+), and newer 2025-2026 benchmarks

### Terminal-bench

- Site: [tbench.ai](https://www.tbench.ai) (formerly terminal-bench.com), repo: [github.com/laude-institute/terminal-bench](https://github.com/laude-institute/terminal-bench). Checked 2026-08-12/13.
- Measures whether an agent can complete real, end-to-end terminal tasks autonomously (compiling code, training models, setting up servers), inside a Docker-based sandboxed terminal environment, invoked via a `tb run` CLI. The repo puts the task count around 100 and calls itself still in beta with planned expansion.
- Multiple leaderboard generations are live simultaneously: Terminal-Bench 3 (newly shipped, table did not render for this research — `tbench.ai/leaderboard/terminal-bench/3.0` returned 404 at fetch time, `[unverified]` whether that's a broken link or an unpopulated page), 2.1 (live), 2.0 (live), 1.0 (legacy), and a Terminal-Bench-Science 1.0 in development.
- **This is the closest thing to a neutral, same-tasks, multi-agent-CLI leaderboard that exists.** The Terminal-Bench 2.1 table was independently verified by pulling the page's embedded JSON directly (not just trusting a page-summarizer), because one entry ("Fable 5" as a Claude model codename) looked suspicious and needed confirmation — it checked out, linking to a real Anthropic model announcement page. Full ranked table (accuracy over 445 trials each, checked 2026-08-12):

  | Rank | Agent | Model | Effort | Accuracy | Date |
  | --- | --- | --- | --- | --- | --- |
  | 1 | Claude Code | Fable 5 | xhigh | 83.82% | 2026-06-07 |
  | 2 | Codex | GPT-5.5 | xhigh | 83.15% | 2026-05-01 |
  | 3 | Terminus 2 | Fable 5 | high | 80.45% | 2026-06-05 |
  | 4 | Cursor CLI | Grok 4.5 | high | 79.33% | 2026-07-09 |
  | 5 | Claude Code | Opus 4.8 | high | 78.88% | 2026-07-09 |
  | 6 | Codex | GPT-5.6 Terra | max | 78.43% | 2026-07-11 |
  | 7 | Terminus 2 | GPT-5.5 | xhigh | 77.98% | 2026-05-01 |
  | 8 | mini-SWE-agent | Muse Spark 1.1 | xhigh | 76.18% | 2026-07-09 |
  | 9 | Codex | GPT-5.6 Luna | max | 75.73% | 2026-07-11 |
  | 10 | Claude Code | Sonnet 5 | high | 74.61% | 2026-07-09 |
  | 11 | Terminus 2 | Gemini 3 Pro | high | 73.93% | 2026-05-01 |
  | 12 | Claude Code | Opus 4.7 | max | 68.90% | 2026-05-01 |
  | 13 | Terminus 2 | Opus 4.7 | max | 66.07% | 2026-05-01 |
  | 14 | Gemini CLI | Gemini 3 Pro | high | 65.84% | 2026-05-01 |
  | 15 | Gemini CLI | Gemini 3.1 Pro | high | 65.84% | 2026-05-05 |
  | 16 | Terminus 2 | Gemini 3.1 Pro | high | 65.62% | 2026-05-05 |
  | 17 | Claude Code | GLM-5.1 | max | 58.65% | 2026-05-01 |

  Agents present: Claude Code, Codex (OpenAI), Terminus 2 (Terminal-Bench's own reference harness), Cursor CLI, mini-SWE-agent, Gemini CLI. **Not present**: OpenCode, Aider, GitHub Copilot CLI, Devin, Windsurf. Metric is `accuracy` with `accuracy_stderr`, plus `pass_at_k`, `total_cost_usd`, `reward_hacks`, and `output_tokens` per row — meaning Terminal-Bench's own submission schema already carries cost and token fields, though whether every submitter fills them consistently is unconfirmed.
- Offline runnability: task images are standard Docker/OCI, so pull-once-and-cache should work the way it does for any Docker harness, but this isn't documented as an explicit guarantee — `[unverified]`.
- Terminal-Bench's newer official harness, **Harbor** ([github.com/laude-institute/harbor](https://github.com/laude-institute/harbor)), is built to evaluate "arbitrary agents" and explicitly lists Claude Code, OpenHands, and Codex CLI as supported, plus third-party benchmark support for SWE-bench and Aider Polyglot, with cloud execution via Daytona/Modal/LangSmith. Whether its result schema captures cost/tokens/turns wasn't confirmed from the fetched page — `[unverified]`.

### Aider's polyglot benchmark

- Source: [aider.chat/docs/leaderboards](https://aider.chat/docs/leaderboards/) (checked 2026-08-12).
- Measures a model's ability to write/edit code correctly, unassisted, using 225 Exercism exercises across C++, Go, Java, JavaScript, Python, and Rust.
- The newest results visible on the fetched page are dated through October/November 2025 (GPT-5 high reasoning leading at 88.0%). No results newer than November 2025 were found — given Terminal-Bench already references GPT-5.5, Grok 4.5, and Opus 4.8 by mid-2026, **Aider's leaderboard may be stale relative to current frontier models as of August 2026** — `[unverified]` whether it has had a 2026 refresh.
- Offline runnability: the Exercism problem sets are static and typically vendored, so a fully offline run is plausible, but explicit no-network-at-runtime documentation wasn't located — `[unverified]`.

### LiveCodeBench

- Site: [livecodebench.github.io](https://livecodebench.github.io/). Checked 2026-08-12; the leaderboard page renders its table client-side via JavaScript, so `WebFetch` only saw a loading placeholder, and the GitHub repo fetch failed. **Current SOTA and any 2026-specific state are entirely `[unverified]`** — the only concrete data point available is the original paper (arXiv:2403.07974, March 2024), which predates this research window and shouldn't be cited as current.
- What is known and stable about the design: LiveCodeBench measures code generation, self-repair, test-output prediction, and code execution using problems scraped from LeetCode, AtCoder, and Codeforces, and handles contamination by tagging every problem with its release date so scoring can be restricted to problems released after a model's training cutoff (the paper's headline example: DeepSeek's accuracy dropped sharply on LeetCode problems released after its own release date, while GPT models stayed flatter — evidence of contamination on older problems for one model and not the other).
- Offline runnability wasn't confirmed from what loaded; problem sets are typically distributed as a versioned HuggingFace dataset, which would make pre-downloading straightforward, but this is inference, not confirmed — `[unverified]`.

### HumanEval / HumanEval+ (EvalPlus)

- Source: [github.com/evalplus/evalplus](https://github.com/evalplus/evalplus) (checked 2026-08-12).
- HumanEval+ and MBPP+ extend the original test suites with far more test cases (80x and 35x respectively) specifically to catch code that passes the original sparse tests but breaks on edge cases. The score delta between original and expanded suites is reported as a "coding rigorousness" signal.
- Offline runnability: **confirmed yes** — EvalPlus supports local evaluation via HuggingFace transformers or vLLM backends, with Docker-based sandboxed execution and no required external API dependency for scoring.
- Relevance in 2026: the repo predates the agentic-coding benchmark wave (Terminal-Bench, SWE-bench) and is likely used more as a regression/floor-level sanity check than a frontier differentiator today. This is an interpretive judgment from available evidence, not something the source states outright — `[unverified]/interpretive`.

### New 2025-2026 benchmarks found

Discovery here was constrained by the exhausted WebSearch budget — coverage is limited to benchmarks whose names/URLs were already known, not a fresh open-ended sweep. Absence of a benchmark from this list is not evidence it doesn't exist.

| Benchmark | What it measures | Offline runnability |
| --- | --- | --- |
| **Multi-SWE-bench** ([github.com/Multi-SWE-bench/Multi-SWE-bench](https://github.com/Multi-SWE-bench/Multi-SWE-bench)) | Extends SWE-bench to 7-8 languages (Java, TypeScript, JavaScript, Go, Rust, C, C++, +1). Full set 1,632 instances curated from 2,456 candidates, with mini (400) and flash (300) subsets for faster runs. | Explicitly designed for offline operation — data, code, and container images are all publicly released for local, self-contained Docker evaluation. |
| **SWE-Lancer** ([github.com/openai/SWELancer-Benchmark](https://github.com/openai/SWELancer-Benchmark)) | Whether frontier LLMs can complete real Upwork-style freelance software engineering tasks (framed around "$1M" of real task value). | **Repo archived 2025-07-18**, now read-only; maintained location moved to [github.com/openai/preparedness](https://github.com/openai/preparedness) (not independently fetched). Task count, Docker use, and offline runnability `[unverified]`. |
| **BigCodeBench** ([github.com/bigcode-project/bigcodebench](https://github.com/bigcode-project/bigcodebench)) | 1,140 function-level, tool-use-heavy programming tasks, plus a 148-task "Hard" subset. | Supports local execution as well as E2B sandbox/remote backends — usable offline. Repo itself archived 2026-07-20, but the HF Spaces leaderboard is reported still active (163 models evaluated as of the last noted release, v0.2.2.dev2); still cited by Meta AI, DeepSeek, Qwen, AWS AI per the repo. |
| **Commit0** ([commit-0.github.io](https://commit-0.github.io/)) | Has an agent rebuild 54 real Python libraries from scratch (ranging from wcwidth at 38 tests to xarray at 15,643 tests and web3.py at 40,433 tests) against the libraries' existing test suites, docs, linting, and type-checking. | Not explicitly documented; the harness's isolated-environment and optional cloud-distribution design implies at least some setup-time network dependency — `[unverified]`. |
| **CORE-Bench** ([github.com/siegelz/core-bench](https://github.com/siegelz/core-bench); core-bench.github.io 404'd) | 270 tasks across 90 real scientific papers (CS, social science, medicine; Python/R), testing whether an agent can computationally reproduce a paper's results end to end. | Auto-downloads each task's code repo on first run; after that initial fetch it's cacheable and can run fully offline — one of the more clearly offline-friendly benchmarks found. |
| **METR / RE-Bench** | Not resolved — `metr.org/re-bench` 404'd and no search budget remained to find a current URL. | Entirely unverified; nothing reported rather than guessed. |

### Offline-runnability summary across section 2

| Benchmark | Needs live internet for anything? | Pre-cacheable for offline repeat runs? |
| --- | --- | --- |
| Terminal-Bench | Docker images per task | Likely yes (standard pull-and-cache), not documented as a guarantee `[unverified]` |
| Aider polyglot | Not confirmed | Static Exercism set, plausible but `[unverified]` |
| LiveCodeBench | Not confirmed (docs unreachable this session) | `[unverified]` |
| HumanEval+/MBPP+ (EvalPlus) | No | Yes, confirmed |
| Multi-SWE-bench | No, after initial asset download | Yes, confirmed |
| SWE-Lancer | Not confirmed (repo moved) | `[unverified]` |
| BigCodeBench | No | Yes, confirmed |
| Commit0 | Not confirmed | `[unverified]`, leans toward some setup-time network need |
| CORE-Bench | Yes, but only at first run | Yes, confirmed cacheable after initial download |

## 3. Instrumenting a harness for apples-to-apples metrics

**No single existing tool captures all five target metrics (wall-clock, token breakdown, dollar cost, pass rate, turn/tool-call count) across different agent CLIs in one unified schema.** The closest pieces, and how they'd need to be combined:

- **SWE-bench's official harness**: correctness only. No cost, token, or turn fields in the evaluation output — it produces resolve/fail outcomes per instance. Source: [github.com/SWE-bench/SWE-bench](https://github.com/SWE-bench/SWE-bench) (checked 2026-08-13).
- **Terminal-Bench's harness**: mixed. The source tree includes an `agents/installed_agents/` directory with wrappers for ClaudeCode, Codex, GrokCli, GeminiCli, OpenCode, QwenCode, Aider, Goose, CursorCli, MiniSweAgent, and OpenHands — i.e. this is already a multi-agent-CLI surface. Reading `claude_code_agent.py` directly: it builds a `claude --output-format stream-json` command but **does not parse the resulting JSON stream for tokens/cost/turns** — it returns the terminal command object only, even though Claude Code's own stream carries that data. `[unverified]` whether a downstream reporting layer elsewhere in the repo extracts it. The Terminal-Bench 2.1 leaderboard schema itself (see section 2) does carry `total_cost_usd` and `output_tokens` fields per submitted row, so cost/token capture does happen somewhere in the submission pipeline, just apparently not inside the adapter code inspected.
- **Harbor** (Terminal-Bench's newer, more general harness): explicitly designed to evaluate arbitrary agents including Claude Code, OpenHands, and Codex CLI, with third-party support for SWE-bench and Aider Polyglot. Whether its result schema captures cost/tokens/turns is `[unverified]` from what was fetched.
- **Inspect AI** ([github.com/UKGovernmentBEIS/inspect_ai](https://github.com/UKGovernmentBEIS/inspect_ai), the UK AI Security Institute's framework): its `EvalStats`/eval-log format documents input/output token statistics as a built-in field. The standout mechanism is **`sandbox_agent_bridge()`**: it runs a proxy inside the task sandbox on `localhost:13131` and intercepts the agent's outbound calls to OpenAI/Anthropic/Google-compatible APIs, routing them through Inspect's own model-provider layer. The docs specifically name Claude Code, Codex CLI, and Gemini CLI as agents that can be pointed at that local port via env vars, after which their calls flow through Inspect's standard transcript/token/cost accounting. This is language-agnostic bridging — the agent doesn't need to be written in Python or use Inspect's SDK — and is **the strongest lead found for true cross-agent apples-to-apples accounting**, because the accounting logic lives in Inspect rather than in each agent's own self-reporting. Source: [inspect.aisi.org.uk/agent-bridge.html](https://inspect.aisi.org.uk/agent-bridge.html) (checked 2026-08-13).
- **promptfoo**: has coding-agent tooling but it's aimed at red-teaming/security testing (prompt injection, sandbox escape), not benchmark scoring with cross-provider cost/token accounting.
- **LiteLLM** ([github.com/BerriAI/litellm](https://github.com/BerriAI/litellm)): a viable proxy layer — any agent that supports pointing its `OPENAI_BASE_URL`/`ANTHROPIC_BASE_URL` (or equivalent) at a proxy gets consistent token/cost/latency logging independent of which CLI is calling, via LiteLLM's built-in cost tracking, per-project spend tracking, and callback integrations (Langfuse, MLflow, etc.). It does not do task scoring or turn counting itself — it's the API-call accounting layer only, and needs to be paired with a harness for pass/fail and turn counts.
- **Claude Code's own telemetry** ([code.claude.com/docs/en/monitoring-usage](https://code.claude.com/docs/en/monitoring-usage), checked 2026-08-13): the richest single-agent self-reporting found.
  - OTEL metrics: `claude_code.session.count`, `claude_code.token.usage`, `claude_code.cost.usage`, `claude_code.lines_of_code.count`, `claude_code.code_edit_tool.decision`, `claude_code.active_time.total`, exportable via OTLP (gRPC/HTTP) or scraped as Prometheus at `localhost:9464/metrics`.
  - OTEL events: `claude_code.api_request` carries per-call `input_tokens`, `output_tokens`, `cache_read_tokens`, `cache_creation_tokens`, `cost_usd`, and `model`, correlatable across a session via a `prompt.id` attribute.
  - Session JSONL transcripts at `~/.claude/projects/*/*.jsonl` carry full turn-by-turn history a harness could parse to reconstruct turn counts, though the docs flag the format as version-specific, not a stable contract.
  - Env toggles: `CLAUDE_CODE_ENABLE_TELEMETRY=1`, `OTEL_METRICS_EXPORTER`, `OTEL_LOG_USER_PROMPTS`, `OTEL_LOG_TOOL_DETAILS`.
  - This is Claude-Code-specific self-reporting, not a cross-agent normalizer. A harness comparing Claude Code against Aider or Codex CLI would need equivalent scraping logic per agent (each has its own cost/token display) or route everything through a shared layer (LiteLLM or Inspect's bridge) for one consistent schema.

### Summary table

| Tool | Multi-agent generic? | Cost | Tokens (in/out/cached) | Turns/tool calls | Wall-clock | Pass/fail | Maintained |
| --- | --- | --- | --- | --- | --- | --- | --- |
| SWE-bench harness | No (patch format only) | No | No | No | No | Yes | Active, last visible update noted 2025-01 |
| Terminal-Bench 1.x harness | Adapters exist for many CLIs, but doesn't parse their token/cost streams (confirmed from source) | Present in leaderboard submission schema, not in adapter | Present in leaderboard schema, not in adapter | `[unverified]` | `[unverified]` | Yes | Active |
| Harbor | Yes, explicit design goal | `[unverified]` | `[unverified]` | `[unverified]` | `[unverified]` | Yes | Active |
| Inspect AI core | Yes for framework-native agents | `[unverified]` | Yes (input/output confirmed) | `[unverified]` | `[unverified]` | Yes | Active |
| Inspect AI `sandbox_agent_bridge` | Yes, bridges any external CLI via localhost proxy | Inherited from Inspect model tracking | Inherited | Recorded per interaction | `[unverified]` | Via Inspect scorers | Active |
| LiteLLM proxy | Yes, provider-agnostic, no task scoring | Yes | Yes | No (not a harness) | Yes (latency) | No | Active, high commit velocity |
| Claude Code OTEL | No (Claude Code only) | Yes | Yes, incl. cache | Reconstructable from JSONL, no native turn counter | Via `active_time.total` | No | Active |

**Practical implication for a custom harness**: the pragmatic build is a task/scoring layer (reuse SWE-bench's harness or Terminal-Bench/Harbor for pass/fail) combined with a cost/token accounting layer (either Claude Code's own OTEL export for Claude Code specifically, or a shared LiteLLM proxy / Inspect bridge for cross-agent consistency), plus a thin wrapper that times each run and counts turns from session transcripts. Nothing surveyed provides this end to end for arbitrary agent CLIs out of the box.

## 4. Existing head-to-head comparisons of Claude Code vs other agents

**The one credible, independently verifiable, same-tasks leaderboard is Terminal-Bench 2.1** (table reproduced in section 2). Verified directly against the page's embedded JSON, not just a page-summary. As of the most recent entries (checked 2026-08-12): Claude Code (running the "Fable 5" model) tops the board at 83.82%, with OpenAI's Codex (GPT-5.5) a close second at 83.15% and newer GPT-5.6 variants close behind at ranks 6 and 9. The gap between the top Claude Code entry and the top Codex entry is under 1 point. Agents present on this board: Claude Code, Codex, Terminus 2, Cursor CLI, mini-SWE-agent, Gemini CLI. **Not present**: OpenCode, Aider, GitHub Copilot CLI, Devin, Windsurf.

Other sources checked:

- **SWE-bench** ([swebench.com](https://www.swebench.com)): traditionally ranks model+scaffold pairs (OpenHands, Agentless, mini-SWE-agent, etc.) rather than commercial CLI products by name, so Claude Code doesn't appear as a named scaffold on the visible leaderboard content. `[unverified]` whether this reflects Claude Code simply not being submitted, versus a deeper reason.
- **METR** ([metr.org/blog](https://metr.org/blog)): no blog posts comparing Claude Code against other coding CLIs found; their coding-agent work is model-level safety/capability evaluation, not CLI-vs-CLI comparison.
- **OpenHands / All Hands AI** ([openhands.dev/blog](https://www.openhands.dev/blog)): one relevant post, "Claude Code vs Cursor: Which AI Coding Tool to Use in 2026" (2026-08-10). **This is vendor marketing, not a benchmark** — no task-based scores, qualitative comparison only, and it ends by positioning OpenHands itself as a complementary "platform layer." Flagged as marketing, not evidence.
- **Epoch AI** ([epoch.ai/blog](https://epoch.ai/blog)): no Claude Code vs. competitor comparisons found. The closest adjacent piece concerns AI contributions to the Codex project's own codebase, not a CLI-vs-CLI benchmark.
- **Anthropic's own Opus 4.8 announcement** (anthropic.com/news/claude-opus-4-8): cites Terminal-Bench 2.1 and states, in a footnote, that "GPT-5.5's reported score with the Codex CLI harness is 83.4%." This differs slightly from the 83.15% shown on the live tbench.ai leaderboard for the same Codex/GPT-5.5 pairing — likely a different eval run or self-reported figure vs. the leaderboard's submitted run. `[unverified]` which number is authoritative; flagged as a minor, unreconciled discrepancy between a vendor claim and the independent leaderboard.
- **[github.com/EkagraAgarwal/harness-bench](https://github.com/EkagraAgarwal/harness-bench)**: a Python framework running 39 tasks (6 real OSS repos, 8 SWE-bench Verified instances, 25 Terminal-Bench 2.1 tasks) against BanyanCode, OpenCode, Aider, Claude Code, and Codex CLI, with stubbed support for Continue, Copilot, and Cursor. Tracks 15 metrics including pass-rate@k, cost, wall-clock time, and token usage — structurally, this is close to what a custom harness for this task would look like. However: it's maintained by the BanyanCode team and lists their own product first in every comparison, has 0 stars/forks, and no populated results were visible in the README (a `results/pilot-k1/` directory is referenced but not shown). Treat as an early-stage, vendor-adjacent project with unconfirmed actual run results, not a maintained neutral leaderboard.

**Bottom line**: outside Terminal-Bench's own leaderboard, no independent, neutral, same-tasks comparison of Claude Code against Aider/OpenCode/Cursor CLI specifically was found. Given the WebSearch outage during this research pass, this is a gap in coverage, not confirmation that nothing else exists — a follow-up pass with a fresh search budget on queries like `"claude code vs aider benchmark 2026"` and `"agent arena" coding CLI benchmark 2026` would be worth running before treating this as exhaustive.

## 5. Docker/sandboxing on a Mac

This section is the thinnest in the doc: the dedicated research agent assigned to it did not return results before this document was finalized, so it combines a couple of direct fetches with well-established, low-volatility facts about Docker on Apple Silicon. Anything benchmark-specific is flagged.

- **Docker Desktop on Apple Silicon**: Docker Desktop runs containers inside a lightweight Linux VM regardless of host architecture. Native arm64 images run without emulation. Cross-architecture images (the amd64-only images that most SWE-bench task images are built as, per section 1) run via QEMU-based binfmt emulation, which carries real CPU overhead for compute-heavy workloads — this is a well-known, stable characteristic of cross-arch container emulation generally, but a specific multiplier (e.g. "Nx slower") for SWE-bench task images specifically was not measured or sourced this session — `[unverified]`. The SWE-bench harness's own docs call arm64 "experimental" and recommend `--namespace ''` to build images locally on Apple Silicon rather than pulling amd64 images (source: section 1, [github.com/swe-bench/SWE-bench](https://github.com/swe-bench/SWE-bench)).
- **OrbStack** ([orbstack.dev](https://orbstack.dev), checked 2026-08-13): a Docker Desktop alternative built for macOS, positioned as faster and lighter (its own marketing cites roughly 17 vs. 45 minutes to provision a dev environment, and under 0.1% background CPU / under 10MB initial disk footprint, though these are self-reported vendor figures from an August 2023 benchmark, not independently re-verified here). It supports Rosetta-based x86 emulation for running amd64 containers on Apple Silicon. Whether the SWE-bench harness or Terminal-Bench's CLI work with OrbStack out of the box (vs. needing adaptation) was not confirmed this session — `[unverified]`, though since OrbStack presents a standard Docker-compatible socket/API, most Docker-based tooling is expected to work without modification as a general property of the tool, not something specific to these benchmarks.
- **Colima, Podman, Apple's native containerization framework**: not independently researched this session. Apple introduced a native `container`/Containerization framework for running Linux containers on Apple Silicon around 2025; its current maturity, and whether SWE-bench's or Terminal-Bench's harnesses work with it, is **entirely `[unverified]`** — a direct fetch attempt against `apple.github.io/container/` returned no usable content this session.
- **Isolation strategy**: SWE-bench's harness gives each task instance its own tagged Docker image (a fresh container per instance is the documented model, not a shared container with reset state), which is what makes exact-commit reproducibility possible. Terminal-Bench similarly runs each task inside a sandboxed terminal container per the repo's stated design. Neither harness's specific network-isolation enforcement mechanism (e.g. `--network none` at container run time to prevent the agent from exfiltrating test data or fetching answers) was confirmed from primary docs this session — `[unverified]`, though disabling network access during the graded run is a standard practice for benchmarks concerned about answer leakage, and would be a reasonable default to enforce manually if the harness doesn't already do it.
- **Cleanup for repeated runs**: not independently researched this session; standard Docker hygiene (`docker system prune`, `docker image prune -a` between benchmark sweeps, and per-instance volume removal after each task) applies generically and isn't benchmark-specific.

**This section needs a dedicated follow-up pass** before being treated as load-bearing for an actual Mac setup decision — it's the one area of this doc built mostly from general Docker knowledge rather than confirmed primary-source research on the specific harnesses.

## 6. Realistic scope for a solo dev

Like section 5, the dedicated research agent for this topic did not return before this document was finalized. What follows is inferred from data surfaced incidentally in other lanes, clearly labeled, plus the well-established mechanics of the benchmarks already documented above — treat the concrete cost/hour figures as `[unverified]` estimates, not sourced measurements.

- **What is known**: SWE-bench Verified's leaderboard reports a per-model `cost` figure for at least some entries — e.g. mini-SWE-agent with Claude 4.5 Opus (high) cost $0.75 to reach 76.80% (section 1), while MiniMax M2.5 (high) cost $0.07 for 75.80%. That's a per-instance-averaged cost implied by the leaderboard's reporting convention, though whether it's per-instance or per-full-run wasn't confirmed — `[unverified]`. If per-instance, running the full 500-instance Verified set with a similar scaffold would be on the order of tens to hundreds of dollars depending on model choice, which is a rough inference, not a sourced total.
- **Statistical variance**: Terminal-Bench's 2.1 leaderboard explicitly reports `accuracy_stderr` alongside `accuracy` for every entry (section 2), which is a strong methodological signal that this benchmark treats single-run pass rates as noisy and expects multiple trials — the table showed 445 trials per entry, i.e. the benchmark's own task count times repeated runs, not 445 unique tasks. This is the clearest evidence found that a credible comparison needs repeated trials rather than a single pass/fail run per task, though the exact recommended trial count for a *reduced* task set was not sourced.
- **Pragmatic reduced-scope proposal** (inferred, not sourced from a specific "how many tasks do you need" study): given Terminal-Bench's own practice of reporting stderr over ~4-5 trials per task at their full ~100-task scale, a solo dev wanting a repeatable side-by-side of a handful of agents without runaway cost could reasonably target a stratified subset of roughly 20-30 tasks (spanning easy/medium/hard difficulty and a few different repo/language types) run 3 times each per agent, which keeps total runs in the 60-90 range per agent while still surfacing variance rather than hiding it behind single-run numbers. This is a reasoned proposal built from the evidence gathered, not a validated methodology from a published source, and should be treated as a starting point to adjust once real cost/time-per-task numbers are measured locally.
- **What's missing**: actual reported wall-clock and dollar-cost figures for running SWE-bench Lite or Verified end to end (from teams who've published the experience), and the same for Terminal-Bench, were not retrieved this session. A follow-up search pass focused specifically on GitHub issue/discussion threads and blog posts reporting real run costs (queries like `"cost to run SWE-bench Verified"`, `"time to run terminal-bench"`) would close this gap.

## Recommended offline benchmark harness design

**What to reuse rather than build:**

- **Task/scoring layer**: reuse an existing harness rather than writing a scorer from scratch. Terminal-Bench (or its successor, Harbor) is the best fit for this project's stated goal, because it already has installed-agent adapters for Claude Code, Codex, Cursor CLI, Gemini CLI, OpenCode, and Aider, and its leaderboard schema already carries `total_cost_usd` and `output_tokens` fields — meaning the target metric set is already part of its data model, even if the specific Claude Code adapter doesn't currently populate those fields from Claude Code's own stream. For issue-resolution-style tasks specifically (rather than general terminal tasks), SWE-bench's harness is the right reuse target, accepting its Docker/HuggingFace/GitHub dependencies at setup time and its arm64-as-experimental status on Apple Silicon.
- **Cost/token accounting layer**: reuse Claude Code's own OTEL export (`claude_code.api_request` events carry `input_tokens`, `output_tokens`, `cache_read_tokens`, `cache_creation_tokens`, `cost_usd`) for the Claude Code side specifically, since it's already richer than anything a custom scraper would produce. For a genuinely cross-agent-consistent version of the same accounting, reuse either a LiteLLM proxy (redirect each agent's API base URL through it) or Inspect AI's `sandbox_agent_bridge()`, which was the strongest lead found for a single accounting mechanism that works across Claude Code, Codex CLI, and Gemini CLI without modifying any of them.

**What to build:**

- A thin orchestration wrapper that: (a) picks a stratified task subset (per section 6's reasoning) from an existing suite rather than inventing new tasks, (b) launches each agent CLI against each task inside a per-task-fresh container (following SWE-bench's and Terminal-Bench's own per-instance isolation model rather than reusing a container across tasks), (c) times wall-clock per task, (d) parses each agent's session transcript (Claude Code's JSONL, or the equivalent for other CLIs) to reconstruct turn/tool-call counts, since no surveyed tool does this generically across agents, and (e) merges the scoring layer's pass/fail with the cost/token layer's per-call data and the wrapper's own timing/turn counts into one normalized results table (one row per task x agent x trial).
- Explicit network isolation at container run time (`--network none` during the graded portion of each task) if the reused harness doesn't already enforce it, since this wasn't confirmed as a given for either SWE-bench or Terminal-Bench in this research pass.
- A pre-caching step run once, offline-enabling: pull every Docker image the chosen task subset needs, snapshot the relevant HuggingFace dataset locally, and vendor the Aider/Exercism problem set if that suite is included, so subsequent runs need zero network access beyond whatever the agent CLI itself needs to call its model API.

**What this design deliberately does not attempt**: a from-scratch scorer, a from-scratch task set, or a from-scratch multi-agent cost normalizer. Every piece with an existing, actively maintained implementation (task definitions, Docker isolation model, per-call cost accounting) is reused; the only genuinely new code is the thin cross-agent orchestration and transcript-parsing wrapper that ties reused pieces into one comparable table, because that specific combination does not exist as a maintained open-source project as of this research pass (the closest attempt found, `harness-bench`, is unmaintained-looking and vendor-adjacent — see section 4).

## Sources

- [swebench.com](https://www.swebench.com) / [swebench.com/verified.html](https://www.swebench.com/verified.html)
- [github.com/swe-bench/SWE-bench](https://github.com/swe-bench/SWE-bench)
- [huggingface.co/datasets/princeton-nlp/SWE-bench](https://huggingface.co/datasets/princeton-nlp/SWE-bench), [SWE-bench_Lite](https://huggingface.co/datasets/princeton-nlp/SWE-bench_Lite), [SWE-bench_Multimodal](https://huggingface.co/datasets/princeton-nlp/SWE-bench_Multimodal)
- [arxiv.org/abs/2509.16941](https://arxiv.org/abs/2509.16941) (SWE-Bench Pro paper)
- [labs.scale.com/leaderboard/swe_bench_pro_public](https://labs.scale.com/leaderboard/swe_bench_pro_public), [labs.scale.com/leaderboard/swe_bench_pro_private](https://labs.scale.com/leaderboard/swe_bench_pro_private)
- [github.com/scaleapi/SWE-bench_Pro-os](https://github.com/scaleapi/SWE-bench_Pro-os)
- [tbench.ai/leaderboard/terminal-bench/2.1](https://www.tbench.ai/leaderboard/terminal-bench/2.1)
- [github.com/laude-institute/terminal-bench](https://github.com/laude-institute/terminal-bench), [github.com/laude-institute/harbor](https://github.com/laude-institute/harbor)
- [aider.chat/docs/leaderboards](https://aider.chat/docs/leaderboards/)
- [livecodebench.github.io](https://livecodebench.github.io/)
- [github.com/evalplus/evalplus](https://github.com/evalplus/evalplus)
- [github.com/Multi-SWE-bench/Multi-SWE-bench](https://github.com/Multi-SWE-bench/Multi-SWE-bench)
- [github.com/openai/SWELancer-Benchmark](https://github.com/openai/SWELancer-Benchmark), [github.com/openai/preparedness](https://github.com/openai/preparedness)
- [github.com/bigcode-project/bigcodebench](https://github.com/bigcode-project/bigcodebench)
- [commit-0.github.io](https://commit-0.github.io/)
- [github.com/siegelz/core-bench](https://github.com/siegelz/core-bench)
- [github.com/SWE-bench/SWE-bench](https://github.com/SWE-bench/SWE-bench) (harness instrumentation lane)
- [github.com/UKGovernmentBEIS/inspect_ai](https://github.com/UKGovernmentBEIS/inspect_ai), [inspect.aisi.org.uk/agent-bridge.html](https://inspect.aisi.org.uk/agent-bridge.html), [inspect.aisi.org.uk/eval-logs.html](https://inspect.aisi.org.uk/eval-logs.html)
- [github.com/BerriAI/litellm](https://github.com/BerriAI/litellm)
- [code.claude.com/docs/en/monitoring-usage](https://code.claude.com/docs/en/monitoring-usage)
- [promptfoo.dev/docs/red-team/agents](https://www.promptfoo.dev/docs/red-team/agents/)
- [metr.org/blog](https://metr.org/blog)
- [openhands.dev/blog/claude-code-vs-cursor](https://www.openhands.dev/blog/claude-code-vs-cursor)
- [epoch.ai/blog](https://epoch.ai/blog)
- [anthropic.com/news/claude-opus-4-8](https://www.anthropic.com/news/claude-opus-4-8)
- [github.com/EkagraAgarwal/harness-bench](https://github.com/EkagraAgarwal/harness-bench)
- [orbstack.dev](https://orbstack.dev)

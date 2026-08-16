# Existing alternatives landscape: open-source Claude-Code-like agents (August 2026)

Research date: 2026-08-12/13. Facts below come from the GitHub REST API, repo READMEs/LICENSE files fetched directly, and HN Algolia search, not from model memory. Where I could not pin a claim to a primary source it's marked `[unverified]`.

**Method note.** This session's WebSearch budget was already exhausted by other concurrent work, so this research runs on direct `WebFetch` calls against `api.github.com`, raw GitHub file URLs, project sites, and the HN Algolia search API instead of general web search. That's a narrower net than usual: it covers every tool named in the brief and follows the leads GitHub/HN surfaced, but a broader web sweep might turn up smaller indie projects HN and GitHub metadata don't.

## TL;DR

The space filled in fast. There are now several credible, actively-maintained open-source Claude Code alternatives, and one of them (Charm's Crush) is already Go, already Bubble-Tea-adjacent, already supports Ollama/llama.cpp/LM Studio out of the box, and ships under a source-available license that converts to MIT after two years. Nobody has built the specific combination this project wants: narrow single-user scope, hybrid local/cloud routing, a mobile companion with real push notifications, and a built-in benchmark harness that compares itself to Claude Code. Those four gaps are real, but three of them are additive features you could plausibly build on top of Crush rather than reasons to start a TUI and agent loop from zero.

## 1. OpenCode — two unrelated projects share the name

**anomalyco/opencode** (the one behind opencode.ai) is the one people mean when they say "OpenCode" in 2026.

- Repo: [github.com/anomalyco/opencode](https://github.com/anomalyco/opencode). It was originally created under the `sst` org (SST, the Adam Elmore / Dax "Serverless Stack" team) and now lives under `anomalyco`; `github.com/sst/opencode` redirects there. I could not find a blog post or announcement explaining the org move (HN Algolia search returned zero hits for it), so treat the reason for the transfer as `[unverified]` — the transfer itself is directly observable from the redirect and the `full_name` field in the GitHub API response.
- Language: TypeScript, monorepo built with Turbo/Bun. Terminal UI plus a desktop app (macOS/Windows/Linux) and an IDE extension — not a Go/Bubble Tea project.
- Local model support: yes, via [Models.dev](https://models.dev) (also an SST-adjacent project), which the docs describe as routing to "75+ LLM providers … including local models." No Ollama-specific setup instructions appear on the docs page I fetched.
- Maturity: 196,621 stars, 25,273 forks, 5,129 open issues, last push 2026-08-13 (today, per the API). License: MIT. Created 2025-04-30, so roughly 15 months of growth to nearly 200k stars — the fastest-growing project in this whole survey. Source: [api.github.com/repos/anomalyco/opencode](https://api.github.com/repos/anomalyco/opencode), checked 2026-08-13.

**opencode-ai/opencode** is a different, older, unrelated project — also called "OpenCode," which is the source of the naming collision.

- Repo: [github.com/opencode-ai/opencode](https://github.com/opencode-ai/opencode). Created 2025-03-16 (about six weeks before the SST/Anomaly one), written in **Go**, MIT licensed.
- Status: **archived**, last push 2025-09-18. 13,636 stars, 1,769 forks. It stopped moving roughly a year before this research date. Source: [api.github.com/repos/opencode-ai/opencode](https://api.github.com/repos/opencode-ai/opencode), checked 2026-08-13.
- This is the closest prior art to "Go coding agent CLI called OpenCode" and it's dead. Nobody picked it back up as of this check.

## 2. Aider

- Repo: [github.com/Aider-AI/aider](https://github.com/Aider-AI/aider) (formerly paul-gauthier/aider). Python, Apache-2.0. 48,153 stars, 4,834 forks, 1,793 open issues. Created 2023-05-09. Source: [api.github.com/repos/Aider-AI/aider](https://api.github.com/repos/Aider-AI/aider), checked 2026-08-13.
- **Last push: 2026-05-22** — about three months stale relative to this research date, while every other actively-developed project in this survey (OpenCode, Crush, Goose, Cline, Continue, Codex, Gemini CLI) shows a push timestamp of 2026-08-13 (the day I checked). That gap is the single clearest maintenance-momentum signal in this research: Aider isn't archived, but it has visibly slowed relative to the rest of the field.
- Architecture: pure terminal chat interface, no TUI framework — line-based prompts, not panels/widgets. Repo-map context strategy (a static-analysis-derived map of the codebase fed into the prompt) and search/replace or unified-diff style edits, configurable per model.
- Local models: connects through LiteLLM-style provider config to "almost any LLM including local models"; the README doesn't call out Ollama specifically in the section I fetched, but Aider's long-standing docs (outside this fetch) do document Ollama/llama.cpp setups — flagged `[unverified]` here since I didn't independently confirm the current docs page.
- Benchmark culture: Aider publishes its own leaderboards (an "OpenRouter Ranking" badge shows Aider ranked in the platform's top 20 tools) and a "Singularity" badge claiming 88% of the code in its most recent release was written by Aider itself. This benchmark-driven, dogfooding culture is real and distinctive among the tools surveyed — no other project in this list makes an equivalent self-authorship claim.
- License: Apache-2.0.

## 3. Cline, Continue.dev, Goose, Crush, and Go-specific agents

### Cline

- Repo: [github.com/cline/cline](https://github.com/cline/cline). TypeScript/Node. Apache-2.0. 66,085 stars, 7,098 forks, 982 open issues. Created 2024-07-06, pushed 2026-08-13. Source: [api.github.com/repos/cline/cline](https://api.github.com/repos/cline/cline), checked 2026-08-13.
- Architecture: not just a VS Code extension anymore. As of this check it ships a VS Code extension, a JetBrains plugin, a standalone CLI (`npm i -g cline`), a Node SDK, and a "Kanban" web interface for coordinating multiple agent runs. Genuinely multi-surface.
- Local models: Ollama and LM Studio explicitly documented, plus any OpenAI-compatible self-hosted endpoint.

### Continue.dev — discontinued

- Repo: [github.com/continuedev/continue](https://github.com/continuedev/continue). TypeScript, Apache-2.0, 35,459 stars. The GitHub API still reports `archived: false` and a same-day push timestamp, but the actual README (fetched directly from `raw.githubusercontent.com`, checked 2026-08-13) states, verbatim: *"The `continuedev/continue` repository is no longer actively maintained and is read-only for all users."* It describes a final 2.0.0 release across the CLI, VS Code extension, and JetBrains plugin that removed telemetry and auth, then stopped. This is a confirmed end-of-life, not a slowdown — worth noting since the raw API metadata alone would have missed it.
- Continue did ship a standalone CLI before end-of-life, so it briefly competed in this exact space.

### Goose (Block)

- Repo: [github.com/aaif-goose/goose](https://github.com/aaif-goose/goose) — note the org: it now lives under the **Agentic AI Foundation** (an AAIF Linux Foundation project), not directly under `block`, though Block originated it (`github.com/block/goose` redirects here). Rust, Apache-2.0. 52,736 stars, 5,996 forks, only 253 open issues (unusually low ratio to stars, suggesting active triage). Created 2024-08-23, pushed 2026-08-13. Source: [api.github.com/repos/aaif-goose/goose](https://api.github.com/repos/aaif-goose/goose), checked 2026-08-13.
- Architecture: desktop app, CLI, and API, 15+ LLM providers, 70+ MCP extensions. Ollama is an explicit supported provider.
- Governance under a Linux Foundation-adjacent foundation rather than a single company is distinctive among this set and signals long-term maintenance commitment beyond one vendor's roadmap.

### Crush (Charm) — the closest architectural match to what this project wants

- Repo: [github.com/charmbracelet/crush](https://github.com/charmbracelet/crush). **Go**, built by Charm (makers of Bubble Tea, Lipgloss, Glow — the exact TUI ecosystem this project would use). 27,311 stars, 2,152 forks, 623 open issues. Created 2025-05-21, pushed 2026-08-13 (today). Source: [api.github.com/repos/charmbracelet/crush](https://api.github.com/repos/charmbracelet/crush), checked 2026-08-13.
- Local models: explicit, first-class support for Ollama (with auto-discovery), llama.cpp (`llama-server`), LM Studio, and OMLX, plus custom OpenAI/Anthropic-compatible endpoints and LiteLLM. This is the most complete local-model story of any tool in this survey.
- Architecture: LSP integration for context, MCP server support (stdio/HTTP/SSE), permission-based tool execution, session/workspace sharing.
- Design philosophy: README language is "Glamourous agentic coding for all" and "Works Everywhere" — explicitly broad-platform and general-purpose, not narrow or single-user-optimized. It does **not** mention subagents, a mobile companion app, push notifications, or any built-in comparison against Claude Code. Positioning leans on flexibility and "industrial-grade reliability," not raw speed benchmarks.
- License: **FSL-1.1-MIT** (Functional Source License) — source-available, not pure open source. Covered in detail in the licensing section below.

### Other Go-specific agents

I did not find a second Go-native coding-agent CLI with meaningful traction beyond `opencode-ai/opencode` (archived) and `charmbracelet/crush` (active). The HN Algolia search for Go/Bubble Tea coding-agent projects surfaced nothing beyond these two. That's a genuine gap in coverage from the search-budget constraint noted above — a deeper sweep of `awesome-*` lists might find smaller Go projects this pass missed. Flagging as `[unverified: absence]` rather than a confirmed "nothing else exists."

## 4. Anything optimized for efficiency/speed/local-first as the primary goal?

Nothing in this survey states "narrow scope" or "single-user speed" as its primary design goal the way this project's brief does. The closest matches, ranked:

1. **Crush** — closest on architecture (Go, Bubble-Tea ecosystem, best local-model support) but explicitly positions itself as broad/general-purpose ("for all," "works everywhere"), not narrow or minimal. Direct overlap on tech stack and local-first capability; no overlap on the "narrow, single-power-user" philosophy.
2. **Aider** — closest on "do one thing" philosophy (no TUI, no desktop app, no IDE extension, just a terminal chat loop) and has genuine local-model support, but it's Python, not Go/Bubble Tea, and its benchmark culture is about model quality, not tool speed.
3. **opencode-ai/opencode (archived)** — was Go and terminal-only, closest possible prior art for "narrow Go TUI agent," but it's dead with no successor claiming that specific niche.

No project claims "efficiency/speed/reliability over capability" as its headline value proposition. Every actively maintained project in this survey (OpenCode/Anomaly, Cline, Goose, Crush) is trending toward *more* surfaces (desktop apps, IDE extensions, web dashboards, mobile), not fewer. That's a real, verifiable gap: the market is converging on "do everything, everywhere" and nobody big is building the opposite bet.

## 5. Licensing and fork viability

| Project | License | Fork-friendly? |
|---|---|---|
| anomalyco/opencode | MIT (verified from repo LICENSE file) | Yes, fully permissive, but TypeScript — forking it doesn't get you Go |
| Aider-AI/aider | Apache-2.0 | Yes, permissive, but Python |
| cline/cline | Apache-2.0 | Yes, permissive, but TypeScript |
| continuedev/continue | Apache-2.0 | Permissive but the project is discontinued — forking means adopting an orphaned codebase with no upstream to track |
| aaif-goose/goose | Apache-2.0 | Yes, permissive, Rust — closest language to Go in terms of "gets you close to the metal," but still a rewrite if the goal is specifically Go |
| charmbracelet/crush | **FSL-1.1-MIT** (source-available) | Conditionally. You can read, modify, and self-host it freely today. You cannot offer a competing commercial product/service built on it until two years after each release, at which point that release converts to plain MIT automatically. For a personal, non-commercial tool this restriction is close to irrelevant — the practical blocker is architectural, not legal: it's Charm's general-purpose agent, not a stripped-down single-user tool, so "fork and strip down" means removing a lot of surface area (LSP, MCP server modes, multi-platform desktop concerns) rather than starting from a blank slate that already matches the target shape. |
| opencode-ai/opencode (archived) | MIT | Freely forkable, Go, but a year stale and abandoned — you'd be reviving dead code, not extending a maintained base |
| anthropics/claude-code | **Not open source.** `license: null` in the GitHub API, and the README (checked 2026-08-13) states npm installation is deprecated in favor of pre-built binaries, with no build-from-source path in the repo. It functions as an issue tracker/plugin-docs repo, not a source distribution. Not forkable. |
| openai/codex | Apache-2.0, Rust, 105,564 stars, active | Permissive, but not evaluated in depth here beyond licensing (out of the core comparison set requested) |
| google-gemini/gemini-cli | Apache-2.0, TypeScript, 106,491 stars, active | Permissive, same caveat as above |

**Bottom line on forking**: of the projects that are (a) actively maintained, (b) genuinely permissive or acceptably source-available, and (c) written in a systems-ish language, **Crush is the only real fork candidate**, and even there the honest framing is "extend and add capabilities on top of," not "strip down into." Every Go option besides Crush is either dead (opencode-ai) or nonexistent. Every actively-maintained MIT/Apache project besides Crush is in the wrong language for a "Go + Bubble Tea" goal, which means forking one of them defeats the stated point of building in Go in the first place.

## 6. Gaps — what nobody has actually built

Checked against Crush, OpenCode/Anomaly, Aider, Cline, Goose, Codex CLI, Gemini CLI, plus a general search for adjacent projects.

**Hybrid local/cloud model routing, inside a single coherent coding-agent tool.** PARTIAL, not FOUND. Every tool surveyed supports *both* local and cloud providers (that's now table stakes), but none of them do active routing — deciding per-request whether to send a task to a local model or a cloud model based on complexity, cost, or privacy. The closest thing is [Plano](https://github.com/katanemo/archgw) (formerly "Arch Gateway," by katanemo, Apache-2.0, ~7.0k stars, active), an AI-native proxy/gateway with preference-based and semantic-alias routing across providers — but it's a general-purpose LLM gateway, not built for or by a coding-agent project, and I found no evidence any of the coding CLIs in this survey use it or anything like it internally. This gap is real.

**Mobile companion app with real push notifications over WAN.** NOT FOUND among the established projects (OpenCode, Aider, Cline, Continue, Goose, Crush all lack this). The one adjacent hit is [goClaw](https://rhelm.io), a "Show HN" post from 2026-02-24 (1 point, 0 comments) describing a solo indie project: "a companion mobile app (coming to App Store and Play Store) that lets you talk to your agents from your phone," with task routing across local/API/human based on complexity. It's unlaunched/unreleased as far as the HN post shows, has essentially zero traction, and is not connected to any of the major agent CLIs. Treat this as evidence the idea has occurred to someone, not evidence it's been solved. This gap is real and is the least contested of the four.

**Built-in benchmark-driven self-comparison against Claude Code specifically.** NOT FOUND. Aider has a strong benchmark culture (leaderboards, self-authorship tracking) but benchmarks *models*, not itself-vs-Claude-Code. None of the other tools ship anything like this. This gap is real, though also somewhat unusual as a feature to want built into a tool — it's more naturally an external evaluation harness than something the agent ships with. Worth deciding whether this belongs inside the tool at all versus as a separate benchmark repo that happens to test multiple CLIs including this one.

**Subagent orchestration built for a single narrow-use-case power user (not enterprise multi-agent platforms).** PARTIAL. Goose and Crush both support MCP-based extension/tool composition; Cline ships a "Kanban" board for coordinating multiple concurrent agent runs, which is the closest thing to solo-user multi-agent orchestration in this survey, but it's still framed around parallel task boards, not a lightweight subagent-delegation model scoped to one person's workflow. Claude Code's own subagent feature (Task tool, `.claude/agents/*.md`) is the actual reference implementation of what "good" looks like here, and it's closed source — nobody in the open-source set has cloned that specific UX. This gap is real.

## Build vs. fork vs. adopt

**Adopt Crush as-is: no.** It's general-purpose by design, and none of the four gaps this project cares about are on its roadmap as far as its README shows.

**Fork Crush and add the missing pieces: plausible, and worth taking seriously before building from zero.** It already has the language (Go), the TUI ecosystem (Bubble Tea/Charm), the best local-model integration of anything surveyed, MCP support, and an actively-maintained, today-pushed codebase under a license that's practically unrestrictive for a personal tool. The FSL-1.1-MIT competing-use clause only bites if this ever became a commercial product competing with Crush itself, which isn't the stated goal. Forking it means inheriting a wider surface area than a from-scratch narrow tool would want (LSP integration, multi-platform desktop packaging, general-provider config), so "fork" here really means "start from a working Go+Bubble Tea+local-model agent and cut it down, then add hybrid routing, a mobile companion, and a lightweight subagent model on top" — not a small diff.

**Build from scratch: defensible only for the parts of the brief that are genuinely novel.** Hybrid local/cloud routing and a mobile companion with real WAN push notifications are unsolved by anyone in this survey, including Crush. Those two features are worth building regardless of what the base agent loop looks like. The core "read files, edit files, run commands, talk to an LLM" loop is not novel — Crush, OpenCode/Anomaly, Goose, Cline, and Aider have all solved it, several of them well, in three different languages.

**Honest verdict**: building an entirely new agent loop and TUI from zero to get to where Crush already is today would be redundant effort. The differentiated, actually-uncontested value in this project's brief is the routing layer and the mobile/push layer, not the base coding-agent mechanics. The choice that isn't purely mine to make: whether to (a) fork Crush and bolt those two features plus a solo-scoped subagent model onto it, accepting its existing architecture and FSL license, or (b) build a smaller, purpose-built Go+Bubble Tea core from scratch specifically so it stays minimal enough that hybrid routing and mobile push are first-class rather than bolted on, accepting the cost of re-solving problems Crush has already solved. Recommend deciding this by prototyping the routing layer against Crush's actual codebase for a day before committing either way — that will surface whether Crush's architecture accommodates a routing layer cleanly or fights it.

## Sources

- [github.com/anomalyco/opencode](https://github.com/anomalyco/opencode) / [api](https://api.github.com/repos/anomalyco/opencode) — checked 2026-08-13
- [opencode.ai](https://opencode.ai) and [opencode.ai/docs](https://opencode.ai/docs) — checked 2026-08-13
- [github.com/opencode-ai/opencode](https://github.com/opencode-ai/opencode) / [api](https://api.github.com/repos/opencode-ai/opencode) — checked 2026-08-13
- [github.com/Aider-AI/aider](https://github.com/Aider-AI/aider) / [api](https://api.github.com/repos/Aider-AI/aider) — checked 2026-08-13
- [github.com/cline/cline](https://github.com/cline/cline) / [api](https://api.github.com/repos/cline/cline) — checked 2026-08-13
- [github.com/continuedev/continue](https://github.com/continuedev/continue), [README](https://raw.githubusercontent.com/continuedev/continue/main/README.md) / [api](https://api.github.com/repos/continuedev/continue) — checked 2026-08-13
- [github.com/aaif-goose/goose](https://github.com/aaif-goose/goose) (formerly block/goose) / [api](https://api.github.com/repos/block/goose) — checked 2026-08-13
- [github.com/charmbracelet/crush](https://github.com/charmbracelet/crush), [LICENSE.md](https://github.com/charmbracelet/crush/blob/main/LICENSE.md) / [api](https://api.github.com/repos/charmbracelet/crush) — checked 2026-08-13
- [github.com/anthropics/claude-code](https://github.com/anthropics/claude-code), [README](https://raw.githubusercontent.com/anthropics/claude-code/main/README.md) / [api](https://api.github.com/repos/anthropics/claude-code) — checked 2026-08-13
- [github.com/openai/codex](https://github.com/openai/codex) / [api](https://api.github.com/repos/openai/codex) — checked 2026-08-13
- [github.com/google-gemini/gemini-cli](https://github.com/google-gemini/gemini-cli) / [api](https://api.github.com/repos/google-gemini/gemini-cli) — checked 2026-08-13
- [github.com/katanemo/archgw (Plano)](https://github.com/katanemo/archgw) — checked 2026-08-13
- [models.dev](https://models.dev) — checked 2026-08-13
- [HN Algolia search API](https://hn.algolia.com/api/v1/search) — used for goClaw, Plano, and gap-analysis leads, checked 2026-08-13

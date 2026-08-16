# Building a Claude Code replacement: synthesis and recommendation

Date: 2026-08-12. Synthesized from seven research passes in this directory: [local-inference-apple-silicon.md](local-inference-apple-silicon.md), [agentic-harness-architecture.md](agentic-harness-architecture.md), [go-tui-ecosystem.md](go-tui-ecosystem.md), [remote-mobile-access.md](remote-mobile-access.md), [browser-simulator-automation.md](browser-simulator-automation.md), [coding-benchmarks.md](coding-benchmarks.md), [existing-alternatives-landscape.md](existing-alternatives-landscape.md).

## Is this possible in 2026?

Yes, and less effort than it looks, because the hard 80% already exists as working, MIT-adjacent source you can read and copy from. The three genuinely hard subsystems, a Go agentic tool-use loop, a Bubble Tea TUI that handles streaming tokens and diffs at scale, and local-model routing on Apple Silicon, are all solved problems with a production reference implementation sitting in one repo: Charm's own coding agent, **Crush** (`charmbracelet/crush`, 27.3k stars, Go, Bubble Tea, best-in-class local-model support, actively pushed the day this research ran). Nobody else surveyed comes as close to matching your stated stack.

What's genuinely unsolved anywhere in the open-source landscape, and therefore the actual differentiated work in this project, is narrower than "build a Claude Code replacement": hybrid local/cloud model routing built into the coding-agent loop itself, a mobile companion with real push notifications over the public internet, and a benchmark harness that compares an agent against Claude Code specifically. Those three pieces are real, scoped, buildable in weeks, not the multi-year undertaking a from-scratch agent-plus-TUI-plus-ecosystem would be.

The honest scope decision, then, is not "build vs. don't." It's **fork Crush and add three subsystems, or build a smaller purpose-built core from scratch that never carries Crush's general-purpose surface area.** Both are 2026-feasible for a solo developer. Recommendation: prototype the routing layer against Crush's actual code for a day before committing to either, per [existing-alternatives-landscape.md](existing-alternatives-landscape.md)'s closing section. If Crush's architecture accommodates it cleanly, fork. If it fights you, you'll know fast, and you'll have Crush's source open as a reference the whole time either way.

## Requirements

### Functional

1. Agentic tool-use loop: gather context, call tools (read/write/edit files, run shell, search), verify results, repeat. Streaming, not turn-based-then-parse.
2. Local-first inference on Apple Silicon, with fallback to a hosted frontier model (Claude, GPT, Gemini) when task complexity, local model failure, or context length demands it.
3. A permission gate in front of anything destructive, sandboxing for shell/file/network access, and hooks at the points that matter (pre-tool-use, session-start) to make catastrophic actions (the PocketOS/Replit-class incidents documented in [agentic-harness-architecture.md](agentic-harness-architecture.md)) structurally hard to reach, not just discouraged.
4. Lightweight subagent delegation for context hygiene (an Explore-equivalent), not enterprise-scale multi-agent orchestration.
5. Remote triggering and status/approval from a phone, over the public internet, not just LAN.
6. Vision-capable verification: launch and drive Chrome, optionally the iOS Simulator, capture screenshots or accessibility trees, judge the result.
7. A mechanism to iterate on the system prompt/guidance based on observed failures, not a one-time hand-authored prompt.
8. An offline-runnable benchmark harness that scores this agent against Claude Code on real coding tasks, tracking wall-clock time, token usage, cost, and pass rate.

### Non-functional

- Runs fully offline for the local-model path: no network call required for a basic edit-and-verify loop.
- Fast enough that local inference doesn't make the tool feel worse than Claude Code for routine work, or the whole "efficiency" premise fails.
- Reliable: bounded retries, no silent tool-call failures, no infinite loops (the open OpenCode bugs in [agentic-harness-architecture.md](agentic-harness-architecture.md) section 6 are the cautionary example).
- Narrow: built for one power user's workflows, not a general product. This is what licenses you to skip most of what makes Claude Code's surface area large (four-tier config hierarchy, enterprise policy, OpenTelemetry export, agent teams).

## Recommended technology stack

| Layer | Choice | Why |
|---|---|---|
| Core language | Go | Static binary, no runtime dependency, matches Crush's stack, matches your stated preference |
| TUI | Bubble Tea v2 + Bubbles + Lip Gloss | Actively maintained through August 2026, no credible alternative beats it for a Go team, and Crush is a working reference for every hard problem (streaming, diffing, virtualized scroll) you'll hit. Don't default to nested `tea.Model` composition. Copy Crush's flat-model, imperative-sub-component pattern from day one. |
| Local inference backend | Ollama's HTTP API first | Only backend written in Go with a Go client library, routes GGUF and safetensors models to the faster engine automatically, abstracts tool-calling template plumbing that trips up raw llama.cpp and raw MLX. Move to `mlx_lm.server` or a project like Rapid-MLX only once raw tok/s is the actual bottleneck. |
| Local coding model | A small-active-parameter MoE coder in the 30B-total class (Qwen3-Coder-30B-A3B or Qwen3-Coder-Next) at 4-bit | Fits 48-64GB unified memory, fast because these models are memory-bandwidth-bound not compute-bound, avoids flagship 480B+/1T models that aren't practically runnable locally despite topping leaderboards |
| Cloud fallback | Claude API (Sonnet/Opus), routed to when local model quality, context length, or reliability requirements exceed what local can deliver | The agentic-reliability gap between local and frontier models is real and durable (Epoch AI: 3-4 months, stable since 2023), concentrated in sustained multi-step task execution, exactly what a coding agent spends most of its time doing |
| Sandbox | macOS Seatbelt (`sandbox-exec`) directly, the same primitive Claude Code itself uses | Zero extra install on macOS, restricts filesystem writes to a scoped root and session temp, gates network via an allowlist proxy |
| Browser automation | go-rod (or chromedp, near-equivalent) | CDP-native, no Node.js dependency, go-rod self-manages its own Chrome binary. Skip Firefox: no mature Go WebDriver BiDi client exists yet, and the only path (playwright-go) trades away single-binary simplicity for a Node driver process. |
| iOS Simulator | `xcrun simctl` (screenshot, lifecycle) + `facebook/idb` (tap/type) | Zero dependencies, zero macOS permission prompts for simctl; idb is alive (last updated Aug 7, 2026) despite the Appium ecosystem drifting off it |
| Vision | Accessibility tree (CDP `Accessibility.getFullAXTree`) as the default read path, hosted Claude vision only for genuinely visual judgments | Near-zero token cost for "is X present/correct," reserves expensive vision calls for layout/color/alignment checks a local text model can't answer |
| Remote/mobile transport | Tailscale (+ Funnel only if reachability without both devices on the tailnet is required) | Free on the Personal plan, no bandwidth billing, gives you a mesh network as a side effect |
| Mobile client | PWA first (1-2 dev-days), fall back to native SwiftUI + TestFlight only if push-notification action buttons prove insufficient | Zero App Store friction, iOS Safari has supported Web Push for home-screen PWAs since 16.4 |
| Push notifications | ntfy.sh free tier | Single unauthenticated HTTP POST, no infrastructure to run, proportionate to "agent needs input / agent finished" being low-frequency and high-importance |
| Auth | QR-pairing flow minting a device-scoped bearer token in Keychain | Matches Claude Code's own Remote Control UX, skips building an OAuth device-flow server you don't need for one user |
| Benchmark harness | Reuse Terminal-Bench/Harbor for task/scoring (already has adapters for Claude Code, Codex, Cursor CLI, Gemini CLI, OpenCode, Aider), reuse Claude Code's own OTEL export or Inspect AI's `sandbox_agent_bridge()` for cost/token accounting | Nothing surveyed does both end to end for arbitrary agents; the only new code needed is a thin orchestration and transcript-parsing wrapper tying reused pieces into one comparable table |

## Architecture shape

Borrow the shape that's converged across every tool surveyed: a permission gate in front of anything destructive, a runtime sandbox behind that as blast-radius containment, and an explicit opt-in bypass scoped to disposable environments, never the local default. Layer a hybrid model router in front of the tool-use loop: cheap/fast/local for scoped single-file work, escalate to a hosted frontier model when the task looks like sustained multi-step reasoning, a long context requirement, or a local model has already failed once on this task. Anthropic's own numbers on multi-agent overhead (roughly 15x token cost of simple chat) argue for keeping subagent use to context-hygiene only, an Explore-equivalent that does throwaway search and returns a summary, not a "teams" model.

For prompt refinement, skip formal eval-harness A/B testing (that machinery pays for itself at Anthropic's iteration volume, not at one person's). Start from Anthropic's own documented practice instead: a minimal prompt tested against the strongest available model, instructions added only in response to observed failure modes, transcripts read manually rather than graded automatically.

## Phased plan and effort estimate

1. **Prototype the routing layer against Crush's actual code, 1 day.** Decides fork vs. build-from-scratch.
2. **Core loop plus permission gate plus sandbox, 1-2 weeks if forking Crush, 3-4 weeks from scratch.** This is the one piece every tool researched gets right in its own way; get the structured tool-call/result cycle right before anything else.
3. **Local inference wired through Ollama, model selection and memory-ceiling tuning, 3-5 days.** Includes defensive retry/loop-detection logic, since that's precisely where local models fail in practice (documented: repetition loops, malformed tool calls, a real MLX wired-memory kernel panic with zero warning).
4. **Hybrid routing to a hosted fallback, 3-5 days.** The genuinely novel piece nobody else has built.
5. **Remote/mobile basic version, 4-6 developer-days**, per [remote-mobile-access.md](remote-mobile-access.md): Tailscale, QR pairing, a PWA, ntfy pushes, Mac left awake rather than sleep/wake infrastructure.
6. **Browser/simulator vision tooling, 3-5 days**: go-rod plus accessibility-tree extraction, simctl plus idb, vision calls routed to Claude only when needed.
7. **Benchmark harness, 1-2 weeks**: a stratified 20-30 task subset run 3x per agent (per [coding-benchmarks.md](coding-benchmarks.md) section 6's reasoning), Terminal-Bench/Harbor for scoring, Claude Code's OTEL export or Inspect AI's bridge for cost/token accounting, a thin wrapper tying them together.

Total: roughly 6-10 weeks of solo focused work if forking Crush, 10-16 weeks from scratch, before counting the native mobile app upgrade (15-25 additional developer-days, per remote-mobile-access.md) or a full-scope benchmark run across hundreds of tasks. This assumes you're not also learning Go or Bubble Tea from zero.

## Real risks worth naming up front

- The local-vs-frontier agentic reliability gap won't close on your timeline. Design for local-model failure (bounded retries, explicit loop detection, clean parse-failure recovery) as a first-class case, not an edge case, and expect to fall back to a hosted model for anything requiring reliable multi-file edits or long autonomous runs.
- Bubble Tea v2 has an open, unresolved scroll-performance regression ([bubbletea#1724](https://github.com/charmbracelet/bubbletea/issues/1724)) worth tracking before leaning hard on continuous-scroll UX.
- Wish (Charm's SSH library) carries two 2026 CVEs, one critical. Don't reach for it as the remote-access answer; Crush's own `crush serve` HTTP/SSE pattern, or the Tailscale-plus-PWA stack above, sidesteps it entirely.
- No existing benchmark tool captures wall-clock, tokens, cost, and pass rate across different agent CLIs in one schema. You will build a thin wrapper regardless of which harness you reuse for scoring.
- Charm's own funding/runway status past its 2023 Series A is unconfirmed either way. Still shipping code isn't proof of multi-year survival. Worth a direct check if this project's timeline leans on Crush's continued existence.

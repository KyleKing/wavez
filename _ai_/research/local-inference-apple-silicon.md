# Local LLM inference on Apple Silicon for coding agents (August 2026)

Research pass for a Go-based, local-first coding agent host. All facts below are dated and
sourced. Many numeric claims in this space (tokens/sec, memory tiers) come from
SEO/content-farm sites without disclosed methodology; those are marked
`[low-confidence]` or `[unverified]` and should not drive a decision without an
independent test run. Model names in the "frontier" section (Claude Opus 5, Claude
Sonnet 5, "Claude Fable/Mythos 5," GPT-5.6, Gemini 3.1 Pro) surfaced across multiple
independent searches, but a few of the exact names and scores could not be pinned to a
primary source and are flagged as such. Note also that this document is itself the
output of Sonnet 5, so its knowledge of "current" Anthropic models should be read with
that in mind rather than trusted as an outside confirmation.

## 1. Inference backends: MLX vs llama.cpp vs Ollama vs LM Studio

### MLX (mlx-lm, mlx-swift, mlx-community)

mlx-lm's release cadence ran roughly every 1-4 weeks through April 2026 (v0.31.3, 2026-04-22)
but shows no further release as of 2026-08-12, a roughly four-month gap with no
primary-source explanation found. The core `mlx` package kept shipping through that
window (v0.32.0, 2026-07-07), so the framework isn't abandoned, but mlx-lm specifically
looks stalled and that should get re-checked before betting on it
([github.com/ml-explore/mlx-lm/releases](https://github.com/ml-explore/mlx-lm/releases),
verified via GitHub API 2026-08-12).

At WWDC 2026, Apple opened its Foundation Models framework to third-party backends and
shipped `MLXLanguageModel`, letting Swift apps load mlx-community Hugging Face models
through the native Foundation Models API
([developer.apple.com/videos/play/wwdc2026/328](https://developer.apple.com/videos/play/wwdc2026/328/)).
Apple's ML research team also published on MLX plus the M5 GPU's Neural Accelerators
([machinelearning.apple.com/research/exploring-llms-mlx-m5](https://machinelearning.apple.com/research/exploring-llms-mlx-m5)),
and presented "Run local agentic AI on the Mac using MLX" at WWDC26
(session 232, title/listing only, not verified in depth). Together these mark MLX as
Apple's own recommended local-agentic path in 2026, not just a community project.

mlx-lm ships a Python API (`load`, `generate`, `stream_generate`) plus an OpenAI-compatible
server (`mlx_lm.server`). It doesn't ship a native grammar-constrained decoding engine
comparable to llama.cpp's GBNF (see section 6). Third-party OpenAI/Anthropic-compatible
servers built on top of MLX exist and go further than the stock server: `mlx-serve`
(no-Python, Anthropic-compatible, with tool calling),
[`vllm-mlx`](https://github.com/waybarrios/vllm-mlx) (continuous batching, MCP tool
calling), and `mlx-openai-server`. These are community projects; treat maturity claims
in their READMEs as unverified until tested.

Performance: MLX is consistently reported faster than llama.cpp/Metal across every
source found, roughly +20% to +90% depending on model and workload, with prefill seeing
the largest gains. The one primary-attributable number is Ollama's own benchmark: moving
Qwen3.5-35B-A3B from its llama.cpp/Metal path (Ollama 0.18) to its MLX path (Ollama 0.19)
took decode from 58 to 112 tok/s (+93%) and prefill from 1,154 to 1,810 tok/s (+57%)
([ollama.com/blog/mlx](https://ollama.com/blog/mlx), 2026-03-30). That same release also
shipped a KV-cache rewrite, so the gain isn't purely attributable to the backend swap.
Every other MLX-vs-llama.cpp number in circulation (the 21-87% range on an M4 Max, the
2-2.5x claim for LM Studio's MLX path) comes from content-farm sites with no disclosed
methodology.

No source found in this research ran all four backends (MLX, llama.cpp, Ollama, LM
Studio) head-to-head on identical hardware and an identical model. Every public
comparison is partial.

### llama.cpp

Still under near-continuous development: multiple tagged builds land per day (5 builds
between 2026-08-11 17:12 and 2026-08-12 12:18 alone, via GitHub API). `llama-server`
exposes `/completion`, `/v1/chat/completions`, `/v1/completions`, `/v1/embeddings`,
`/slots`, `/metrics` (Prometheus), and, notably for a host that wants to speak either wire
format, an Anthropic Messages API-compatible endpoint at `/v1/messages`
([server README](https://github.com/ggml-org/llama.cpp/blob/master/tools/server/README.md)).
Streaming is SSE on both completion and chat endpoints.

Structured output uses `--grammar` (GBNF) with JSON-schema-to-grammar conversion built
in, covering a subset of the JSON Schema spec. OpenAI-style function calling needs the
`--jinja` flag and, per the docs, often a custom chat template per model to work
reliably, so it isn't zero-config across arbitrary GGUF files. The built-in MCP server
and multimodal support are explicitly marked experimental with a "do not enable in
untrusted environments" warning in the docs.

The CLI/flag surface is not frozen. New flags land at the same clip as the build tags,
and breaking changes to server flags have shipped before. A Go host should pin an exact
build tag rather than track `master`, and re-test on every bump.

### Ollama

Releasing near-daily as of August 2026 (v0.32.10-rc1, 2026-08-12). Ollama 0.19
(2026-03-27/30) added MLX as an additional backend for Apple Silicon, not a full
replacement: GGUF-format models still route through llama.cpp, safetensors-format models
route through MLX, gated behind ">32GB unified memory." Secondary coverage claims a later
0.30.x release generalized this file-format-based routing further, but that specific
detail wasn't confirmed against Ollama's own release notes.

Ollama's server is written in Go and drives a llama.cpp-based (or, for safetensors
models, MLX-based) runner subprocess over HTTP via CGO bindings in
`github.com/ollama/ollama/llama`
([pkg.go.dev](https://pkg.go.dev/github.com/ollama/ollama/llama),
[DeepWiki: llama.cpp Integration](https://deepwiki.com/ollama/ollama/5.2-llama.cpp-integration)).
It ships its own Go client library (`github.com/ollama/ollama/api`), the most direct fit
for a Go host that wants to bind against a local backend rather than parse subprocess
stdout. Wrapper overhead versus raw `llama-server` on the same GGUF file is small when
Ollama is actually using its llama.cpp path (2-7% in one `[low-confidence]` June 2026
test on an M2 Max).

### LM Studio

LM Studio 0.4.0 (dated to 2026-01-28 by search results) shipped `llmster`, a headless
daemon that runs LM Studio's inference engine without any GUI, intended for servers, CI,
and cloud instances, plus `lms`, a CLI shipped alongside either the desktop app or
`llmster`
([lmstudio.ai/blog/0.4.0](https://lmstudio.ai/blog/0.4.0),
[docs comparison page](https://lmstudio.ai/docs/app/basics/lmstudio-vs-llmster-vs-lms)).
Both `lms` and `llmster` are closed-source binaries, which matters if a Go host wants to
vendor or audit what it's shelling out to. The server listens on `localhost:1234` with
OpenAI- and Anthropic-compatible endpoints and local MCP support.

LM Studio supports an MLX backend on Apple Silicon alongside llama.cpp/GGUF, and enforces
JSON Schema on MLX output using Outlines-style token matching
([LM Studio structured output docs](https://lmstudio.ai/docs/developer/openai-compat/structured-output)).
The specific "2-2.5x faster than llama.cpp" claim for its MLX path comes from secondary
sources only; LM Studio's own docs page didn't mention MLX where it was fetched directly.

### Which backend fits a Go host that shells out or binds

HTTP is the right integration point for all four. `llama-server`, `mlx_lm.server`,
Ollama, and LM Studio's `llmster`/GUI server all expose HTTP with OpenAI-compatible (and,
for llama.cpp and LM Studio, Anthropic-compatible) JSON and SSE streaming. Parsing raw
CLI stdout would mean giving up structured streaming and the grammar/JSON-schema
constraints that only the HTTP servers expose.

Ollama is the most natural fit for a Go host specifically: it's written in Go, ships a Go
client library, and its wire protocol is plain HTTP/JSON. It also abstracts the
underlying backend's tool-calling mechanics behind its own OpenAI-shaped layer, so a
coding agent that wants `tools=[...]` to just work has less backend-specific plumbing to
write, at the cost of being one layer further from the raw engine.

If raw tokens/sec on Apple Silicon is the priority, an MLX-backed path (`mlx_lm.server`
directly, or Ollama/LM Studio pointed at a safetensors model) beats plain llama.cpp/GGUF
per every source found, though the largest specific multipliers rest on low-confidence
sources. llama.cpp has the more mature, more explicitly documented structured-output
story (see section 6). No source measured cold-start latency across all four backends on
the same hardware; that's a real gap if startup time matters for the agent's UX.

## 2. Best open-weight coding models runnable locally

The open-weight coding landscape shifted heavily toward Chinese labs through 2025-2026.
Meta's Llama 4 (Scout/Maverick, 2025-04-05) is the last Llama release found; no new
open-weight Llama model shipped through at least 2026-05-16, and Meta retired its hosted
Llama API on 2026-07-06. By May 2026 secondary analysis had DeepSeek V4 ahead of Llama 4
on most reasoning/coding benchmarks. Llama is not a current-generation leading choice as
of August 2026, though a Llama 4 Maverick Q4 quant is still cited as a stable 70B-class
workhorse for a 96GB Mac `[unverified]`.

**Qwen (Alibaba).** Qwen3-Coder-480B-A35B-Instruct (2025-07-22) is the established
flagship: 480B total / 35B active MoE, natively 256K context extendable to 1M via YaRN.
GGUF (Unsloth dynamic quants) and MLX 4-bit/8-bit builds both exist on Hugging Face; 4-bit
GGUF reportedly needs 80-120GB unified memory `[unverified]`. Qwen3-Coder-Next, reported
in February 2026 `[unverified primary date]`, is smaller and explicitly aimed at
local/agentic use: 80B total, ~3B active, 256K context. Unsloth's own docs (a credible
source for its own quants) put 4-bit at >45GB unified memory, with a 2-bit XL quant
usable above 30GB, and currently recommend llama.cpp over MLX for this specific model
because of an MLX KV-cache consistency bug during conversation branching
([unsloth.ai/docs/models/qwen3-coder-next](https://unsloth.ai/docs/models/qwen3-coder-next)).
Reported speeds (all `[unverified]`, from SEO aggregators): Qwen3-Coder-30B-A3B at 4-bit,
~130 tok/s on a 64GB M4 Pro, ~230 tok/s on an M5 Max; Qwen3-Coder-Next Q4_K_M around
22-28 tok/s on M3 Max/M4 Max, with little difference between chip generations because
these MoE models are memory-bandwidth bound rather than compute bound.

**DeepSeek.** No DeepSeek-Coder-V3-branded release was found; DeepSeek-Coder-V2 (2024) is
the last dedicated "Coder" model. DeepSeek-R1 (Jan 2025) and its distills (Qwen and Llama
based, 1.5B-70B) remain in active local use. DeepSeek shipped a V4 family on 2026-04-24
under MIT license: V4-Pro (1.6T total, 49B active, 1M context) and V4-Flash (284B total,
13B active, same context class), with a production V4-Flash-0731 build on 2026-07-31.
DeepSeek R1-671B on a Mac Studio M3 Ultra 512GB runs at roughly 17-18 tok/s and needs
about 404GB of storage, filling most of the machine's unified memory
([TechRadar](https://www.techradar.com/pro/apple-mac-studio-m3-ultra-workstation-can-run-deepseek-r1-671b-ai-model-entirely-in-memory-using-less-than-200w-reviewer-finds),
[MacRumors, 2025-03-17](https://www.macrumors.com/2025/03/17/apples-m3-ultra-runs-deepseek-r1-efficiently/)).
"DeepSeek R2" has not shipped as of this research; everything about it in circulation is
rumor.

**GLM-4.x/5.x (Zhipu AI / Z.ai).** GLM-4.6 (2025-09-30, 355B total / 32B active) and
GLM-4.7 (2025-12-22 per a Chinese tech-news aggregator, moderate confidence) both claim
top-tier open-weight coding results. No primary Zhipu/Z.ai source was directly fetched
for either release; treat both dates and claims as secondary-sourced. GLM-4.7-Flash, a
much smaller ~10-12B variant, is reported to run on a normal Mac at 60-85 tok/s on an M4
with 48GB `[unverified]`, while the full GLM-4.6 needs roughly 205GB+ unified memory for
5+ tok/s. GLM-5.x is referenced as newer still, with guidance that a 256GB Mac at 2-bit
quant is the realistic tier for GLM-5 `[unverified]`.

**Other notable entrants.** Moonshot's Kimi K2 (Aug 2025, 1T total/32B active MoE)
iterated through K2.5 "Thinking" (2026-01-27, multimodal), K2.6 (2026-04-20), and a
coding-focused K2.7 refresh (~June 2026). K2.6 is reported to tie GPT-5.5 on SWE-Bench
Pro at roughly 80% lower cost per token `[unverified exact figure]`. On a single 512GB
Mac Studio, Kimi K2.5 in native INT4 is reported at 5-15 tok/s depending on quant and
context. MiniMax M2 (230B total/10B active) targets coding/agentic workflows directly,
and Cerebras's REAP-pruned variants (e.g. `cerebras/MiniMax-M2-REAP-162B-A10B`) cut about
30% of experts while preserving near-identical accuracy on code generation and function
calling, making the model meaningfully lighter for local hardware
([Hugging Face model card](https://huggingface.co/cerebras/MiniMax-M2-REAP-162B-A10B)).
Codestral Mamba and StarCoder2 both predate this window (2024) and show no 2025-2026
successor; that line looks dormant.

**Important caveat on the flagship numbers above:** the largest, highest-scoring
open-weight models (DeepSeek V4 Pro-Max, Kimi K2.6, GLM-5.2, full Qwen3.6 Plus) are not
practically runnable at full capability on consumer Apple Silicon even quantized. Their
leaderboard scores are earned on cloud-hosted, full-precision deployments. The actual
local-on-a-Mac experience uses smaller/distilled/quantized siblings (Qwen3-Coder-30B-A3B
class, GLM-4.7-Flash class), whose benchmark scores were not separately found in this
research and are likely meaningfully below the flagship numbers. This means the
local-on-Apple-Silicon quality gap versus frontier hosted models is probably larger than
the open-weight-vs-closed numbers in section 3 suggest.

## 3. Quality gap vs frontier hosted models on agentic coding

Frontier versions current as of August 2026, per each vendor's own newsroom: Claude
Opus 5 (2026-07-24) and Claude Sonnet 5 (2026-06-30) from Anthropic
([anthropic.com/news](https://www.anthropic.com/news)); GPT-5.6 (flagship "Sol," plus
"Terra" and "Luna" tiers) from OpenAI
([openai.com/index/gpt-5-6](https://openai.com/index/gpt-5-6/)); Gemini 3.1 Pro from
Google, with Gemini 3.6 Flash launched 2026-07-21. Names like "Claude Fable 5" and "Claude
Mythos 5" recur across multiple leaderboard mirrors as tiers above or alongside Opus 5,
but no `anthropic.com/news` page titled "Introducing Claude Fable/Mythos 5" was found.
Treat those two names as unverified rather than confirmed public products.

**SWE-bench Verified** (via a leaderboard aggregator, [leaderboard.steel.dev](https://leaderboard.steel.dev/leaderboards/swe-bench-verified/),
not swebench.com directly, so treat as secondary but structured):

| Tier | Model | Score |
|---|---|---|
| Frontier | Claude Mythos 5 | 95.5% |
| Frontier | Claude Fable 5 | 95.0% |
| Frontier | Claude Opus 4.8 | 88.6% |
| Frontier | GPT-5.6 Sol | 82.2% |
| Open-weight | DeepSeek-V4-Pro-Max | 80.6% |
| Open-weight | Kimi K2.6 | 80.2% |
| Open-weight | MiniMax M2.5 | 80.2% |
| Open-weight | GLM-5.2 | 80.0% |
| Open-weight | Qwen3.6 Plus | 78.8% |

The gap between the very top of the leaderboard and the best open-weight model is about
15 points. Against a mid-tier frontier model (GPT-5.6 Sol) the best open-weight models are
within roughly 2 points, much closer to a "good" frontier model than to the single best
one. On SWE-bench Pro, a harder successor benchmark gaining traction as Verified
saturates, GLM-5.2 is reported at 62.1% versus GPT-5.5's 58.6%, meaning at least one
open-weight model leads at least one frontier model on at least one harder benchmark
`[secondary, unverified]`.

**LiveCodeBench** (via [vals.ai](https://www.vals.ai/benchmarks/lcb) and
[benchlm.ai](https://benchlm.ai/coding) summaries): Gemini 3 Pro Preview leads at 91.7,
Claude Fable 5 at 89.78, DeepSeek V4 and Kimi K3 finish roughly 2.3-2.6 points behind the
best closed model. This is the narrowest gap found across any benchmark, likely because
LiveCodeBench measures single-shot code generation rather than a full agentic harness.

**Aider polyglot leaderboard**: the official [aider.chat leaderboard](https://aider.chat/docs/leaderboards/)
as fetched directly still topped out at GPT-5 (Aug 2025, 88.0%) and DeepSeek-V3.2-Exp
(Oct 2025, 74.2%), with no visible 2026-generation entries (no GPT-5.6, Opus 5, Kimi K2.6,
or GLM-5.2 rows). This looks like a real gap in current Aider-specific data rather than
evidence that DeepSeek-V3.2 is still the open-weight ceiling; other benchmarks above show
clearly newer, more capable open models exist.

**Agentic-specific reliability.** Epoch AI's Capabilities Index, the most rigorous primary
source found, puts the open-weight lag at roughly 3-4 months behind frontier closed
models as of early-to-mid 2026, a lag that's been stable to slightly widening since 2023
([epoch.ai/data-insights/open-closed-eci-gap](https://epoch.ai/data-insights/open-closed-eci-gap)).
Example scores: Kimi K2.6 at 151.60, GLM-5 at 146.62, Qwen 3.5 Plus at 146.11, versus
GPT-5.5 Pro at 159.35 and Claude Opus 4.7 at 156.18. Epoch flags its own caveat: public
benchmarks may flatter open models because open labs can optimize against public test
sets more directly. OpenRouter's June 2026 analysis reaches a similar "stable 3-6 month
gap for 18+ months" read
([openrouter.ai blog](https://openrouter.ai/blog/insights/the-open-weight-models-that-matter-june-2026/)),
and states plainly that DeepSeek V4 Flash was "the first open-weight model that teams
immediately dropped into real agentic pipelines as a plausible substitute" for a frontier
model.

The gap concentrates specifically in sustained multi-step reliability rather than raw
coding knowledge. A commonly repeated framing (via secondary sources, not independently
verified): a model that's 90% reliable on a single step is only about 59% reliable across
five sequential steps, so small per-step gaps between open and closed models compound in
agentic settings far more than they show up on single-shot benchmarks like
LiveCodeBench. No source directly compared a specific named open-weight model against a
specific named frontier model on multi-file edits or long-context retrieval with raw
numbers; that comparison is a genuine gap in what's publicly available as of this
research pass.

**Trend.** No single source gives a clean "the gap is X% and shrinking/growing at rate Y"
number. The most defensible read, from Epoch AI, is that the open-weight lag has held
roughly steady at 3-4 months through 2025 into 2026, with the gap narrowest on raw code
generation and widest on sustained agentic task execution with error recovery.

## 4. Memory and hardware requirements

Reported memory tiers (mostly `[unverified]`, from SEO aggregators, directionally
consistent across sources but without disclosed benchmark methodology):

- 16GB: only small (<12B) models at 4-5 bit, short context.
- 48-64GB (M2/M3/M4 Max, or top-spec Pro): GLM-4.7-Flash (10-12B) comfortably;
  Qwen3-Coder-30B-A3B at 4-bit; Qwen3-Coder-Next at 4-bit needs >45GB per Unsloth's own
  docs (moderate confidence, since it's Unsloth describing its own quant).
- 96-128GB (top M3/M4 Max, or entry Ultra): Llama 4 Maverick Q4 as a 70B-class workhorse;
  Qwen3-Coder-480B GGUF 4-bit reportedly needs 80-120GB.
- 192-256GB (M2/M3 Ultra): entry point for full GLM-4.6 (355B/32B active) at reduced
  quant.
- 512GB (Mac Studio M3 Ultra, top config): needed for DeepSeek R1-671B (~17-18 tok/s,
  moderate confidence) or Kimi K2.5 (5-15 tok/s depending on quant and context).

One concrete production incident illustrates the real risk here. A developer's
`mlx_lm.server` wired roughly 75% of system RAM (48GB of 64GB) at startup via
`mx.set_wired_limit()`. Combined with an unbounded KV cache growing during an agentic
tool-use session on a hybrid-attention model (Gemma-4-26B), this triggered a macOS kernel
panic (`"completeMemory() prepare count underflow" @ IOGPUMemory.cpp:550`). Because the
memory was wired, the OS's Jetsam OOM killer couldn't reclaim it, and critically, the
system showed no memory-pressure warning at all before the crash, since wired memory
bypasses the normal pressure-detection path. The fix was to cap Metal memory explicitly
with `mx.metal.set_memory_limit()` and to prefer standard grouped-query-attention MoE
models (predictable KV-cache growth) over hybrid-attention models with full-context
global layers
([Medium, Michael Hannecke, 2026-04-10](https://medium.com/@michael.hannecke/how-my-local-coding-agent-crashed-my-mac-and-what-i-learned-about-mlx-memory-management-e0cbad01553c)).
Anyone building a long-running local agent on MLX needs an explicit memory ceiling, not
just "enough RAM."

Because MoE speed is governed by active-parameter count rather than total parameter
count on unified memory, chip generation matters less than expected: multiple sources
report only modest differences between M3 Max and M4 Max at the same active-parameter
budget, since the bottleneck is memory bandwidth, not compute.

## 5. Context length: local reality vs frontier APIs

Frontier hosted models in 2026 are converging on 1M-token context as the new standard
(with Llama 4 Scout's 10M an outlier). But advertised versus effective context diverges
sharply everywhere, hosted and local alike: long-context retrieval benchmarks (RULER,
MRCR v2, NoLiMa) show substantial degradation on multi-fact retrieval well before the
advertised limit, and one commonly repeated finding puts the break point around 30-40%
before the claimed limit (a 200K-context model becoming unreliable around 130K tokens,
often as a sudden drop rather than a gradual one) `[unverified exact figures, but
consistent with the well-known "lost in the middle" effect]`.

Locally, usable context is memory-bound on top of that. LM Studio on a 512GB Mac Studio
M3 Ultra can set context up to 163,840 tokens for large local MoE models, well short of
the 1M-token native claims of DeepSeek V4 or Qwen3-Coder's YaRN-extended window, because
the KV cache at long context competes with model weights for the same unified memory
pool. Most people running coding models locally on 64-128GB Macs land at an effective
working context of roughly 8K-32K tokens regardless of a model's claimed max, reserving
the rest of memory for weights. Only 192GB+ Macs can push toward 64K-160K context on
large MoE models, still far short of hosted 1M windows. This is a synthesized inference
from the memory mechanics described above, not a single directly sourced number.

Speed also degrades with context length on Apple Silicon specifically, not just
theoretically. An open llama.cpp GitHub issue documents "Token Generation Speed Decline
with GGUF Models on M3 Ultra"
([github.com/ggml-org/llama.cpp/issues/13373](https://github.com/ggml-org/llama.cpp/issues/13373)).
One concrete data point: MiniMax-M2 at 6.5-bit quant on an M3 Ultra ran about 42 tok/s at
short context, dropping to about 12 tok/s at 6,800 tokens `[unverified single data
point]`, a roughly 3.5x slowdown from short to medium-length context on the same
hardware, model, and quantization.

## 6. Structured output and tool-calling quality

**llama.cpp.** GBNF grammar plus automatic JSON-schema-to-GBNF conversion is mature.
Per-token overhead is now single-digit percent for typical schemas; the older 5-15%
figure is described as outdated by mid-2026 commentary
([TianPan.co, 2026-04-16](https://tianpan.co/blog/2026-04-16-grammar-constrained-generation-output-reliability)).
One team reported going from 32% to 0.4% post-processing parse errors after adopting
constrained decoding. There's a real accuracy tradeoff, though: format constraints
degraded reasoning accuracy by up to 27 points on a math benchmark in one cited study, and
BAML found constrained JSON parsing at 91.37% accuracy versus 93.63% for free-form
parsing. Complex or pathological grammars remain a weak spot, with inference-time
blowups of 2-10x reported for the class of grammar-compilation engines llama.cpp's GBNF
belongs to `[not confirmed 1:1 against llama.cpp's own engine]`. Open stability bugs as
of mid-2026 include null-pointer dereferences in the jinja template parser and sampler
assertion failures on long contexts, framed in one write-up as a potential DoS vector for
`llama-server`. Tool-call parsing got a hardening pass: on parse failure it now surfaces
a clean error and preserves the raw unparsed string instead of aborting, letting a caller
catch the failure and re-prompt. Sub-4-bit quantization is a real, separate risk factor
for malformed tool calls, independent of the grammar engine itself.

**MLX/mlx-lm.** Markedly less mature. mlx-lm has no native, first-party grammar-constrained
decoding engine comparable to GBNF. Structured output on MLX models typically comes from
the third-party Outlines library bolted on top, or from separate reimplementations in
downstream projects: LM Studio enforces JSON Schema on MLX output using Outlines-style
token matching; macMLX (a standalone Swift/Metal app) implements its own constrained
decoding independent of mlx-lm; Rapid-MLX ships 17 different tool-call parsers with
auto-detection and manual fallback, which itself signals that tool-call parsing
reliability is still enough of a live problem in the MLX ecosystem to need that many
parsers. As of February 2026, mlx-lm's own `batch_generate` still didn't support
structured output at all (open bug, no maintainer response), and the mlx-swift side is
further behind still, with structured output only requested, not built. Whether
`mlx_lm.server` itself enforces schema constraints or just relies on the underlying
model's own tool-call formatting wasn't confirmed in a primary source.

**Ollama.** Native tool-calling has matured steadily since v0.3.0 (July 2024). Reliability
depends entirely on whether the specific model's chat template declares native tool
support; Ollama surfaces this as a "Tools" capability badge per model on its library site.
Non-tool-trained or older/smaller models still show classic failure modes locally: a
HuggingFace forum thread reports a local Llama-3.1-8B model via Ollama getting stuck
calling a tool repeatedly without ever producing a final answer, while the same agent
code against Claude's API via LiteLLM didn't reproduce the problem, isolating the failure
to the local model/serving path rather than the agent framework.

## 7. Known limitations and gotchas from real use

A controlled test is worth naming directly: a developer built a Rust coding agent ("Whet")
and ran the same coding task against 7 Ollama-served models. Four succeeded
(devstral-small-2 24B, glm-4.7-flash 19B, qwen3:8b, qwen3:14b); three failed, each
differently. qwen2.5:7b refused to act and kept asking for permission despite an explicit
"ACT, DON'T ASK" system instruction. qwen2.5-coder:14b emitted its tool call as plain
markdown text instead of invoking the actual tool-call API, producing zero functional
tool calls. A qwen3:14b run repeated the same failing shell command across 10 iterations,
burning about 30,000 tokens before hitting the iteration limit, because the system prompt
told it to keep retrying. The author's stated takeaway: model generation matters more
than size, since the qwen3 family beat larger qwen2.5-family models regardless of
parameter count
([DEV Community, kuroko1t](https://dev.to/kuroko1t/what-happens-when-local-llms-fail-at-tool-calling-testing-7-models-with-a-rust-coding-agent-cep)).

Other recurring failure modes reported across 2025-2026 sources:

- Repetition loops in agentic use. Models assign increasing probability to recently
  generated tokens, creating self-reinforcing loops; smaller distilled models loop more
  than their full-size teachers. Greedy decoding can't escape these cycles on its own;
  mitigations include beam search, presence-penalty tuning, and DPO fine-tuning
  targeting repetition specifically
  ([arXiv 2512.04419](https://arxiv.org/html/2512.04419v1)).
- Quantization degrading agentic precision specifically, not just general quality.
  Low-bit quantization (INT8/W8A8 cited) disproportionately harms tasks needing strict
  syntactic precision, cascading into expensive self-correction loops; one cited study
  reports a 70% increase in total time-to-solution from this effect
  `[unverified against the primary paper text, only seen via a search summary of
  arXiv 2512.18337]`.
- Context rot. As an agent's context fills with messages, tool calls, file reads, and
  responses, signal-to-noise drops and the model stops following simple instructions even
  though it's technically still "in context." This is a general framing repeated across
  sources, not tied to specific local-model data.
- macOS memory management. Covered in section 4: MLX's wired-memory allocation combined
  with unbounded KV-cache growth can trigger a kernel panic with no prior
  memory-pressure warning, because wired memory bypasses the normal pressure-detection
  path macOS otherwise uses to warn or intervene before a crash.

## 8. Existing open-source local-first coding agent projects

**Pi** (`@earendil-works/pi-coding-agent`), a Node-based coding agent harness, has a
published build log wiring it to a local `mlx_lm.server` running
Qwen3-Coder-30B-A3B-Instruct (30.5B total/3.3B active, ~17.2GB at 4-bit). Reports
resident memory of 28-30GB on a 64GB M-series Mac and 60-130 tok/s depending on chip (M4
Max around 130 tok/s). Malformed JSON tool calls are named as the single most common
failure mode. The project ships a path-protection extension that intercepts tool calls
and gates access to `~/.ssh`, `~/.aws`, `.env`, and `id_rsa` patterns behind a
confirmation prompt, explicitly acknowledged in the writeup as not real sandboxing
([Medium, Michael Hannecke, 2026-05-14](https://medium.com/@michael.hannecke/building-a-sovereign-coding-agent-on-apple-silicon-with-pi-and-mlx-swift-ef9bb23cafe2)).

**macMLX** (`github.com/magicnight/mac-mlx`, released around April 2026) is a native
Swift/SwiftUI app, CLI, and always-on OpenAI-compatible API server built with its own
`InferenceEngine` protocol over MLX, independent of mlx-lm's own server. It implements
its own tool-calling and JSON-schema structured output, claims 2.5-3.2x throughput under
concurrent load via continuous batching, and integrates with Cursor, Continue, Cline, and
Zed. Limited to roughly 70B params; SSD-tiered KV cache is still on its roadmap.

**Rapid-MLX** (`github.com/raullenchai/Rapid-MLX`) pitches itself as a drop-in
OpenAI/Anthropic-API replacement over MLX, claiming 4.2x faster than Ollama and 0.08s
cached time-to-first-token via prompt caching. It ships 17 tool-call parsers with
auto-detection and manual fallback, an explicit response to tool-call parsing being a
live problem across the MLX ecosystem. Verified end-to-end against Claude Code, Cursor,
and Aider as of 2026-07-28, though Cursor specifically can't reach its localhost endpoint
due to Cursor's own server-routing architecture.

**OpenClaw**, a large agent runtime (140K stars) originally built for messaging apps, has
been wired to a local `mlx-lm` server; because that server implements the OpenAI Chat
Completions API including function-call formatting, OpenClaw's existing tool-calling loop
runs against it unmodified. Reported performance: an M3 Max 64GB gets 25-40 tok/s on 14B
models with 4-8s response time at roughly 2K-token contexts; an M3 Ultra 192GB gets
18-25 tok/s on 70B models. Qwen2.5 14B/32B is called out as the most reliable size for
tool-calling specifically; 7B models showed degraded performance on multi-step workflows.
Practical context ceiling noted around 32K tokens
([contracollective.com](https://contracollective.com/blog/mlx-openclaw-apple-silicon-local-agent-runtime-2026)).

Among general-purpose coding agents: Aider supports pointing at a local `mlx_lm.server`
via an `openai/`-prefixed model config, though a long-standing GitHub issue asks for
better documentation of that path. Cline has a published blog post on running Qwen3
Coder 30B locally and an active GitHub discussion about using Rapid-MLX as a 2-4x-faster
local backend. Zed's local-model story runs through Ollama and generic OpenAI-compatible
endpoints, not a dedicated MLX code path. OpenHands (formerly OpenDevin) is model-agnostic
via Ollama and recently added an "MLX-powered performance layer" for Apple Silicon,
recommending Qwen3.6-35B-A3B as a starting point and reporting 25-40 tok/s on 32B models
on a 192GB Mac Studio M3 Ultra.

**Go-specific tooling.** No Go bindings to MLX itself were found; MLX is Python/Swift/C++
only, so a Go host reaches MLX inference over HTTP against an OpenAI-compatible server
(`mlx_lm.server`, macMLX, Rapid-MLX), not via direct language bindings. For llama.cpp,
several Go binding projects exist: Ollama's own CGO bridge
(`github.com/ollama/ollama/llama`), `go-skynet/go-llama.cpp` and its
`tcpipuk/go-llama.cpp` fork, `dianlight/gollama.cpp` (built on `purego` specifically to
avoid CGO), and smaller forks (`Qitmeer/llama.go`, `matthewrennie/go-llama.cpp`). Ollama's
own architecture, a Go server driving a llama.cpp/MLX subprocess over HTTP, is the
closest thing to a reference implementation for "Go host talks to local LLM inference" on
Apple Silicon.

## Bottom line for a solo dev building a local-first coding agent

Point the host at Ollama's HTTP API first. It's the only backend written in Go with a
Go client library and a stable-shaped wire protocol, it already routes GGUF and
safetensors models to the faster backend automatically, and its tool-calling layer
abstracts away the grammar/template plumbing that trips up raw llama.cpp and raw
mlx-lm alike. Treat direct `mlx_lm.server` or a project like Rapid-MLX as a later
optimization once raw tok/s on Apple Silicon actually becomes the bottleneck, not the
starting point, since MLX's structured-output and tool-calling story is still visibly
less mature than llama.cpp's.

On models: start with a small-active-parameter MoE coder in the 30B-total class
(Qwen3-Coder-30B-A3B or Qwen3-Coder-Next) at 4-bit. It fits in 48-64GB, runs fast because
speed here tracks active parameters and memory bandwidth rather than total size, and
avoids the flagship 480B+/1T-class models that aren't practically runnable on consumer
Apple Silicon at all despite topping the leaderboards. Budget for roughly 8K-32K tokens
of real working context on that hardware tier regardless of what the model claims, and
cap MLX's memory explicitly if going that route. A wired-memory kernel panic with zero
warning is a documented real failure mode, not a hypothetical.

On expectations: a well-run 4-bit MoE coder in this class will not close the gap to
Claude Opus/Sonnet 5, GPT-5.6, or Gemini 3.1 Pro on genuinely agentic work. The gap is
narrowest on raw single-shot code generation (LiveCodeBench, within a few points) and
widest on sustained multi-step task execution with error recovery, which is exactly what
a coding agent spends most of its time doing. Treat a local model as good enough for
scoped, single-file tasks, fast iteration loops, and offline/no-cost use, and expect to
fail back to a hosted frontier model for anything requiring reliable multi-file
edits or long autonomous runs. Design the agent's tool-calling and retry logic
defensively from day one (bounded retries, explicit loop detection, clean parse-failure
recovery) since that's precisely where local models fail in practice, not on knowing
the language or the API.

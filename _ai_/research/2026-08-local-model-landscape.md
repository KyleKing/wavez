# Local and hosted model landscape for the M4 Pro, 2026-08-18

Research pass answering the question raised while planning the M4 Pro probe
(DESIGN.md Next, item 6): is there a 2026 model that runs on a 24 GB Mac and
approaches Claude Sonnet on agentic coding, and is a local orchestrator plus a
hosted coder a better split than one hosted model. Produced by an agent session
with a benchmark sub-agent and light human review; every number carries a URL
and is marked vendor-claimed or third-party measured. Validate before building
on it. The audit (`_ai_/bench/audit-2026-08-18.md` section 3) and
`local-inference-apple-silicon.md` are the earlier passes this extends.

Every number carries a URL. **[V]** = vendor-claimed, **[M]** = third-party measured.

## 0. Corrections to the brief's premises

- The current Anthropic mid-tier is **Claude Sonnet 5** (2026-06-30), at **$2 in / $10 out per MTok** — *cheaper* than Sonnet 4.5/4.6, now legacy at $3/$15. Haiku 4.5 is unchanged at $1/$5. ([models overview](https://platform.claude.com/docs/en/about-claude/models/overview), [pricing](https://platform.claude.com/docs/en/about-claude/pricing))
- Qwen shipped four generations in 2026: **Qwen3.5** (Feb–Mar), **Qwen3.6** (Apr), Qwen3.7, and **Qwen3.8-27B on 2026-08-14** — four days ago. ([Qwen3.5](https://github.com/QwenLM/Qwen3.5), [Qwen3.8](https://github.com/QwenLM/Qwen3.8)). Qwen3-Coder-30B-A3B and qwen3:8b are two generations obsolete; your 2/10 result says nothing about 2026 models.
- **GLM-4.6-Air and GLM-4.7-Air never shipped.** GLM-4.7-Flash (30B-A3B, 2026-01-20) is the actual successor to GLM-4.5-Air.
- **Aider polyglot is dead as a source** — frozen since Oct 2025, no 2026 model on it ([leaderboard yml history](https://github.com/Aider-AI/aider/blob/main/aider/website/_data/polyglot_leaderboard.yml)).

## 1. Local candidates that fit 18–20 GB

File sizes read directly from the Hugging Face blob API.

| Model | Params | Best GGUF ≤ ~18 GB | MLX 4-bit | Decode, Apple silicon |
|---|---|---|---|---|
| **Qwen3.8-27B** (2026-08-14) | 27B dense, hybrid Gated DeltaNet | Q4_K_M **17.11 GB**, IQ4_XS 15.71 ([unsloth](https://huggingface.co/unsloth/Qwen3.8-27B-GGUF)) | **16.08 GB** ([mlx-community](https://huggingface.co/mlx-community/Qwen3.8-27B-4bit)) | none found |
| **Qwen3.6-27B** (Apr) | 27B dense, same | Q4_K_M 16.8, UD-Q4_K_XL 17.6 ([unsloth](https://huggingface.co/unsloth/Qwen3.6-27B-GGUF)) | 16.08 GB | **~9.6 tok/s** M4 Pro, Ollama [M, weak] |
| **Qwen3.6-35B-A3B** (Apr) | 35B / 3B MoE | UD-IQ4_XS **17.73**, UD-Q3_K_XL 16.85 ([unsloth](https://huggingface.co/unsloth/Qwen3.6-35B-A3B-GGUF)) | 20.43 GB — no headroom | ~32 tok/s M4 Pro MLX Q4 [M, weak] |
| **GLM-4.7-Flash** | 31B / ~3B MoE | MXFP4_MOE 16.97, IQ4_XS 16.27 ([unsloth](https://huggingface.co/unsloth/GLM-4.7-Flash-GGUF)) | 16.87 GB | **50.2 → 6.2 tok/s** from 0 → 100k ctx, M1 Ultra [M] ([disc. #19120](https://github.com/ggml-org/llama.cpp/discussions/19120)) |
| **Qwen3.5-9B** | 9B dense | Q4_K_M **5.68**, Q8_0 9.53 ([unsloth](https://huggingface.co/unsloth/Qwen3.5-9B-GGUF)) | 5.98 / 8-bit 10.45 | — |
| **Qwen3.5-4B** | 4B dense | — | 8-bit **5.16 GB** | 48 tok/s [M, non-Apple] |
| gpt-oss-20b | 21B / 3.6B | MXFP4 ~12 GB | 8-bit 22.26 (too big) | 65 tok/s [M, non-Apple] |

### The vendor claims, and what independent measurement says

Qwen's own card ([Qwen3.6-27B](https://huggingface.co/Qwen/Qwen3.6-27B)) **[V]**, internal bash + file-edit scaffold:

| | Qwen3.6-27B | Qwen3.6-35B-A3B | Claude 4.5 **Opus** |
|---|---|---|---|
| SWE-bench Verified | 77.2 | 73.4 | 80.9 |
| Terminal-Bench 2.0 | 59.3 | 51.5 | 59.3 |
| LiveCodeBench v6 | 83.9 | 80.4 | 84.8 |

Qwen3.8-27B **[V]** goes further: SWE-bench Pro 61.7, Terminal-Bench 2.1 (Terminus) 73.0, LiveCodeBench v6 90.3 ([card](https://huggingface.co/Qwen/Qwen3.8-27B)).

**Now the independent numbers.** Terminal-Bench publishes its raw submission data, including which runs its team audited:

| Model | Terminal-Bench 2.0 [M] |
|---|---|
| Claude Sonnet 4.5 | **40.1 – 46.5%** (varies by scaffold) |
| MiniMax M2.7 | 42.9 – 45.1% |
| Kimi K2.5 | 43.2% (verified) |
| DeepSeek V3.2 | 39.6% |
| GLM-4.7 | 33.3 – 33.4% |
| Claude Haiku 4.5 | 13.9 – 35.5% |
| Qwen3-Coder-480B | 23.9 – 27.2% |
| **Qwen3.6-35B-A3B** | **23.0 – 24.6%** |
| Qwen3.5-9B | 9.2% |
| gpt-oss-20b | 3.1 – 3.4% |

(source: tbench.ai's leaderboard JSON payload — 142 TB2.0 rows; Claude Sonnet 5 appears only on TB2.1 at **74.6% ± 1.6%** with the Claude Code scaffold)

**Qwen's claimed 51.5 for Qwen3.6-35B-A3B is roughly double the ~24% independent measurement of the same model on the same benchmark.** On the only agentic-coding benchmark where both a local candidate and the Claude models are measured by the same third party, the best local MoE lands at about half of Sonnet 4.5 and inside Haiku 4.5's scaffold spread. Berkeley's BFCL v4 tells the same story from the tool-calling side: Sonnet 4.5 **73.24%** and Haiku 4.5 **68.70%** in function-calling mode, with **no Qwen3.6 or Qwen3.8 entry at all** ([data_overall.csv](https://gorilla.cs.berkeley.edu/data_overall.csv)).

No independent evaluation exists for Qwen3.6-27B or Qwen3.8-27B on any agentic benchmark. Given the 2× gap measured on their sibling, treat their vendor tables as marketing until someone reruns them.

### llama.cpp tool-calling defects that hit these exact models

Verified today via the GitHub API:

- **[#26530](https://github.com/ggml-org/llama.cpp/issues/26530)** (open, 2026-08-03) — Qwen3-Coder-template models "often fail to output `<tool_call>`" with **20+ tools and 10k+ token prompts**. Response comes back as plain text with `finish_reason: stop`; the lazy grammar "is correctly generated but never triggered." `tool_choice: "required"` works, `auto` does not. This is the closest published match to a 2/10 edit rate.
- **[#26987](https://github.com/ggml-org/llama.cpp/issues/26987)** (open, 2026-08-12) — same trigger, on Qwen3.6-35B-A3B, when the model abandons the XML format entirely. Reporter ruled out their proxy with a 20/20 direct replay; unresolved.
- **[#20837](https://github.com/ggml-org/llama.cpp/issues/20837)** (open since 2026-03-21, 59 comments, active 2026-08-10) — the one you named. Maintainer `pwilkin`: the grammar trigger is independent of the reasoning parser and forces EOG, so "this affects all models that have a tendency to add tool calls within thinking blocks." [PR #20970](https://github.com/ggml-org/llama.cpp/pull/20970) did not fully fix it; still reproduced on Qwen3.6-35B-A3B. Workarounds: `enable_thinking:false`, or `--reasoning-format none`.
- **[#26356](https://github.com/ggml-org/llama.cpp/issues/26356)** (open, unanswered) — with the **default unified KV cache** and 4 concurrent requests, well-formed tool-call emission decays from 54% to **4%** over 350 BFCL entries. `--no-kv-unified` holds 82–88%.
- **[#25923](https://github.com/ggml-org/llama.cpp/issues/25923)** (open) — an empty-object tool schema or `maxLength ≥ 2000` emits invalid GBNF that breaks the *whole* combined tool grammar. Reproduced against Claude Code's real tool set.

## 2. MLX vs llama.cpp on M4 Pro

**Speed: yes, roughly 2×, and the one M4 Pro measurement is stark.** [llama.cpp#19366](https://github.com/ggml-org/llama.cpp/issues/19366) reports Qwen3-Coder-Next Q4_K_M at **~24 tok/s under llama.cpp vs ~60 tok/s under MLX 4-bit on the same M4 Pro** [M]; closed as stale, never addressed. On an M4 Max/128 GB with Qwen3.5-35B-A3B 4-bit: **MLX 130.2, llama.cpp ~71, Ollama ~45** [M] ([Kapetanović, 2026-03-18](https://antekapetanovic.com/blog/qwen3.5-apple-silicon-benchmark/)). The gap is widest on hybrid linear-attention models, where llama.cpp's Metal kernels are newest. Note llama.cpp logs `tensor API disabled for pre-M5 and pre-A19 devices` — the Metal 4 tensor path is M5-only ([#20141](https://github.com/ggml-org/llama.cpp/issues/20141)).

**Tool calling: no, and this is disqualifying.** `mlx_lm.server` uses a hand-written per-model parser registry, and the model you'd most want has no parser: [mlx-lm#1293](https://github.com/ml-explore/mlx-lm/issues/1293) (open since 2026-05-21) — "Qwen 3.5 35B A3B and Qwen 3.6 27B do not emit `tool_calls`… `_infer_tool_parser()` returns `None` → models respond with empty content when tools are provided." Alongside: [#1627](https://github.com/ml-explore/mlx-lm/issues/1627) (qwen3_coder drops float-formatted int args, "causing client infinite loops"), [#1604](https://github.com/ml-explore/mlx-lm/issues/1604) (parser crashes the server), [#1371](https://github.com/ml-explore/mlx-lm/pull/1371) (server pre-decodes `function.arguments`, violating the OpenAI spec — unmerged), [#1493](https://github.com/ml-explore/mlx-lm/issues/1493) (hangs at 22–26k-token prompts), [#984](https://github.com/ml-explore/mlx-lm/issues/984) (streaming tool calls never fire when the delimiter spans tokens). LM Studio's MLX path shows the identical failure shape ([#1794](https://github.com/lmstudio-ai/lmstudio-bug-tracker/issues/1794), [#1979](https://github.com/lmstudio-ai/lmstudio-bug-tracker/issues/1979), [#1528](https://github.com/lmstudio-ai/lmstudio-bug-tracker/issues/1528)).

MLX is twice as fast and a generation behind on the thing wavez depends on.

## 3. Hosted tier — live OpenRouter prices

Pulled from `https://openrouter.ai/api/v1/models` today. USD per 1M tokens.

| Model | In | Out | Cached in | Ctx |
|---|---|---|---|---|
| `qwen/qwen3.7-flash` | 0.03 | 0.13 | 0.006 | 1M |
| `z-ai/glm-4.7-flash` | 0.06 | 0.40 | 0.01 | 203k |
| `deepseek/deepseek-v4-flash` | 0.083 | 0.165 | 0.017 | 1M |
| `qwen/qwen3.6-35b-a3b` | 0.14 | 1.00 | 0.05 | 262k |
| `xiaomi/mimo-v2.5` | 0.14 | 0.28 | 0.0028 | 1.05M |
| `openai/gpt-5.6-luna` | 0.20 | 1.20 | 0.02 | 1.05M |
| `minimax/minimax-m3` | 0.30 | 1.20 | 0.06 | 1M |
| `google/gemini-3.7-flash` | 0.375 | 1.875 | 0.038 | 1M |
| `qwen/qwen3.8-27b` | 0.45 | 3.20 | 0.05 | 262k |
| `z-ai/glm-5` | 0.60 | 1.92 | 0.12 | 205k |
| `moonshotai/kimi-k2.7-code` | 0.71 | 3.50 | 0.15 | 262k |
| `anthropic/claude-haiku-4.5` | 1.00 | 5.00 | 0.10 | 200k |
| `deepseek/deepseek-v4-pro` | 1.32 | 3.96 | 0.044 | 1M |
| `openai/gpt-5.3-codex` | 1.75 | 14.00 | 0.175 | 400k |
| **`anthropic/claude-sonnet-5`** | **2.00** | **10.00** | **0.20** | **1M** |

**Prompt caching** is a first-class `input_cache_read` price for most current models. Anthropic's multipliers: 1.25× for a 5-min write, 2× for 1-hour, **0.1× for a read** — a hit pays for itself after one read ([pricing](https://platform.claude.com/docs/en/about-claude/pricing)). Sonnet 5 cached input at $0.20/MTok is the number that decides the architecture question.

**Is the local-orchestrator / hosted-coder split worth it? No.** Three reasons:

1. Agent cost is dominated by re-sent context, and caching already kills it. Sonnet 5 cached input is a fifth of Haiku 4.5's *uncached* rate.
2. The local orchestrator would still emit tool calls through llama.cpp — the component with the open defect list in §1. You keep every failure mode and lose the model quality.
3. Splitting means the model choosing *what* to edit and the model *performing* the edit hold different context. A `str_replace` tool tolerates that badly.

The small tool-calling specialists are also a trap. In a 40-case, 13-model eval (2026-03, LM Studio, Q4_K_M) the three models marketed as specialists — Hammer 2.1 7B, xLAM-2 8B FC-R, Mistral Small 3.2 24B — scored **20.0%, 15.0%, and 42.5%**, against **97.5% for plain Qwen3.5 4B**, because their native formats don't survive OpenAI-compatible translation ([jdhodges](https://www.jdhodges.com/blog/local-llms-on-tool-calling-2026-pt1-local-lm/)) [M]. BFCL rank does not transfer through a harness.

## 4. Recommendation matrix

**(a) Best single local model for agentic edits: Qwen3.8-27B at Q4_K_M (17.11 GB)** via `llama-server --jinja --reasoning-format none --no-kv-unified`. It has the strongest claims, its MLX 4-bit is 16.08 GB (real headroom in 20 GB), and the hybrid architecture keeps the KV cache small (~2 GB at 32k per Qwen3.6-27B's card). Fall back to Qwen3.6-27B if the four-day-old model misbehaves — that architecture needs a very recent llama.cpp *and* a matching `libggml` rebuild ([disc. #27164](https://github.com/ggml-org/llama.cpp/discussions/27164)). Expect **~10 tok/s** on your M4 Pro. Set your expectation to "cheap local turns," not "does the edit."

**(b) Best local model under 10 GB for a tool-calling orchestrator: Qwen3.5-9B at Q4_K_M (5.68 GB)**, run with thinking disabled — it is the exact model in #20837. Qwen3.5-4B (MLX 8-bit, 5.16 GB) is the measured tool-calling leader in the one real eval available. But per §3, don't build the split.

**(c) Best hosted per-dollar coder: `z-ai/glm-4.7-flash` at $0.06/$0.40** for volume, `moonshotai/kimi-k2.7-code` at $0.71/$3.50 as a mid-tier (43.2% TB2.0 verified, above Sonnet 4.5's Claude Code scaffold), and **Sonnet 5 at $2/$10 with caching** when the edit must land. Note `qwen/qwen3.8-27b` is hosted at $0.45/$3.20 — you can run the *identical* model remotely, 8× faster, for fractions of a cent.

**(d) Is "comparable to Sonnet locally" realistic in 2026? No.** Stated plainly: the only independent agentic-coding measurement of the best local candidate puts Qwen3.6-35B-A3B at **~24% on Terminal-Bench 2.0 against Sonnet 4.5's 40–47%** — roughly half, and inside Haiku 4.5's range rather than above it. Its vendor card claims 51.5 for the same benchmark. Add ~10 tok/s decode and at least three open, reproduced llama.cpp defects that drop tool calls under exactly your workload shape (many tools, long prompts, thinking on), and the local tier is a cost-saver for low-stakes turns, not a Sonnet substitute. **Use hosted for edits.**

## Unverified / conflicting sources

- **The largest conflict in this report:** Qwen's card claims Terminal-Bench 2.0 = **51.5** for Qwen3.6-35B-A3B; tbench.ai's own submission data measures **23.0–24.6%**. I cannot reconcile these. By extension, treat the unmeasured Qwen3.6-27B (59.3) and Qwen3.8-27B (73.0) claims as unverified.
- **Sonnet 5 on Terminal-Bench 2.1:** tbench.ai lists **74.6% ± 1.6%** (Claude Code scaffold); a [Vellum transcription](https://www.vellum.ai/blog/claude-sonnet-5-benchmarks-explained) of Anthropic's launch chart reports **80.4**. tbench.ai attributes 80.4 to "Terminus 2 + Fable 5," suggesting Vellum may have misread a row. Anthropic's own figures are published as an image. Unresolved.
- **Cross-version comparisons are invalid.** Haiku 4.5's 41.8 is Terminal-Bench v1; Qwen quotes v2.0 and v2.1 (Terminus). Do not subtract these.
- **Aider freeze date:** the leaderboard page stamps 2025-11-20; the repo's commit history points to 2025-10-03/04. Either way it predates every 2026 model.
- **M4 Pro tok/s for Qwen3.6-27B (~9.6) and Qwen3.6-35B-A3B (~32)** come from SEO-style blogs with no reproducible method — order of magnitude only. The llama.cpp#19366 figures (24 vs 60) are a user report in the tracker and are better sourced.
- **GLM-4.7-Flash's card fetch returned "Released: August 2025"** alongside SWE-bench Verified 59.2 / τ²-bench 79.5. The date is wrong (shipped 2026-01-20), so those scores may belong to GLM-4.5-Air.
- **Could not retrieve:** OpenRouter's programming usage rankings (SPA-only; `/api/frontend/*` 404s), swebench.com's live leaderboard, LiveCodeBench's official board, and taubench.com beyond its top-3 preview — all client-rendered. Several tau-bench and Aider numbers circulating on aggregator sites (llm-stats.com, codesota.com) could not be traced to primary sources and were discarded rather than reported.
- **No SWE-bench Verified figure for Sonnet 5 or Haiku 4.5 was obtainable** from a primary source in this pass; Haiku 4.5's 73.3% comes from Anthropic's launch post [V], Sonnet 5's is published only as an image.
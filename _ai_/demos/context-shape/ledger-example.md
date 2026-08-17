# Hypothesis ledger: "hosted runs report success having done nothing"

Session of 2026-08-16. This is the whole durable output of the
investigation; the transcripts it came from are in the numbers beside it.

| # | Hypothesis | Experiment | Observation | Verdict |
|---|---|---|---|---|
| 1 | OpenRouter routes to a provider that cannot do tool calls | 8 identical requests, record `provider` and `tool_calls` | 8/8 SiliconFlow, 8/8 native | refuted |
| 2 | wavez does not send the tool schemas | local proxy captures the outgoing body | 6 tools present, `stream: true` | refuted |
| 3 | wavez's system prompt causes it | same request with and without the prompt | 15/15 native without, 4/15 with | supported |
| 4 | The project-context half is the cause | bisect the prompt at `## Project context` | context alone 15/15, harness bullets alone 0/15 | refuted for context, supported for harness bullets |
| 5 | The model itself is the dominant cause | hold the request fixed, vary the model | 30b-a3b 3/10; qwen3-coder, glm-4.6, kimi-k2, gpt-5-mini all 10/10 | supported, dominant |

Cause: `qwen/qwen3-coder-30b-a3b-instruct` omits its `<tool_call>` wrapper
(QwenLM/Qwen3-Coder#475), worst when a call follows prose; the harness
bullets that described tools in prose roughly doubled the rate.

Fixes: tool guidance moved into the tool schemas, hosted default changed,
and a turn that renders a call as text now fails instead of completing.

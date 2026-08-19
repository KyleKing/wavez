# prefix-tokens

Spike from `_ai_/research/2026-08-efficiency-frontier.md`: how large is the
stable prefix (system prompt plus tool schemas plus the project's `context`
list) in the served model's own tokens, rather than in source bytes at four
characters a token? The 8k budget share and the hosted caching decision
(Sonnet caches from 1,024 tokens, Haiku from 4,096) both hang on it.

## Method

`measure.py` posts the assembled prefix to a `llama-server --jinja`, once as
raw text through `/tokenize` and once rendered through `/apply-template` with
and without the tools, so the count includes what the chat template adds. The
prefix comes from a throwaway test in `internal/app` that builds the App for
a root with `app.WithProviders(fake...)`, marshals `a.SystemPrefix` and
`a.Tools.Specs()` as OpenAI tool objects to `prefix.json`, and is deleted
afterwards; nothing in the tree dumps it, on purpose.

```
llama-server -m <gguf> --port 8089 -c 8192 --jinja &
./measure.py prefix.json http://127.0.0.1:8089
```

## Numbers

2026-08-18, M4 Pro, this repo's `.wavez.pkl` (two `AGENTS.md` sections in
`context`, seven tools), `qwen3:8b` served by llama-server build 10470:

| Piece | Tokens |
|---|---|
| system text, raw | 1,002 |
| tools JSON, raw | 1,343 |
| template, user turn only | 9 |
| template, system + user | 1,016 |
| template, system + tools + user | 2,441 |
| stable prefix through the template | about 2,430 |
| chars/4 estimate of the same bytes | 2,450 |

So the prefix is 30% of an 8k window and 7% of 32k on this repo, and the
chars/4 heuristic is within 1% of the served tokenizer once the tool schemas
are counted, which `internal/agent`'s estimate now does. The tools cost more
than the system prompt: the template renders each schema with its own
framing, and seven of them are 1,425 tokens against 1,002 for the two
`AGENTS.md` sections.

Not measured: the same prefix through the hosted tokenizers (gpt-5-mini and
Sonnet 5 `usage.prompt_tokens` on a dry-run request), because this laptop
holds no OpenRouter key. On the qwen count it clears Sonnet's 1,024-token
caching floor with room and falls short of Haiku's 4,096, and the hosted
tokenizers usually count English denser than qwen's, so the Haiku half is
the one worth checking before relying on it.

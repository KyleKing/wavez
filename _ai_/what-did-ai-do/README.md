# what-did-ai-do

Quiz yourself on what your AI coding agent actually did, active-recall comprehension checks generated from real Claude Code and Aider session transcripts

## Installation



### GH CLI Extension

> **Note:** Requires a [GitHub Release](https://github.com/kyleking/what-did-ai-do/releases) with precompiled binaries. See [CONTRIBUTING.md](CONTRIBUTING.md#releases) for creating the first release.

```bash
gh extension install kyleking/what-did-ai-do
```

### Homebrew

```bash
brew install kyleking/tap/what-did-ai-do
```

### Go Install


```bash
go install github.com/kyleking/what-did-ai-do/cmd/what-did-ai-do@latest
```


## Development

```bash
mise install && hk install --mise
mise run ci
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for full development workflow.

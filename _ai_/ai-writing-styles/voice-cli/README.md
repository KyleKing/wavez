# voice-cli

CLI for importing and reviewing personal writing samples (iMessage, Mail, Linear) into a voice-guide corpus for Claude Code

## Installation



### GH CLI Extension

> **Note:** Requires a [GitHub Release](https://github.com/kyleking/voice-cli/releases) with precompiled binaries. See [CONTRIBUTING.md](CONTRIBUTING.md#releases) for creating the first release.

```bash
gh extension install kyleking/voice-cli
```

### Homebrew

```bash
brew install kyleking/tap/voice-cli
```

### Go Install


```bash
go install github.com/kyleking/voice-cli/cmd/voice-cli@latest
```


## Development

```bash
mise install && hk install --mise
mise run ci
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for full development workflow.

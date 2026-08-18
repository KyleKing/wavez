# Editing a prompt in Neovim

Plan, not yet built. The composer in `internal/tui/vim.go` already does modal editing
inline and fullscreen. This is about the last step the user asked for: writing a prompt
in real Neovim, with completion for the things only wavez knows.

## What is already true

- The composer's buffer is `[][]rune` behind `Value()` and `SetValue()`, so handing text
  out and taking it back needs no component state to reconcile
- `internal/mention` resolves `@file` and `@symbol` against the code-intelligence store,
  and reports an unresolved reference rather than dropping it
- The store holds 2345 symbols and an FTS index over names, paths, and file text, so the
  candidate list for a completion request is a query it already answers
- Neovim 0.11+ native LSP is what the config uses: one file per server in
  `~/.config/nvim/lsp/`, listed in `vim.lsp.enable({...})` in
  `lua/kyleking/deps/lsp.lua`. Completion is `vim.lsp.completion.enable()` with manual
  trigger on `<C-Space>` by default and an autotrigger toggle. No blink, no nvim-cmp, no
  mini.completion, so a server that speaks `textDocument/completion` needs no plugin

## The three pieces

### 1. Handoff from the composer

`ctrl+x ctrl+e` in the composer writes `Value()` to a temp file, runs `$EDITOR` on it
through `tea.ExecProcess`, and `SetValue()`s the result on return. The readline
convention for "edit this line in an editor" is exactly that chord, and it is free in
both modes. `ctrl+f` stays fullscreen, because the two are different things and a user
who wants the frame should not pay an editor launch.

The temp file needs an extension wavez owns (`.wavez`) so a filetype rule can fire on
it. That is what connects this piece to the next one.

Open: what happens to a draft if the editor exits nonzero, and whether the composer's
own undo stack should be cleared on return (it describes edits the buffer no longer
has).

### 2. `wavez lsp`

A subcommand serving LSP over stdio, so Neovim starts it the way it starts any other
server. Scope for a first pass is one request:

- `textDocument/completion` at an `@` prefix, answered from the mention resolver's own
  index: file paths from the tracked tree, symbols from the store with kind, signature,
  and the first doc line as the completion detail
- `initialize` advertising `completionProvider` with `@` as a trigger character, and
  nothing else, so the client knows not to ask for hover, definition, or diagnostics

`internal/lsp` already speaks the protocol as a *client* (powernap, for the diagnostics
gate). A server is the other half and shares neither code nor dependency, so this is new
surface either way: either hand-roll the framing over stdio, which is small and has no
version risk, or take a server library. Decide with the shape of the request set, which
today is one method.

Reuse rather than re-derive: the resolver's budget rules, its ambiguity handling, and its
"unresolved says why" behavior are the product of the mention work and should not be
reimplemented behind the LSP boundary. The server is a transport over
`internal/mention`, not a second implementation of it.

Open: whether a completion request should refresh the index first. The freshness
doctrine says every query re-checks, measured at 18 ms on an unchanged tree, which is
inside a keystroke budget. That is probably a yes.

### 3. The Neovim side

Two files, both following conventions already in the config:

- `~/.config/nvim/lsp/wavez.lua`, matching the shape of `lsp/gopls.lua`:
  `filetypes = { "wavez" }`, `root_markers = { ".wavez.pkl", ".git" }`, and a `cmd` of
  `{ "wavez", "lsp" }`
- `"wavez"` added to the `vim.lsp.enable({...})` list, plus a filetype rule mapping
  `*.wavez` to the `wavez` filetype

Nothing else. `<C-Space>` already triggers completion on any attached client, and the
autotrigger toggle already works per buffer.

## Order

The handoff is useful with no server at all, since `$EDITOR` on a temp file is already
better than an inline box for a long prompt. Build it first because it is bounded and
testable in a PTY, then the server, then the two config files.

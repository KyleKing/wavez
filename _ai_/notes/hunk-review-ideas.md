# Hunk-style review from Neovim (idea)

Source: a 2026-08 exploration session (transcript removed from the repo, in git history under `_ai_/nvim-hunk-code-review-ideas/`).

- hunk.dev-style review for any jj or git commit, driven from Neovim, iterated locally, then posted deterministically because the review data is structured
- Wanted and missing elsewhere: incremental diff across a whole changeset (not per file), ignore merges, review state that survives force-pushes via a local cache
- hunk already has native jj support (revsets, pager). The unclaimed ground is jj-specific: conflict as state, bookmarks, the operation log
- difftastic gives structural diff. delta is line-based prettification only
- Reusable: the floating-terminal launcher pattern in `~/.config/nvim/lua/kyleking/deps/terminal-integration.lua` for embedding external TUIs

Maps to Wavez: the v0.4 VCS layer and the review-state-that-survives-force-push item in the deferred list.

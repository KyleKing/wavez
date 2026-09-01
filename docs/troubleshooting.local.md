# Troubleshooting (wavez-specific)

## golangci-lint reports issues in files that do not exist

`golangci-lint` caches results by package path, and the cache outlives the
directory. After a sibling worktree is deleted, a run from this repo still
prints that worktree's findings under `../<name>/...`, so a clean tree can
report dozens of issues and a real regression hides among them. Check whether
the reported path exists before chasing it, and clear the cache:

```bash
mise exec -- golangci-lint cache clean
```

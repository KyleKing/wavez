---
name: wavez-refactor
description: Renaming, moving, or deleting Go declarations, extracting lines into a function, removing or reordering a function's parameters, or inlining a call. Run `wavez rename|move|delete|extract|params|inline` (one call that updates every reference and compiles the result) before reaching for Edit or sed.
---

# wavez refactor

Six commands that take Go names or line ranges instead of text. Run them through the Bash
tool as `wavez ...`, never by editing each occurrence yourself. Paths are
relative to the working directory or the module root (`--root DIR` overrides
it). Each command changes everything or nothing, compiles the touched
packages with their tests, and prints a few lines. A package that already
failed to build is fine: the change is kept unless it adds an error.
`--dry-run` prints the same report and leaves the tree as it was.

## rename

```bash
wavez rename internal/bench/stats.go:Read ReadLog
```

Target is `file.go:Name` or `./pkg/dir:Name`; `Name` may be `Type.Method` or
`Type.Field`. Renames the declaration and every reference in the module,
tests and `Example` functions included. An interface method takes its
implementers with it. The report ends with the `file:line` places it left
for you:

- `not renamed, excluded from this build`: code for another GOOS/GOARCH or
  behind a build tag. Edit these, or that build breaks.
- `still appears in N comment or string lines`: edit the ones that mean
  this symbol.

## move

```bash
wavez move internal/guard/split.go:splitSequence,splitPipeline internal/guard/sequence.go
```

Moves top-level funcs, methods (`Type.Method`), types, consts, and vars, with
their doc comments, to another file of the same package. The destination is
created if absent, and imports are fixed in both files.

## delete

```bash
wavez delete internal/edit/apply.go:ApplyToFile internal/edit/apply_test.go:TestApplyToFile
```

Deletes declarations across any number of files and fixes imports. Deleting a
type deletes its methods. A file left with nothing in it is removed.

## extract, params, inline

- `wavez extract internal/x/run.go:40-58 parseFlags` moves the whole statements on those lines into a new function (a method if they use the receiver) and prints its signature.
- `wavez params internal/x/run.go:Run ctx,opts` keeps those parameters in that order at the declaration and every call; leave one out to remove it. Adding one is not supported.
- `wavez inline internal/x/run.go:72 helper` replaces the calls to `helper` on that line (or `FROM-TO`) with its body.

## Refusals

A refusal leaves the tree untouched and says what to do next: use the target
or name it offers, or run the `wavez delete ...` line it prints when the
holders it lists (tests, usually) should go too. `the result does not build`
lists only the compile errors the change would have added.

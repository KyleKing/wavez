---
name: wavez-refactor
description: Renaming a Go function, method, or type, moving declarations to another file of the package, or deleting declarations with their tests. Run `wavez rename|move|delete` (one call that updates every reference and compiles the result) before reaching for Edit or sed.
---

# wavez refactor

Three commands that take Go names instead of text. Run them through the Bash
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

## Refusals

A refusal leaves the tree untouched and says what to do next: use the target
or name it offers, or run the `wavez delete ...` line it prints when the
holders it lists (tests, usually) should go too. `the result does not build`
lists only the compile errors the change would have added.

---
name: wavez-refactor
description: Renaming a Go function, method, or type, moving declarations to another file of the package, or deleting declarations with their tests. Run `wavez rename|move|delete` (one call that updates every reference and compiles the result) before reaching for Edit or sed.
---

# wavez refactor

Three commands that take Go names instead of text. Run them through the Bash
tool as `wavez ...`, never by editing each occurrence yourself. Paths are
relative to the working directory or the module root (`--root DIR` overrides
it). Each command changes everything or nothing, ends by compiling the touched
packages with their tests, and prints a few lines.

## rename

```bash
wavez rename internal/bench/stats.go:Read ReadLog
```

Target is `file.go:Name` or `./pkg/dir:Name`; `Name` may be `Type.Method` or
`Type.Field`. Renames the declaration and every reference in the module, tests
included. The report lists the files changed, then `file:line` for comments
and strings that still say the old name. Those are not edited: read them and
fix the ones that mean this symbol.

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

Every refusal leaves the tree untouched and says what to do next.

- `no declaration named`: wrong name or wrong file. Use the offered name or
  target.
- `names N declarations`: the name is ambiguous. Re-run with one of the
  listed targets.
- `still used outside the declarations being deleted`: lists each holder as
  `file:line in Decl`. If the holders should go too (tests), run the printed
  `wavez delete ...` line; otherwise change them first.
- `is declared in one block with`: a `const`/`var`/`type` block moves or
  deletes whole. Name every member, or split the block first.
- `move stays inside one package`: cross-package moves are unsupported.
- `gopls refused`: its reason follows (name conflict, symbol in a dependency).
- `does not typecheck even without this change`: fix the listed compile
  errors, then re-run.
- `the result does not build`: the change was undone; the compiler output
  says what it would have broken.
- `gopls is not installed`: `go install golang.org/x/tools/gopls@latest`.

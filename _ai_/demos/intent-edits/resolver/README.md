# intentgo

Prototype for the "Intent edits" idea in [DESIGN.md](../../../../DESIGN.md): turn a
short intent line into a correct Go code shape, deterministically, leaving only a
marked hole for a model to fill.

## Build and run

```
go build -o intentgo .
./intentgo 'add fn RemoteIdentity(remoteURL string) string in ./internal/vcs near CheckoutIdentity doc="..."'
./intentgo 'add field RepoSummary.RemoteID string in ./internal/models doc="..."'
./intentgo 'like PruneRemote: add DeleteBranch in ./internal/vcs'
```

Go 1.26. One dependency, `golang.org/x/tools/imports`. Every command prints a
unified diff to stdout, a JSON summary (files touched, lines added, hole lines,
imports added, warnings) to stdout after the diff, and wall time to stderr.

## Grammar

```
add fn NAME(PARAMS) RESULTS in PKGDIR [near SYMBOL] [test=yes|no] [doc="..."]
add field TYPE.FIELD FTYPE in PKGDIR [tag="..."] [doc="..."]
like SRC: add DST in PKGDIR
```

`add fn` places the function after `near`'s decl (function, method, type, or
var/const) in whichever file holds it, or appends to the package's main file
(the file named after the package, else the first alphabetically) if `near` is
omitted or not found. Body is `panic("TODO(intent): NAME")` under a `// HOLE:`
comment. `golang.org/x/tools/imports.Process` runs on the result. `test=yes`
appends `TestNAME` to the sibling `*_test.go`, copying `t.Parallel()` and
table-driven shape from that file if present.

`add field` appends after the struct's last field and lets `go/format` realign
columns. It warns (does not edit) if `NewTYPE` or `WithFIELD` already exists.

`like` copies SRC's decl, renames SRC→DST (and their lowercased-first-letter
variants) via `go/ast`, keeps only the error-handling skeleton (`err`-setting
assignments, `if err...` guards, the final return) under a `// HOLE:` comment,
and drops other statements. If `TestSRC` exists it mirrors that too, whole
body, flagged for review rather than holed.

## Evaluation

Three cases from `gh-repo-dashboard` (read-only; copies of `internal/vcs` and
`internal/models` at each commit's parent live under `testdata/`). Each intent
was hand-written from reading the real commit, then run and diffed against the
actual post-commit file (`gofmt`-equivalent, since `intentgo` always runs
`go/format`).

| case | intent | commit | placement | signature | body | doc | imports |
|---|---|---|---|---|---|---|---|
| A: add fn | `RemoteIdentity` in `vcs`, near `CheckoutIdentity` | [0c5f099](https://github.com/kyleking/gh-repo-dashboard/commit/0c5f099) | exact (right after `CheckoutIdentity`, matches commit) | exact match, byte for byte | 1 hole line, correctly covers all ~24 logic lines | present, name-first, but one line vs. the commit's six | correct (none needed, none added) |
| B: add field | `RepoSummary.RemoteID string` in `models` | [0c5f099](https://github.com/kyleking/gh-repo-dashboard/commit/0c5f099) | end of struct (deterministic default); commit put it mid-struct after `RemoteRepo` for grouping | field name/type/nothing to check | n/a, no hole needed for a field | correct content, not wrapped to the repo's ~80-col comment width | correct (none needed) |
| C: like | `PruneRemote` → `DeleteBranch` in `vcs` | [677c81e](https://github.com/kyleking/gh-repo-dashboard/commit/677c81e) | exact (same file, right after SRC) | wrong: kept `PruneRemote`'s signature, commit's `DeleteBranch` needs two more params (`branch string, force bool`) the grammar has no slot for | skeleton shape (`if err != nil { return }` + final return) matches structurally; the kept statements still carry SRC's literal call and message, not a hole, so they read as done but are wrong | copied and renamed correctly; `//nolint` directive line silently dropped (`ast.CommentGroup.Text()` strips directive comments) | correct (none needed) |

Wall time: 107ms, 61ms, 71ms respectively, each including a `go/parser` load
of the whole package. All three outputs pass `go/format.Source` (the resolver
refuses to write anything gofmt itself would reject) and were diffed by eye
against the real commit's file, shown above.

## What worked

Deterministic placement after a named symbol is exact in both cases that use
`near`/`SRC`: the resolver finds the right file and the right line without any
model involvement. Doc-comment style detection (symbol-name-first) matches the
package's convention. `imports.Process` correctly added nothing when nothing
was needed; it wasn't exercised on a case that actually required a new import,
which is a gap in this run, not evidence it works under load. Field insertion
and gofmt realignment worked with zero manual intervention.

## What broke

`like` only mirrors SRC's own shape. It has no way to know DST's signature
should differ from SRC's when the two functions do genuinely different things,
because the grammar gives DST no params/results slot of its own. That is a
grammar gap, not just an implementation gap: `like Foo: add Bar` from the
DESIGN.md doc has the same shape. The kept error-handling statements carry
SRC's literal arguments forward instead of holing them, which means `like`'s
output looks more finished than it is. Doc-comment mirroring drops any
directive-shaped comment line (`//nolint:...`) because `go/ast`'s own
`CommentGroup.Text()` strips those; a resolver aiming for parity would need to
walk the comment list itself instead. Long `doc="..."` values aren't wrapped
to match the repo's line width. Field placement is deterministic-by-rule
(append at end) rather than semantic (group with related fields), which is
the right call for a resolver that "never guesses semantics silently," but it
means a human still moves the line by hand about half the time in a repo with
DESIGN.md's grouping conventions.

Code came in around 1,050 lines against an ~800-line target, mostly in
`like.go`: renaming identifiers safely needs two full parse/format round
trips (rename on a fresh AST, reprint, reparse to get clean statement
positions) to avoid hand-rolling a comment-aware AST-to-text printer.

## Verdict

A deterministic resolver gets placement, imports, and doc style essentially
free for straight "add a function/field next to X" edits; that is the
majority of the diff's line count in a small addition, and it needed zero
model tokens to get there. It cannot infer a new signature, cannot judge
whether a field belongs mid-struct for readability, and cannot write a single
line of real logic; all three become explicit holes or grammar gaps rather
than silent guesses, which is the property the design says matters most. The
`like` intent is the shakiest of the three primitives as specified: it only
pays off when SRC and DST truly share a signature, and needs either a richer
grammar or a second resolution pass to be trustworthy past that.

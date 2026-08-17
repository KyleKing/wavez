# Spike: can the generalize phase be mechanically seeded

Feeds the Cycles section of [DESIGN.md](../../../DESIGN.md). The question:
after a root cause is proved, can the harness enumerate the sibling sites,
or must the model imagine them? Asking a model "where else does this
happen" is exactly the kind of recall it is worst at, and there is no way to
tell a complete answer from a confident one.

## Method

Express the proved cause as an `ast-grep` pattern and scan. Both cases below
are real: they are the two root causes found in the session that produced
this spike.

## Case 1: local and syntactic

Cause: a gate that scoped itself to a subset of a change set could examine
zero units and still return `Pass: true`, so the gate log recorded a clean
check that ran nothing. Found in `internal/gate/gotest.go`; the question was
where else.

```
./sweep.sh 'if len($C) == 0 { return $T{$$$A, Pass: true}, nil }'
```

Against the tree at `5e77501`, before the fix:

```
internal/gate/gotest.go:181
internal/gate/convention.go:59
internal/gate/convention.go:68
internal/gate/format.go:51
--- 4 hit(s)
```

Four hits, three files, zero false positives across `internal/`, and exactly
the set that was fixed by hand afterwards. Dropping the `Pass: true` element
from the pattern returns the same four, so the shape alone is specific
enough here.

Run against the tree today, after the fix, the same pattern still returns
three hits: `format.go:53` and both sites in `convention.go`. Those are the
abstentions that were triaged as benign and deliberately left returning a
pass, now with an `Examined` count recording that they checked nothing. That
is the argument for triage over autofix in one line: the sweep finds the
shape, and only a reader knows which instances of the shape are wrong.

## Case 2: dataflow across functions

Cause: an absolute path handed to `ast-grep`, whose rule `files:` globs are
matched against the path it is given, so every scoped rule silently matched
nothing. The general shape is "a path is made absolute, then passed to an
external tool that wanted a relative one" — which crosses function
boundaries.

```
./sweep.sh 'filepath.Join($ROOT, $P)'
```

100 hits across `internal/`, overwhelmingly tests and unrelated path
construction. Useless as a work list.

## What this establishes

The sweep works when the cause is **local and syntactic**: expressible as
one node shape, matchable without following a value. It fails when the cause
is **dataflow across functions**, because `ast-grep` matches structure, not
reaching definitions, and the structural half of such a cause is far too
common to be a signal.

So the generalize phase is seedable, not automatic. The split it implies:

- Local syntactic cause: the harness produces the work list, the model
  triages it, and the pattern itself becomes the durable artifact (a rule
  under `rules/`) that stops the class from returning
- Dataflow cause: the sweep is noise, and the durable artifact has to be
  something other than a pattern — a helper that makes the wrong call
  impossible to write, or a test at the boundary. Case 2's real fix was of
  that shape: a test pinning the argument form, because no pattern
  distinguishes the right `filepath.Join` from the wrong one

The 100-hit result is the useful half of this spike. A generalize phase that
always sweeps would hand the model a work list that is noise half the time,
and a phase whose exit condition is "every hit accounted for" would then be
worse than no phase at all. The exit condition has to accept "the sweep does
not discriminate here, and the artifact is this instead".

## Limits

`ast-grep` only, Go only, one pattern per sweep. Semgrep's single-file taint
would cover part of case 2 and is already an opt-in routine in DESIGN.md;
whether it closes the gap is unmeasured.

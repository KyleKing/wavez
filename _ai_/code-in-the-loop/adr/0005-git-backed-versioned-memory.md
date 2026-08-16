# ADR-0005: Git and YAML as the memory substrate

Date: 2026-08-02
Status: Accepted

## Context

The brief calls for humans to see how memories change over time, review changes, and make
manual edits. That is version control, and building a bespoke one is a large amount of work
to arrive somewhere worse than git.

The lifecycle survey names lineage as one of seven fields in its skill record and lists
rollback, re-admission, attribution, and version audit as the operations it enables. Git
provides all four.

## Decision

Task memories and site knowledge are YAML files in a git repository. Trusted memories live on
a protected branch. Agent-authored edits are written to a quarantine path and land on the
trusted branch only through human promotion. Review is a diff.

Serialization is canonical: keys in schema order, lists in schema-defined order, stable
formatting, so a semantically identical rewrite produces an empty diff.

Volatile statistics are split into separate files from semantic content.

## Consequences

Rollback, blame, bisect, and history are free and already understood. A reviewer asking when
a step changed and why gets an answer from tooling they already use.

Human manual edits are natural. Open the file, change two lines, commit. Nothing about the
system's format makes the human a second-class author, which matters because
edit-then-approve is the review action most likely to produce a good memory.

Signed commits and branch protection give the memory store the same integrity controls as
code, which it needs, because a trusted memory is executed directly.

Canonical serialization is what makes review viable at all. Without it, agent rewrites produce
diffs full of reordered keys and a reviewer stops reading them.

The costs: git is a poor query engine, so anything asking "which memories reference this
element" needs an index built alongside. Concurrent writes from parallel runs need
serialization or they produce conflicts on statistics files, which argues for statistics being
append-only journals reduced periodically rather than files mutated in place. And YAML has
sharp edges around implicit typing that a strict loader and schema validation on write have to
close.

Sharing memories across machines or people becomes a git remote, which is convenient and
makes the supply chain concern from the lifecycle survey immediate rather than theoretical.

## Alternatives rejected

**A database with an audit table.** Better queries, and reinvents diff, merge, blame, and
branch, and makes manual human editing an application feature rather than a text editor.

**JSON.** Better tooling and worse diffs, no comments, and comments in a memory file are
where a human explains why a step is weird.

**Append-only event log as the source of truth.** Precise history and no reviewable current
state, so a human cannot read what will execute without replaying the log.

**Code files rather than data files.** Executable memories as Python or TypeScript would be
more expressive. It erases the line between what an agent may edit and what it may not, which
is the line ADR-0007 depends on.

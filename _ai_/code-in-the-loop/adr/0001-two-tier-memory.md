# ADR-0001: Task memories and site knowledge are separate structures

Date: 2026-08-02
Status: Accepted, amended by [ADR-0010](0010-compile-plans-not-record-steps.md)

The two-structure split stands. ADR-0010 changes what the task-side structure *is*: a goal file
plus a compiled, disposable plan rather than a recorded step list. The element-handle
indirection and the origin partition described below are unchanged, and both matter more under
compilation, since site knowledge is now the compiler's input.

## Context

An agent repeating a browser task needs two kinds of knowledge that are easy to conflate. It
needs the specific sequence that accomplishes this outcome, and it needs general facts about
the site that let it recover when the sequence stops working.

Most published skill and memory systems keep one pool. Agent Workflow Memory induces
workflows from trajectories into a single library. WALT induces site-level tools, which is
the broad half, without a composed task layer above it. The Dynamic Agent Skills lifecycle
survey reports that flat retrieval degrades at moderate library sizes, in the tens to
hundreds, and that focused libraries outperform comprehensive ones.

## Decision

Two structures, partitioned by origin.

Task memories are narrow, ordered, and about intent. One per user-meaningful outcome.

Site knowledge is broad, unordered, and about the site. One per origin, covering routes,
elements with locator strategies, endpoint classifications, flows, auth markers, and hazards.

Task memory steps reference elements by handle into the site knowledge catalog, and never
carry locators inline.

## Consequences

Healing a locator once fixes every task memory targeting it, and a redesign that moves one
control does not invalidate a hundred task files.

The diff makes the distinction between "the site changed" and "the task changed" mechanical
rather than interpretive, which is what lets the review surface treat the two classes of
change differently.

The crawler can maintain the broad half continuously without ever executing a task, which is
what makes site knowledge available at escalation time without having paid to discover it
during the run.

Escalation can carry a targeted slice of broad knowledge alongside the narrow failing step,
which is the combination that makes single-turn repair common.

The cost is indirection. Reading a task memory does not tell you what will be clicked, and
debugging requires joining two files. A tool that renders the resolved view is close to
mandatory rather than nice to have.

A second cost is referential integrity. Deleting an element from the catalog can break task
memories silently, so element entries are marked missing rather than deleted, and a
consistency check has to run on write.

## Alternatives rejected

**One pool with similarity retrieval.** The standard approach, and the survey evidence says
it degrades at exactly the library sizes this system reaches. Exact lookup by origin and task
identity has no retrieval failure mode at all.

**Locators inline in task memories.** Simpler to read and it makes every task memory an
independent copy of decaying site facts, so a redesign requires N repairs instead of one.

**Site knowledge only, with tasks as free-form goals.** This is a plain agent with a good
prompt, and it pays full model cost on every run.

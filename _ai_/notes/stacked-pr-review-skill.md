# Stacked PRs: quotes, links, and a skill sketch

Context: research pulled together while working through a review disagreement on
[coverbasedev/irm#13678](https://github.com/coverbasedev/irm/pull/13678) about whether a
backend-accessor-then-caller stack needed each PR to be independently reviewable.

## Core references

- [Stacked Diffs (and why you should know about them) — The Pragmatic Engineer](https://newsletter.pragmaticengineer.com/p/stacked-diffs)
- [Stacked diffs and tooling at Meta with Tomas Reimers — The Pragmatic Engineer](https://newsletter.pragmaticengineer.com/p/stacked-diffs-and-tooling-at-meta)
- [Stacked PRs: A Better Way to Review Code — Aviator (Travis DePrato)](https://www.aviator.co/blog/stacked-prs-code-changes-as-narrative/)
- [Stacked diffs — Graphite guide](https://graphite.com/guides/stacked-diffs)
- [5 problems stacked diffs address — Graphite](https://graphite.com/guides/5-problems-stacked-diffs-address)
- [In Praise of Stacked PRs — Ben Congdon](https://benjamincongdon.me/blog/2022/07/17/In-Praise-of-Stacked-PRs/)
- [Stacked Pull Requests — Awesome Code Reviews](https://www.awesomecodereviews.com/best-practices/stacked-prs/)
- [Stacked pull requests are now in public preview — GitHub Changelog, 2026-07-30](https://github.blog/changelog/2026-07-30-stacked-pull-requests-are-now-in-public-preview/)
- [Graphite Business Breakdown & Founding Story — Contrary Research](https://research.contrary.com/company/graphite)
- [Stacked Diffs vs. Trunk Based Development — Alex Jukes](https://medium.com/@alexanderjukes/stacked-diffs-vs-trunk-based-development-f15c6c601f4b)

Adjacent concepts (not stacked-PR literature, but relevant by analogy to review order):

- Alistair Cockburn, walking skeleton — *Crystal Clear: A Human-Powered Methodology for Small Teams* (2004); restated in ["Start with a Walking Skeleton," 97 Things Every Software Architect Should Know](https://www.oreilly.com/library/view/97-things-every/9780596800611/ch60.html)
- Debnath, Alkobaisi, Bae, Narayanappa, "Steel threads: Software engineering constructs for defining, designing and developing software system architecture" (2012) — [PDF](https://www.cs.du.edu/~snarayan/sada/docs/steelthreads.pdf); modern explainer: [Jade Rubick, "Steel threads"](https://www.rubick.com/steel-threads/)

## Quotes worth citing

On why stacking exists at all:

> "Stacking lets you make many small PRs easily, and without having to wait for review."
> — Tomas Reimers, Graphite cofounder / ex-Meta ([Pragmatic Engineer](https://newsletter.pragmaticengineer.com/p/stacked-diffs-and-tooling-at-meta))

> "Pretty much 100% of engineers used stacking all the time. It was a cultural norm."
> — Aryaman Naik, ex-Meta ([Pragmatic Engineer](https://newsletter.pragmaticengineer.com/p/stacked-diffs))

On splitting strategy (the one directly relevant to the #13678 debate — and it cuts *against* requiring every PR to show its own usage):

> "A large piece of work that changes 1,000 lines of code can be broken into three separate diffs as follows: 1. Scaffolding (800 lines)... 2. Core business logic (150 lines)... 3. Edge cases (50 lines)."
> — Gergely Orosz ([Pragmatic Engineer](https://newsletter.pragmaticengineer.com/p/stacked-diffs))

The scaffolding PR in that example ships with no caller, same shape as tidefield's accessor-first split. The Awesome Code Reviews guide gives a second, independent example splitting "according to our layered architecture: database, business logic, and UI changes" — also presented as ordinary, not a smell. Neither source backs the position that a PR needs its usage attached to be reviewable; both treat layer-first stacks as normal.

On PR size and review quality generally:

> "Productive engineers tend to make small-enough pull requests, which are atomic enough and easy to review."
> — Gergely Orosz ([Pragmatic Engineer](https://newsletter.pragmaticengineer.com/p/stacked-diffs))

> "A pull request that's twice as large takes more than two times as long to review, or, at least, to review thoroughly."
> — Travis DePrato ([Aviator](https://www.aviator.co/blog/stacked-prs-code-changes-as-narrative/))

The one quote that *does* support the "show me it used" instinct, though about narrative rather than layering specifically:

> "Stacked pull requests work so well because they allow developers to construct a narrative with their PRs."
> — Travis DePrato ([Aviator](https://www.aviator.co/blog/stacked-prs-code-changes-as-narrative/))

On the requirement that stack units be independently reviewable (a weaker claim than "independently demonstrable" — it does not require a caller to exist):

> "For the 'stacking' pattern, the important thing is that atomic units of code changes can be (1) ordered as a DAG and (2) reviewed independently."
> — Ben Congdon ([In Praise of Stacked PRs](https://benjamincongdon.me/blog/2022/07/17/In-Praise-of-Stacked-PRs/))

## Where my position actually stands (synthesized, 2026-08-05)

None of the stacked-PR literature backs a hard rule that every PR needs its usage attached before it's reviewable. Orosz's own worked example and the Awesome Code Reviews guide both treat pure-layer splits (data model, then API, then UI) as unremarkable.

The narrower, defensible version of my argument: the rule should depend on how much judgment the earlier layer requires, not on layering itself.

- Low-judgment layers (scaffolding, boilerplate, generated code) don't need a witness. Nobody needs to see a migration used to review it.
- Interface-shaped layers (a new accessor's query shape, eager-loading choices, a new API contract) benefit from seeing the call site, because the reviewer is being asked to approve a shape whose correctness depends on how it gets consumed.

That's closer to a walking-skeleton/steel-thread argument repurposed for review order rather than build order — deliberately not something I found any existing writer making about stacked PRs specifically, so it should be presented as my own synthesis, not as a cited best practice.

The "horizontal vs. vertical" labels I used in the PR thread are inverted from the standard software-engineering usage (vertical slicing = cuts across all layers to deliver one complete unit; horizontal slicing = splits by layer, the usual anti-pattern name). Don't reuse those labels without fixing the direction — better to just describe the property directly ("does this PR require the reader to already understand a not-yet-written consumer").

## Sketch: a personal "Stacked PR review" skill

Goal: encode the synthesized position above so a future review (mine or an agent's) applies the same standard automatically instead of re-litigating it per PR.

What it would need to do, concretely:

1. **Detect a stack.** Given a PR, check whether its base branch is another open PR's head rather than the trunk branch (`gh pr view <n> --json baseRefName,headRefName` walked recursively, or `gh pr list --search "base:<branch>"`). Build the ordered chain.
2. **Classify each PR in the chain** as scaffolding/boilerplate, interface-shaped, or feature-complete-slice. This is a judgment call, not mechanical — a reasonable heuristic is "does this PR introduce a new function/endpoint/query signature with no caller in the diff," which flags interface-shaped PRs specifically.
3. **For interface-shaped PRs with no caller yet**, surface a prompt to also read the PR(s) further up the stack that call it, rather than reviewing in isolation, and note the interface decisions that are hard to evaluate without that context (shape of returned data, eager-loading, filter semantics).
4. **For scaffolding/boilerplate PRs**, skip that step — reviewing in isolation is fine and expected.
5. **Never assert the horizontal/vertical framing verbatim** — describe the property instead (see wording above) to avoid the terminology collision.
6. **Cite sources correctly if asked to justify the standard**: Orosz's scaffolding/logic/edge-cases split and the Awesome Code Reviews layered example as evidence that layer splits are normal, DePrato's "narrative" quote as the closest existing backing for wanting coherent read order, and walking skeleton / steel thread only as a self-attributed analogy, not an authority on stacked PRs.

Mechanically, in Claude Code this would be a project or personal skill (`.claude/skills/stacked-pr-review/SKILL.md`) with:

- A `description` tuned to trigger on "review this PR stack," "is this stack split right," or when a PR body/description references a parent PR.
- The chain-detection step as a documented `gh` recipe rather than freeform exploration, so it's fast and repeatable.
- The classification heuristic and the citation list embedded directly, so the skill doesn't re-derive the argument from scratch or drift to a different (possibly inverted) framing next time.

Worth prototyping narrowly first, on real stacks like
[#13710](https://github.com/coverbasedev/irm/pull/13710), [#13638](https://github.com/coverbasedev/irm/pull/13638), and
[watch-doggo#67](https://github.com/coverbasedev/watch-doggo/pull/67), before generalizing — those are the three examples already used as the "good stack" baseline in the #13678 thread.

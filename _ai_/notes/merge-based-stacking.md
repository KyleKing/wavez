# Merge-based stacked PR tooling

Notes on tools that automate the merge-forward pattern the `pr-stacking` skill
already documents by hand, so the direction mistake from 2026-08-06 (merging a
branch into its own base, which auto-closed two PRs and mis-retargeted a
third) can't happen again.

## Why merge, not rebase, for syncing a stack

GitHub's classic "Update branch" button on a PR performs a merge: it merges
the base branch into the head branch, creating a merge commit, and leaves
every existing commit's SHA untouched. That matters for review state:

- Commits a reviewer already marked "Viewed" keep the same SHA, so they stay
  checked off.
- Inline comments stay anchored to the lines they were left on.
- The PR's "changes since your last review" view only shows what's actually
  new, not the whole branch re-diffed.

Rebase-based syncing (the mechanism behind `git rebase --update-refs`,
Graphite, git-spice, ghstack, and GitHub's own newer "Stacked PRs" public
preview feature) replaces every commit's SHA on every sync. All of the above
review state is lost each time, and every branch above the rebased one needs
a force-push. That's the opposite of what we want for a stack under active
review, and it's why the `pr-stacking` skill bans rebasing/force-pushing a
branch other open PRs depend on.

GitHub's new native "Stacked PRs" (public preview as of mid-2026) is rebase
cascading, not merge — worth re-checking once it's GA in case a merge mode
gets added, but it doesn't fit this goal today.

## The actual bug from 2026-08-06

Manual `git merge <other-branch>` doesn't know which way is "up" the stack.
Merging the wrong direction (upper branch into its own base) makes the base a
superset of the branch above it, so GitHub reads the upper branch's PR as
satisfied and auto-merges/closes it, then auto-retargets anything based on
the upper branch onto the upper branch's own base. This happened twice in one
session because the fix each time was still a manual `git merge`, with no
tool enforcing direction. See the "Direction matters" section added to the
`pr-stacking` skill for the recovery recipe.

## Tools that bake in the direction, so it can't be gotten backwards

### git-town — `sync --stack` (recommended first try)

[git-town](https://www.git-town.com/) tracks each branch's parent explicitly
(set when you create the branch with `git town hack`/`append`, or via
`git town set-parent`). `git town sync --stack` walks every branch in the
current stack bottom-to-top and merges each parent into its child — the
default [`sync-feature-strategy` is `merge`](https://www.git-town.com/preferences/sync-feature-strategy),
not rebase. Because the tool reads the tracked parent/child edges rather than
whichever branch happens to be checked out, there's no way to invoke it
backwards the way a raw `git merge` allows.

This is compatible with the existing `pr-stacking` skill as-is: merging never
rewrites existing commits, so no force-push is needed for the sync itself,
only a normal `git push` afterward. It's the closest thing to "automate the
manual steps the skill already documents, minus the human error."

Discussion thread on exactly this use case:
[git-town/git-town#6031 "How to effectively merge deeply stacked changes?"](https://github.com/git-town/git-town/discussions/6031)

### GitHub API `update-branch`, scripted per PR

`gh api -X PUT repos/{owner}/{repo}/pulls/{n}/update-branch` triggers the same
merge the "Update branch" button does, over the API. A small script that
walks a stack's PRs bottom-up and calls this per PR gets the same
direction-safety as git-town, because the operation is defined as "update
this PR's head from its base" — there's no argument order that merges the
wrong way. Rougher than git-town (no local parent tracking, you supply the PR
order yourself), but needs no new dependency beyond `gh`.

### git-machete

[git-machete](https://git-machete.readthedocs.io/en/stable/#github) supports
a configurable sync method per branch (merge, rebase, or `rebase --onto`), so
it can also do merge-based stacking. Its `restack-pr` rebase helper is worth
knowing about separately: it temporarily converts PRs to draft during a
restack to avoid CODEOWNERS re-requesting reviewers when a rebase
momentarily includes a lower PR's commits — a real pain point called out in
the lazygit stacked-PR thread, though only relevant if a rebase-based flow
gets adopted later.

## LazyGit: no merge-based stack support today

Everything lazygit currently does for stacked branches is built on
`git rebase --update-refs` (native git 2.38+) — there is no cascading-merge
equivalent. The tracking issue is
[jesseduffield/lazygit#2527 "Stacked PRs in lazygit"](https://github.com/jesseduffield/lazygit/issues/2527),
open and active (19 comments, last activity July 2026). A comment on that
thread describing the confusing-relationship-order problem and the
diamond-dependency limitation (a branch with two dependants, which
`--update-refs` doesn't handle — already flagged by `OliverJAsh` in that
thread with no resolution) would be a useful, non-duplicate contribution.

## Recommendation

Try `git-town sync --stack` locally on the next multi-PR stack before
reaching for anything heavier. It matches the skill's existing merge-forward
convention exactly, needs no change to the "never force-push an open stack"
rule, and removes the specific human error (picking the wrong merge
direction) that caused today's incident.

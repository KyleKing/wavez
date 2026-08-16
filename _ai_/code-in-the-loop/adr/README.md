# Architecture decision records

Format is context, decision, consequences, and rejected alternatives. Each record states what
was given up, because a decision record that only lists benefits is an advertisement.

| # | Decision | Status |
| --- | --- | --- |
| [0001](0001-two-tier-memory.md) | Task memories and site knowledge are separate structures | Amended by 0010 |
| [0002](0002-deterministic-replay-agent-on-exception.md) | Replay deterministically, invoke the agent only on falsification | Amended by 0010 |
| [0003](0003-playwright-with-cdp-enforcement.md) | Playwright for actions, CDP and a proxy for enforcement | Amended by 0014 |
| [0004](0004-mutation-capability-deny-by-default.md) | Deny any request not provably incapable of mutating state | Accepted |
| [0005](0005-git-backed-versioned-memory.md) | Git and YAML as the memory substrate | Accepted |
| [0006](0006-falsifiable-step-expectations.md) | Every step carries confirming and falsifying evidence | Accepted |
| [0007](0007-quarantine-agent-authored-edits.md) | Agent-authored edits are quarantined, and may never weaken an expectation | Amended by 0012 |
| [0008](0008-page-content-is-data.md) | Page content is data, never instruction, and controls never depend on model judgment | Accepted |
| [0009](0009-prefer-webmcp-when-available.md) | Prefer site-declared WebMCP tools over induced sequences | Accepted, provisional |
| [0010](0010-compile-plans-not-record-steps.md) | Compile plans from goals and site knowledge, do not record and patch steps | Accepted |
| [0011](0011-perception-parity.md) | An observation contains only what a human could perceive | Accepted |
| [0012](0012-auto-review-asymmetry.md) | Models may veto, only checkers and humans may approve | Accepted |
| [0013](0013-model-tiering-local-for-untrusted.md) | Four tiers, and the tier that reads untrusted content runs locally | Accepted |
| [0014](0014-stack-selection.md) | TypeScript on Node, three innovation tokens | Accepted |

All records dated 2026-08-02.

Amendments are additive. An amended record keeps its reasoning and gains a note at the top
saying what changed and why, because the rejected alternatives in an amended record are usually
still rejected for the same reasons.

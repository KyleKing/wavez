# code-in-the-loop ADRs (condensed)

Browser automation where learned sequences run as deterministic code and a model is invoked only on divergence. Full project relocated to `~/Developer/kyleking/code-in-the-loop`. Decisions, one line each:

- ADR-0001: Task memories and site knowledge are separate structures. Two structures, partitioned by origin.
- ADR-0002: Replay deterministically, invoke the agent only on falsification. A task memory is executable.
- ADR-0003: Playwright for actions, CDP and a proxy for enforcement. Playwright drives actions.
- ADR-0004: Deny any request not provably incapable of mutating state. A request is admitted only when it can be shown incapable of mutating state.
- ADR-0005: Git and YAML as the memory substrate. Task memories and site knowledge are YAML files in a git repository.
- ADR-0006: Every step carries confirming and falsifying evidence. Each step carries a `confirm` set evaluated as a conjunction and a `falsify` set evaluated as a disjunction, both polled in parallel until one resolves or `timeout_ms` elapses.
- ADR-0007: Agent-authored edits are quarantined, and may never weaken an expectation. Agent-authored edits are written to a quarantine path and never execute.
- ADR-0008: Page content is data, never instruction, and controls never depend on model judgment. Page content is data.
- ADR-0009: Prefer site-declared WebMCP tools over induced sequences. Site knowledge records WebMCP availability per origin and refreshes it on every crawl.
- ADR-0010: Compile plans from goals and site knowledge, do not record and patch steps. A plan agent compiles a plan from a goal file plus site knowledge.
- ADR-0011: An observation contains only what a human could perceive. Observations are built by a filter that admits only content a human viewing the page could perceive.
- ADR-0012: Models may veto, only checkers and humans may approve. Approval comes from a deterministic checker or from a human.
- ADR-0013: Four tiers, and the tier that reads untrusted content runs locally. Four tiers, routed by capability need and by exposure to untrusted content.
- ADR-0014: TypeScript on Node, three innovation tokens. TypeScript on Node 22+, with Playwright for actions and a CDP session for the gate.

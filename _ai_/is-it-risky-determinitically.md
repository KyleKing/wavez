# Can we deterministically tell trivial from risky?

Research for Review, answering: "Can we write more general rules than
regex and directories/files that better and deterministically distinguish trivial
PRs from risky ones? Maybe we deploy to Stage and review logs?"

Short answer up front. The industry has no deterministic "is this diff safe"
oracle, and the one production system that truly skips human review (Meta's
RADAR) is a stack of deterministic scope-narrowing gates with an ML score and an
LLM as extra narrowing layers plus a rejection window. Two signals are genuinely
computable from a diff cheaply and deterministically, and neither is diff size:
a capability delta (did the change introduce a new dangerous kind of operation)
and a module-level blast radius (how much of the tree transitively imports what
changed). Both can only narrow risk, never prove safety, and that is exactly the
shape invariant 2 already demands. The stage-deploy-and-observe idea is not
workable as a pre-merge gate and the evidence for that is strong.

## 1. What the industry actually does

### Google: pattern-approval for mechanical changes only

Rosie shards large-scale changes and routes them to "global approvers" who "use
pattern-based tooling to review each of the changes and automatically approve
ones that meet their expectations"
([SWE-book ch. 22](https://abseil.io/resources/swe-book/html/ch22.html)). The
automation approves shards of one already-human-designed change, so one human
decision is amortized over thousands of commits. Ordinary changes always get a
human reviewer ([Sadowski et al., ICSE-SEIP 2018](https://sback.it/publications/icse2018seip.pdf)).
TAP's statistical models decide which tests to run, never whether to skip review
([Memon et al., ICSE-SEIP 2017](https://research.google.com/pubs/archive/45861.pdf)).

### Meta: the one real precedent for this bot's mission

Three distinct systems, often conflated:

- [Conveyor (OSDI 2023)](https://www.usenix.org/conference/osdi23/presentation/grubic)
  is post-merge deployment: 97% of container-service pipelines deploy with no
  manual step, and safety comes from healthchecks with a lookback window plus
  auto-revert, not from pre-merge scrutiny
- [Diff Risk Score (2025)](https://engineering.fb.com/2025/08/06/developer-tools/diff-risk-score-drs-ai-risk-aware-software-development-meta/)
  is a fine-tuned Llama predicting SEV probability from the diff plus metadata.
  Advisory: it routes reviewer attention, test selection, and release timing
- [RADAR (arXiv 2605.30208)](https://arxiv.org/abs/2605.30208) is the system
  that actually lands diffs "without any human review". Its funnel: deterministic
  eligibility gates (author role, not SOX-scoped, no blocklisted content), static
  heuristics, DRS percentile thresholds (P5 for human diffs, blanket accept only
  for deterministic codemods), an LLM reviewer requiring confidence >= 8/10 with
  every change in a safe category, deterministic validation, and a landing delay
  during which a human can still reject. Reported revert rate one third of
  normal diffs, incident rate 1/50th, on 331K+ landed diffs. Self-reported and
  confounded by selection (only easy diffs qualify), but it exists in production

RADAR is worth reading in full because it is Watch Doggo's design vindicated at
scale: deterministic gates own eligibility, the model layers can only narrow,
and an escape hatch (their landing delay, your human-clicks-merge rule) backstops
the whole thing. Notably, even Meta did not find a deterministic classifier that
replaces the ML and LLM layers. Nobody has.

### Dependency automation: deterministic policy plus crowd data

[Renovate Merge Confidence](https://docs.renovatebot.com/merge-confidence/)
badges are Age, Adoption (share of the Renovate fleet on the release), Passing
(fleet-wide CI pass rate for that update), and a combined Confidence level whose
algorithm Mend states is private and subject to change. Crowd telemetry, not
analysis of your code, and not reproducible from your side. Dependabot automerge
in the wild is a deterministic policy: CI green, patch or minor only, often
dev-dependencies only, enforced by branch protection
([GitHub docs](https://docs.github.com/en/code-security/tutorials/secure-your-dependencies/automate-dependabot-with-actions)).
Watch Doggo's Band B research pass is already stricter than the industry norm
here.

### Merge queues, SubmitQueue, auto-merge

All post-review machinery. GitHub
[auto-merge](https://docs.github.com/en/pull-requests/collaborating-with-pull-requests/incorporating-changes-from-a-pull-request/automatically-merging-a-pull-request)
fires only after required reviews pass.
[Merge queues](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/configuring-pull-request-merges/managing-a-merge-queue)
solve semantic merge conflicts by testing speculative merge commits. Uber's
[SubmitQueue](https://www.uber.com/ca/en/blog/bypassing-large-diffs-in-submitqueue/)
uses ML only to prioritize speculative builds; landing is deterministic. None of
these remove review.

### Defect prediction: an honest reading

Just-in-time defect prediction ([Kamei et al. 2012](https://dl.acm.org/doi/10.1145/3567550)
survey lineage) has a poor industrial record as a gate. Zimmermann's
cross-project study found only 3.4% of model transfers usable
([ESEC/FSE 2009](https://dl.acm.org/doi/10.1145/1595696.1595713)), and where
change-risk ML is deployed for real it routes attention and compute, never merge
authority: Mozilla's [bugbug](https://github.com/mozilla/bugbug) regressor flags
patches for extra scrutiny and its deployed win is
[test selection](https://hacks.mozilla.org/2020/07/testing-firefox-more-efficiently-with-machine-learning/),
Facebook's [predictive test selection](https://arxiv.org/abs/1810.05286)
likewise. No one gates merges on a classic JIT model. Do not build one.

### The cross-cutting pattern

Every system that removes the human pairs a narrow deterministic eligibility
class with an escape hatch (revert window, branch protection, human merge).
ML and LLMs appear only as additional narrowing layers inside that class. Watch
Doggo's two-gate design is already the industry shape. The gap is that its
eligibility class is defined only by paths and size, which is why it matches
nothing.

## 2. What is deterministically computable from a diff, and what is not

### Computable, cheaply

- Whether the diff introduces a syntactic match for a dangerous-capability
  pattern that the base ref did not have (see mechanism 1). Two snapshot scans
  plus a set difference: deterministic, seconds
- Which functions and classes the diff hunks fall inside, and whether a change
  touched a signature versus only a body (tree-sitter, milliseconds per file)
- The module-level reverse-dependency set of every changed file: which modules
  transitively import it. Python via [grimp](https://github.com/python-grimp/grimp)
  (`find_downstream_modules`, seconds), TypeScript via
  [dependency-cruiser](https://github.com/sverweij/dependency-cruiser) or
  ts-morph `findReferences`. This is the granularity Bazel (`rdeps()`,
  [query guide](https://bazel.build/query/guide)) and
  [Microsoft Test Impact Analysis](https://learn.microsoft.com/en-us/azure/devops/pipelines/test/test-impact-analysis)
  ship at scale, which is evidence the coarse version is the reliable version
- Whether changed modules are import-reachable from declared entrypoints (an
  approximation of "on the request path": reachable from the API server's entry
  module versus only from an offline script)
- Structural manifest diffs (already done in `manifest_risk.py`)
- Whether every added executable line sits inside a block guarded by a declared
  feature-flag call pattern (syntactic, approximate, fails closed)

### Not computable, ever or at reasonable cost

- Behavior preservation. Program equivalence is undecidable in general, and the
  practical versions (mutation testing, differential testing) cost minutes to
  hours per run. The deep pass's argued `behavior_preserved` boolean stays the
  best available stand-in
- Whether a one-line change is destructive in meaning. `retries=3` to
  `retries=0`, a flipped comparison, an off-by-one: syntactically identical to a
  trivial edit. No static rule distinguishes these. This is the hard floor under
  the whole question, and it is why every deterministic signal below is a
  necessary condition, never a sufficient one
- Python function-level call graphs. PyCG is archived at ~70% recall
  ([repo](https://github.com/vitsalis/PyCG)), pyan is superficial, duck typing
  and decorators defeat all of them. Accept module granularity for Python.
  TypeScript can do symbol level because the type system resolves dispatch
- Whether a capability arrived through a dependency bump rather than first-party
  code (a bumped package can add a network call your diff never shows;
  [Socket.dev](https://socket.dev/) covers this for the supply-chain side)

## 3. Ranked proposal

Ordered by value per unit of implementation cost. Every one is a Gate 1/Gate 2
narrowing input under invariant 2: each can deny or de-risk, none alone grants.

### 1. Capability delta: "no new dangerous kind of operation" (~2-4 days)

Detect that a diff introduces a NEW instance of a dangerous capability class:
subprocess/`child_process` spawn, `eval`/`exec`/`Function`/`importlib`, raw SQL
string construction, outbound network call (and any literal host in it), file
write, auth/permission decorator or check edit, new third-party import. Two
implementation routes, both deterministic:

- [Semgrep CE](https://semgrep.dev/) with `--baseline-commit $(git merge-base
  base head)`, which scans head and baseline and reports only new findings
  ([docs](https://semgrep.dev/docs/semgrep-ci/ci-environment-variables)). Write
  the ~15 rules in-house rather than using the registry, both to sidestep the
  [Semgrep Rules License](https://semgrep.dev/legal/rules-license/) question and
  to keep the rule set auditable in-repo
- Or [ast-grep](https://ast-grep.github.io/) run at base and head with a JSON
  finding diff, ~20 lines of glue, a single static binary, faster

Cost per PR: seconds. False-negative modes, stated plainly: aliased or dynamic
access (`getattr(os, "sys" + "tem")`), a capability added by a dependency bump,
modification of an *existing* dangerous call's arguments (mitigate by treating
any changed line within an existing sink's enclosing function as touching that
capability), string-built commands assembled across functions, and template or
config files that drive behavior without matching any code pattern. Because of
these, the signal's honest meaning is "no new capability *visible to syntax*",
which is still a real risk separator: the destructive one-liner that flips an
existing flag stays Band D, and this check never claims otherwise.

What it unlocks: a defensible split inside today's monolithic Band D. "Logic
change, zero capability delta, tests present, small core" is the population
where Band C could grow without the 13098-style trap, because the trap cases
(new JWKS fetch, new auth path) are precisely capability introductions. It also
hardens Band A/B: a capability delta in any band is an automatic deny, catching
the `docs/conf.py`-shaped case by behavior rather than by path.

### 2. Module-level blast radius (~3-5 days)

For each changed non-test file, compute the transitive importer count and list:
grimp for Python packages, dependency-cruiser for TypeScript, unioned at the
workspace level. Emit `{changed_module, downstream_count, downstream_services}`.
Threshold it (for example: downstream_count <= N and no cross-service reach) as
a Band C sub-gate, and use it to *compute* what `fanout_dirs` currently
declares by hand, keeping the declaration as the fail-closed fallback when the
graph build fails. Add the entrypoint-reachability variant ("is any changed
module import-reachable from the API entrypoint") as a second boolean once the
graph exists, since it is one query on the same graph.

Cost per PR: graph build is seconds for grimp, tens of seconds for
dependency-cruiser on a large monorepo; cache by base SHA. Limits: import graphs
overcount (importing is not calling) and undercount dynamic imports and
runtime-registered handlers (Celery tasks, route tables built from strings).
Overcounting is the safe direction for a deny-gate.

What it unlocks: replaces the bluntest rule in the system (any touch under
`common/` is Band D) with a measured count, which is the single biggest reason
16/16 PRs land in D on a monorepo where everything lives near `common/`. A
changed module with three downstream importers, all in one service, is a
different fact than one with three hundred, and today they classify identically.

### 3. Changed-symbol extraction with tree-sitter (~2-3 days, multiplier for 1 and 2)

Map diff hunks to enclosing function/method/class nodes at base and head, and
classify each change as body-only, signature, new symbol, or deleted symbol.
Python and TS/TSX grammars are mature
([tree-sitter](https://tree-sitter.github.io/tree-sitter/)); this is a few
hundred lines with pip-installable wheels and no build. It sharpens mechanism 1
(scan only changed functions, catch "modified an existing sink"), feeds
mechanism 2 real symbol names instead of file paths, and gives the deep pass a
structured "these 4 functions changed, 2 signatures" brief instead of a raw
diff. Signature changes are also a deterministic escalator on their own: a
body-only change with tests is a far better Band C candidate than an API-shape
change.

Skip the fancier alternatives: [GumTree](https://github.com/GumTreeDiff/gumtree)
is a JVM research toolchain with half-baked TS support,
[difftastic](https://github.com/Wilfred/difftastic) is explicitly
human-display-only with unstable JSON, and CodeQL's database build busts the
per-PR budget and its
[license](https://github.com/github/codeql-action) requires GitHub Advanced
Security for private repos.

### 4. Merge-then-monitor: the workable version of the stage idea (~1 week, reuses existing scan)

The owner's instinct that runtime evidence beats static guessing is right; the
industry's answer is to collect it after the merge, with auto-revert as the
safety, rather than before approval. Meta's Conveyor
([OSDI 2023](https://www.usenix.org/conference/osdi23/presentation/grubic)),
Slack's ReleaseBot
([Deploy Safety](https://slack.engineering/deploy-safety/),
[The Scary Thing About Automating Deploys](https://slack.engineering/the-scary-thing-about-automating-deploys/)),
and Uber's [Micro Deploy](https://www.uber.com/us/en/blog/micro-deploy-code/)
all merge fast and watch production with automatic rollback.

Watch Doggo already has the skeleton: the monitored-merges scan lists PRs whose
only approval was the App's. Extend it to join each such merge against Sentry
new-issue counts and the deployed service's health/error metrics for a lookback
window, and post the result to the same Slack channel (clean after 24h, or
flagged with the evidence). This is the revert-and-incident data the
growth-ladder already says it needs, collected automatically instead of by hand,
and it needs read-only observability credentials in a *scheduled scan*, not in
the PR-triggered job that is actively shedding credentials. It converts every
bot-approved merge into a labeled outcome, which is the only way the Band C
unlock decision ever gets evidence behind it.

### 5. Band B enrichment with ecosystem data (~1-2 days, minor)

For dependency bumps, fetch [deps.dev](https://deps.dev/) and OSV advisory data,
and Renovate's public Merge Confidence badge values where available, as inputs
to the research deep pass rather than as gates (the confidence algorithm is
private, so it cannot be a deterministic gate on your side). Cheap, and it gives
the Band B research pass structured facts to anchor on. Lowest priority because
Band B already has the strongest sub-gate in the system.

### Sequencing note

Do 1 and 3 together (3 makes 1 materially better), then 2, then 4. Before
widening anything, re-run the 60-PR eval seed with the new signals recorded per
PR: the claim that capability delta plus blast radius cleanly separates the 16
D-banded PRs is testable against data already collected, and it should be tested
before any gate consumes the signals.

## 4. What will not work, and why

**Deploy to stage, read logs, exercise it, then approve.** Not workable as a
pre-merge gate, on four independent grounds. Statistics: Spinnaker's own
[canary best practices](https://spinnaker.io/docs/guides/user/canary/best-practices/)
recommend a 3-hour canary with at least 50 data points per metric per run, and a
near-zero-traffic stage environment can never produce that; all a quiet stage
detects is crash-on-boot, import errors, and failed migrations, which a
deterministic smoke test in CI catches in a fraction of the time with no new
credentials. Latency: it turns a ~10-minute review into a 30-minute-plus one.
Serialization: one shared stage environment queues every review behind a single
mutable deployment. Credentials: it hands deploy rights and log-read access to
the job processing untrusted PR content, reversing ROADMAP 0b, and log contents
become a new prompt-injection surface. Every production deploy-and-observe
system (Kayenta, [Argo Rollouts](https://argo-rollouts.readthedocs.io/),
Flagger, Conveyor) runs post-merge on real traffic with auto-revert. The
pre-merge cousins that do exist (preview environments, GitHub
[deployment protection rules](https://docs.github.com/en/actions/how-tos/deploy/configure-and-manage-deployments/control-deployments))
gate on deterministic test assertions or human clicks, never on open-ended log
reading. Mechanism 4 is the salvageable core of this idea.

**An ML defect-prediction gate.** Cross-project models do not transfer
([Zimmermann et al.](https://dl.acm.org/doi/10.1145/1595696.1595713)), a
single-repo model needs labeled defect history this project does not have, and
the deployed successes (bugbug, Facebook test selection, Meta DRS) all route
attention rather than grant approval. Even RADAR uses its risk score only as one
narrowing layer under deterministic gates.

**Python function-level call graphs for blast radius.** The tooling is not
there: PyCG archived at ~70% recall, pyan unreliable, and dynamic dispatch is
structural, not a maturity gap. Module-level import graphs are what everyone
actually ships. TypeScript symbol-level via ts-morph is real, but do the
module-level version first for symmetry.

**A general "capability inference" engine.** Google's
[Capslock](https://github.com/google/capslock) exists only because Go's static
types make transitive capability analysis tractable; nothing equivalent exists
for Python or JS, and building one is a research program. Syntactic sink
matching (mechanism 1) is the honest achievable version.

**Believing any of it proves safety.** The capability delta and blast radius
signals have irreducible false negatives (the destructive one-liner, the
dependency-mediated capability, the string-built command), so they can move a PR
*down* the escalation ladder only in conjunction with tests, the deep pass's
behavior-preservation argument, and the human who clicks merge. The evidence
from RADAR is that this layered shape works; the evidence from everywhere else
is that no single deterministic rule ever will.

## Sources

Industry: [SWE-book ch. 22 (LSCs)](https://abseil.io/resources/swe-book/html/ch22.html),
[Sadowski et al., Modern Code Review at Google](https://sback.it/publications/icse2018seip.pdf),
[Memon et al., Taming Google-Scale Continuous Testing](https://research.google.com/pubs/archive/45861.pdf),
[Conveyor, OSDI 2023](https://www.usenix.org/conference/osdi23/presentation/grubic),
[Meta Diff Risk Score](https://engineering.fb.com/2025/08/06/developer-tools/diff-risk-score-drs-ai-risk-aware-software-development-meta/),
[RADAR](https://arxiv.org/abs/2605.30208),
[SapFix](https://engineering.fb.com/2018/09/13/developer-tools/finding-and-fixing-software-bugs-automatically-with-sapfix-and-sapienz/),
[Renovate Merge Confidence](https://docs.renovatebot.com/merge-confidence/),
[Dependabot automation](https://docs.github.com/en/code-security/tutorials/secure-your-dependencies/automate-dependabot-with-actions),
[GitHub merge queue](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/configuring-pull-request-merges/managing-a-merge-queue),
[Uber SubmitQueue](https://www.uber.com/ca/en/blog/bypassing-large-diffs-in-submitqueue/),
[Slack ReleaseBot](https://slack.engineering/the-scary-thing-about-automating-deploys/),
[Uber Micro Deploy](https://www.uber.com/us/en/blog/micro-deploy-code/).

Defect prediction: [JIT survey, ACM CSUR](https://dl.acm.org/doi/10.1145/3567550),
[Zimmermann et al., cross-project](https://dl.acm.org/doi/10.1145/1595696.1595713),
[bugbug](https://github.com/mozilla/bugbug),
[Mozilla test selection](https://hacks.mozilla.org/2020/07/testing-firefox-more-efficiently-with-machine-learning/),
[Facebook predictive test selection](https://arxiv.org/abs/1810.05286).

Tools: [Semgrep](https://semgrep.dev/),
[Semgrep diff-aware scanning](https://semgrep.dev/docs/semgrep-ci/ci-environment-variables),
[Semgrep Rules License](https://semgrep.dev/legal/rules-license/),
[ast-grep](https://ast-grep.github.io/),
[tree-sitter](https://tree-sitter.github.io/tree-sitter/),
[grimp](https://github.com/python-grimp/grimp),
[dependency-cruiser](https://github.com/sverweij/dependency-cruiser),
[ts-morph](https://ts-morph.com/),
[PyCG](https://github.com/vitsalis/PyCG),
[GumTree](https://github.com/GumTreeDiff/gumtree),
[difftastic tree diffing](https://difftastic.wilfred.me.uk/tree_diffing.html),
[CodeQL action licensing](https://github.com/github/codeql-action),
[SCIP](https://github.com/sourcegraph/scip),
[Glean](https://engineering.fb.com/2024/12/19/developer-tools/glean-open-source-code-indexing/),
[Capslock](https://github.com/google/capslock),
[Socket.dev](https://socket.dev/),
[Bazel rdeps](https://bazel.build/query/guide),
[Microsoft Test Impact Analysis](https://learn.microsoft.com/en-us/azure/devops/pipelines/test/test-impact-analysis).

Canary/observe: [Kayenta announcement](https://netflixtechblog.com/automated-canary-analysis-at-netflix-with-kayenta-3260bc7acc69),
[Spinnaker canary best practices](https://spinnaker.io/docs/guides/user/canary/best-practices/),
[Spinnaker judge](https://spinnaker.io/docs/guides/user/canary/judge/),
[Argo Rollouts](https://argo-rollouts.readthedocs.io/),
[SRE error budgets](https://sre.google/workbook/error-budget-policy/),
[GitHub deployment protection rules](https://docs.github.com/en/actions/how-tos/deploy/configure-and-manage-deployments/control-deployments),
[sequential testing for regression detection](https://arxiv.org/pdf/2205.14762).

Caveats on the evidence: RADAR is a single self-reported arXiv paper from Meta
authors with selection-confounded outcome numbers. The Renovate confidence
algorithm is private, so its badges cannot be audited. No Netflix source on
review-free merging was found despite searching; claims to the contrary are
unsourced. The Semgrep registry-rules license changed in December 2024 and is
worth re-reading before shipping registry rules in a product.

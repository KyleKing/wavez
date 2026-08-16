# Plan compilation

## What was wrong with the first version

The original task memory was a recorded step list, and healing meant patching steps. That
makes the recording the source of truth, which is backwards. A recording is a fact about one
past execution, and treating it as the specification means every site change is a change to
the specification.

It also handles forms badly, which is most of what anyone wants automated. A form is
conditional (fields appear based on earlier answers), multi-stage, validated server-side, and
frequently reordered between releases. "Fill field three with X" is wrong the moment a field
is inserted.

## The restructure

A **plan agent** compiles a plan from a goal and site knowledge. The compiled plan is cached
as a task memory. The cache is disposable.

```
goal + input schema + site knowledge  ──compile──▶  plan (steps + expectations)
                                                      │
                                                      ├─ cached as task memory
                                                      └─ executed by the runner
```

The source of truth is the goal statement plus site knowledge. The task memory is a build
artifact. On falsification the first move is **recompile**, not patch, because site knowledge
has usually already been updated by the crawler or by another task's run, and recompiling
against current knowledge produces a correct plan without anyone reasoning about a diff.

This is why the scraper matters more than it first appeared. A general-purpose scraper that
keeps site knowledge accurate makes the plan agent cheap, and it means most healing happens
without an agent seeing the failure at all. The crawler noticed the change on Tuesday, and
Thursday's run compiles correctly.

### Task memory becomes two files

```yaml
# tasks/submit-expense-report.goal.yaml     ← source, human-authored or human-reviewed
schema: taskgoal/v1
id: submit-expense-report
origin: https://acme.example
goal: >
  Submit a completed expense report for the given period and confirm it reached
  the approver queue.
inputs:
  - {name: period, type: string, pattern: '^\d{4}-Q[1-4]$'}
  - {name: receipts, type: file[]}
write_intents:
  - {origin: 'https://acme.example', operation_class: expense.create, max_count: 1}
  - {origin: 'https://acme.example', operation_class: expense.attach, max_count: 20}
acceptance:
  - {kind: element_text, match: {element: expenses.status-chip, equals: 'Submitted'}}
```

```yaml
# tasks/submit-expense-report.plan.yaml      ← compiled, disposable, regenerable
schema: taskplan/v1
compiled_from: {goal: submit-expense-report, site_knowledge: sha256:8f2c…, at: 2026-08-01T09:02:00Z}
steps: [ … as before … ]
```

The goal file carries everything with security weight: the write intents, the acceptance
criteria, and the input schema. It is small enough to review properly and it changes rarely.
The plan file is large, churns, and carries no authority of its own, since it is only valid
against the site knowledge hash it was compiled from.

This resolves an awkwardness in the earlier design. A reviewer was being asked to read a
sixty-line step list to approve a two-line security contract, which is how review becomes a
rubber stamp. Now the contract is its own file and the step list is generated output.

### Invalidation

A plan is stale when the site-knowledge hash it references no longer matches for the routes
it touches. Staleness is not failure. A stale plan still runs, and staleness only lowers the
threshold for recompiling instead of repairing when something does go wrong.

Recompilation is cheap relative to a repair session because the plan agent works from
structured site knowledge rather than from a live page, so it needs no browser and no
untrusted content in context.

## Forms, specifically

Form steps are not recorded field-by-field. A form step names the form and a **binding** from
the task's input schema to field semantics:

```yaml
- id: fill-expense-form
  intent: Complete the expense form
  action:
    kind: fill_form
    form: expenses.form-root
    bindings:
      - {input: period, field_semantics: {label_matches: 'period|reporting period', type: text}}
      - {input: receipts, field_semantics: {label_matches: 'receipt|attachment', type: file}}
    unbound_required_fields: escalate
  expect:
    confirm:
      - {kind: form_valid, match: {form: expenses.form-root}}
    falsify:
      - {kind: element_visible, match: {element: expenses.field-validation-error}, attribute: execution}
```

Resolution happens at runtime against the live form's accessibility tree. Fields are matched
by accessible label, input type, and required-ness. A required field with no binding is an
explicit outcome (`unbound_required_fields`) rather than a silent skip, which is the failure
mode that makes recorded form-filling dangerous, since a new required field gets a default
value nobody chose.

Conditional fields work because resolution repeats after each field is set and the form
re-renders. Multi-stage wizards are multiple `fill_form` steps with a navigation between
them, and the binding for a stage that has not appeared yet simply finds nothing to bind and
the step's expectations decide whether that is correct.

This is the WALT lesson applied to input rather than to actions. Express the step in terms of
the site's own semantics (this form, these fields, these meanings) rather than in terms of one
observed traversal, and it survives the redesign that reorders the fields.

## Recompile, repair, or retire

The falsification scope from [ADR-0006](../adr/0006-falsifiable-step-expectations.md) now maps
onto three responses rather than two, and the new one is the cheapest.

| Scope | Response | Model cost |
| --- | --- | --- |
| `precondition` | Re-establish preconditions from a site-knowledge flow | None, or one cheap call |
| `execution` | Retry, then locator fallback, then recompile | None on the fast path |
| `skill` | Recompile from current site knowledge. If the recompiled plan differs at the failing step, run it | One plan-agent call, no page content in context |
| `skill`, recompile produced the same plan | Site knowledge is also stale. Targeted recrawl of the route, then recompile | One crawl, then one plan-agent call |
| `planning` | The goal is no longer achievable on this site. Halt and flag for human review | None |

The repair agent that reads the live page is now the *fourth* thing tried rather than the
first, and it is reached only when a targeted recrawl plus recompilation failed. That is a
large reduction in how often untrusted page content reaches a model at all, and it falls out
of the restructure rather than being a security feature bolted on.

## What this costs

Compilation is a model call that the recorded-steps design did not need, paid on first run
and on every recompile. It is cheap (structured input, no browser, no page content) and it is
not free.

The plan agent can compile a wrong plan from correct site knowledge, and it will do so
confidently. The acceptance criteria in the goal file are the backstop, and they are
human-authored for exactly this reason.

Site knowledge becomes load-bearing in a way it was not. A plan is only as good as the
knowledge it compiled from, so crawler quality now determines task success rather than merely
escalation cost. That is a real coupling and it is the right one, because it concentrates
maintenance in the artifact that one crawler maintains for every task on the origin.

Determinism is weaker. Two compilations from the same inputs should produce the same plan and
will not always. Pinning the compiled plan and treating recompilation as an explicit event,
journaled and diffable, keeps this observable rather than mysterious.

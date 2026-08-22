# progress-estimate

Spike for the progress line in [DESIGN.md](../../../DESIGN.md) ("Thread view",
"Progress is for the human"): how well does a thread's own history, and the
project's history for the same shape of work, predict the wall clock a run
has left? It replays thread logs already on disk and needs no model.

## Run

```
go run . [-min-turns 2] [-whole-thread] <dir>...   # each dir is a project's .wavez/threads
```

A run is one user prompt and the work it caused: it starts at a user event
and ends at the last event before the next one, so the minutes a thread
spends waiting for its human are not counted as work a progress line could
predict. `-whole-thread` scores a thread as one run, which is what the first
pass did.

A turn boundary is an agent note carrying usage, which is where one model
call ended. The run ends at its last event whatever state it reached, so an
aborted run counts as a short one. Scoring is leave-one-run-out: every
boundary of every run is predicted with the other runs as history, and the
tool prints mean and median absolute error in seconds and how often the
estimate landed within a factor of two of the truth.

Estimators, from no history to the most:

| Estimator | Uses |
|---|---|
| elapsed doubles | nothing: remaining = elapsed |
| own mean turn x 3 | this thread's mean turn so far, times three more turns |
| history median total | the median total wall of every other run, less elapsed |
| history conditional median | the median total among runs that outlived this elapsed, less elapsed |
| same shape conditional | the same, over runs that did or did not edit like this one |

## Numbers

2026-08-22, M2 Pro, 138 thread logs, 108 runs and 836 turn boundaries. This
is the corpus the first pass waited for, and it decides the question.

| Estimator | MAE (s) | median (s) | within 2x |
|---|---|---|---|
| elapsed doubles | 221.4 | 71.3 | 32% |
| own mean turn x 3 | 206.6 | 53.9 | 23% |
| history median total | 210.6 | 51.7 | 20% |
| history conditional median | 1511.4 | 62.0 | 34% |
| same shape conditional | 430.6 | 71.4 | 30% |

Nothing here earns a store. The two estimators that read no history at all
are as good as the three that do, and no estimator lands within a factor of
two more than a third of the time, so a countdown built on any of them is
wrong twice for every time it is right. The conditional estimators buy their
within-2x with a long tail: taking the median of only the runs that have
already outlived this one predicts an hour whenever the few that did were
long, which is where the 1511 s comes from.

The turn is a different question and a much easier one:

| Question | MAE (s) | median (s) | within 2x |
|---|---|---|---|
| remaining run, best estimator | 206.6 | 53.9 | 23% |
| next turn, from this run's mean gap so far | 11.3 | 4.9 | 54% |

So the progress line shows the turn, not the run: how long this turn has
been going against what this run's turns have been costing, which needs no
history and no store. Whether the run has ten seconds or ten minutes left is
not predictable from anything on disk here.

The first pass ran on the M4 Pro over six whole threads and decided nothing
(within-2x near a coin flip for every estimator). It also counted human
think time as run time, which is why its MAE was 3x this one's: on the same
138 logs, `-whole-thread` reports MAE 694.7 against 221.4 for the same
estimator.

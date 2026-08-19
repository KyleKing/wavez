# progress-estimate

Spike for the progress line in [DESIGN.md](../../../DESIGN.md) ("Thread view",
"Progress is for the human"): how well does a thread's own history, and the
project's history for the same shape of work, predict the wall clock a run
has left? It replays thread logs already on disk and needs no model.

## Run

```
go run . [-min-turns 2] <dir>...    # each dir is a project's .wavez/threads
```

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

2026-08-18, M4 Pro, the six runs with two or more turns that this laptop had
on disk (all `wavez -p` timing-harness runs of the same four tasks, so the
history is nearly the run itself):

| Estimator | MAE (s) | median (s) | within 2x |
|---|---|---|---|
| elapsed doubles | 33.9 | 30.0 | 32% |
| own mean turn x 3 | 25.1 | 18.5 | 29% |
| history median total | 27.3 | 14.1 | 27% |
| history conditional median | 28.3 | 19.8 | 34% |
| same shape conditional | 44.1 | 53.8 | 11% |

Six runs decide nothing: within-2x hovers near a coin flip for every
estimator and the same-shape split has one or two runs per side. The
corpus that would settle it is the fifty-eight logs in the M2 Pro's
`.wavez/threads` (`_ai_/bench/dogfood.md`, the VHS tour entry), which this
laptop does not have. Run it there before deciding whether the project's
history is worth storing; if "elapsed doubles" and "own mean turn" stay
within a few seconds of the history estimators on that corpus, the
progress line needs no store at all.

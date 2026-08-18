# Timing harness

Runs one task through one runner in a scratch jj-colocated copy of this repo
and appends a JSON line to `results.jsonl`: wall seconds, exit code, changed
files, and whether `go build`, `go vet`, and the touched packages' tests pass
afterwards. The diff each run left is kept beside it for reading.

Setup (once):

```sh
B=/tmp/wz-bench; rm -rf $B; mkdir -p $B
rsync -a --exclude .git --exclude .jj --exclude .wavez --exclude .codegraph ./ $B/repo/
(cd $B/repo && git init -q && git add -A && git commit -qm init && jj git init --colocate && jj commit -m init)
go build -o $B/wavez ./cmd/wavez
cp _ai_/bench/timing/tasks.txt $B/
```

Then, on a machine with nothing else running (a coverage-map build or another
agent's `go test` skews every number):

```sh
for r in wavez-local wavez-hosted claude; do for t in q1 e1 e2 e3; do _ai_/bench/timing/run.sh $r $t; done; done
```

`wavez-hosted` caps spend at $0.50 per run. `claude` is `claude -p` with its
default model and permissions skipped, so it reads this repo's CLAUDE.md the
way it would for a user.

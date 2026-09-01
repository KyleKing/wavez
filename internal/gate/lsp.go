package gate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kyleking/wavez/internal/lsp"
	"github.com/kyleking/wavez/internal/tool"
)

// DefaultLSPTimeout bounds how long one gate run waits for a server to
// publish diagnostics for the files it just synced. Measured against gopls
// v0.23 on this repo, a cold server answers in 0.76 s and a warm one in
// 0.02 s, so this leaves room for a project several times larger without
// making a hung server cost the whole run.
const DefaultLSPTimeout = 5 * time.Second

// maxLSPFrames bounds how many diagnostics one file contributes, matching
// the cap the convention gate puts on rule violations.
const maxLSPFrames = 20

const lspGateName = "lsp"

// LSPGate feeds a language server's diagnostics back after an edit, which
// DESIGN.md's Gates section lists among M1's table stakes. Only errors reach
// the model: warnings and hints are what the formatter and linter pre-passes
// already own, and a type error is the one class of finding that makes the
// change not build.
type LSPGate struct {
	pool     *lsp.Pool
	repoRoot string
	timeout  time.Duration
}

// LSPOption configures an LSPGate.
type LSPOption func(*LSPGate)

// WithLSPTimeout overrides how long one run waits for diagnostics.
func WithLSPTimeout(d time.Duration) LSPOption {
	return func(g *LSPGate) { g.timeout = d }
}

// NewLSPGate builds a gate over pool. The pool outlives the gate and is the
// caller's to close: its whole point is one server process per project reused
// across runs.
func NewLSPGate(repoRoot string, pool *lsp.Pool, opts ...LSPOption) *LSPGate {
	g := &LSPGate{repoRoot: repoRoot, pool: pool, timeout: DefaultLSPTimeout}
	for _, opt := range opts {
		opt(g)
	}

	return g
}

// Name identifies this gate in the gate log.
func (*LSPGate) Name() string { return lspGateName }

// Resources reports no exclusive resource: the gate only reads the working
// tree and talks to its own server process.
func (*LSPGate) Resources() []string { return nil }

// Run syncs every changed file its pool handles and reports the errors the
// server publishes for those files. Every outcome, including a server that
// never answers, is carried by Result rather than by an error.
//
//nolint:unparam // the error return is the Gate interface's, not this gate's
func (g *LSPGate) Run(ctx context.Context, rc RunContext) (Result, error) {
	files := g.handledFiles(rc.Changes)
	if len(files) == 0 {
		return Abstained(g.Name(), rc.Selection.Level,
			"no changed file is one a configured language server handles"), nil
	}

	byClient := make(map[*lsp.Client][]string, 1)
	order := make([]*lsp.Client, 0, 1)

	for _, f := range files {
		client, err := g.pool.Client(ctx, f)

		switch {
		case errors.Is(err, lsp.ErrServerUnavailable):
			// Not a pass, because nothing was checked, and not a failure the
			// model sees, because installing a language server is not work a
			// run can do. The reason is what keeps it auditable in the log.
			return Result{
				Gate:   g.Name(),
				Level:  rc.Selection.Level,
				Reason: fmt.Sprintf("%v, so %d changed file(s) went unchecked", err, len(files)),
			}, nil
		case err != nil:
			return ExaminedNothing(g.Name(), rc.Selection.Level, fmt.Sprintf(
				"the language server did not start on this project: %v", err,
			)), nil
		}

		if _, ok := byClient[client]; !ok {
			order = append(order, client)
		}

		byClient[client] = append(byClient[client], f)
	}

	results := make([]Result, 0, len(order))

	for _, client := range order {
		group := byClient[client]

		versions, err := syncAll(ctx, client, group)
		if err != nil {
			return ExaminedNothing(g.Name(), rc.Selection.Level, err.Error()), nil
		}

		results = append(results, g.collect(ctx, rc, client, group, versions))
	}

	return mergeLSPResults(rc.Selection.Level, results), nil
}

// mergeLSPResults folds one result per language into the gate's single
// outcome, keeping every failure and refusing to pass while any language's
// server left files unexamined.
func mergeLSPResults(level Level, results []Result) Result {
	merged := Result{Gate: lspGateName, Level: level, Pass: true}

	for i := range results {
		r := &results[i]
		merged.Examined += r.Examined
		merged.Failures = append(merged.Failures, r.Failures...)

		if r.Reason != "" {
			merged.Reason = strings.TrimSpace(merged.Reason + " " + r.Reason)
		}

		if !r.Pass {
			merged.Pass = false
		}
	}

	return merged
}

// collect waits for each synced file's diagnostics under one deadline and
// turns the errors among them into failures.
func (g *LSPGate) collect(
	ctx context.Context, rc RunContext, client *lsp.Client, files []string, versions map[string]int,
) Result {
	waitCtx, cancel := context.WithTimeout(ctx, g.timeout)
	defer cancel()

	var (
		failures []TrimmedFailure
		silent   []string
		examined int
	)

	for _, f := range files {
		diags, err := client.Diagnostics(waitCtx, f, versions[f])
		if err != nil {
			silent = append(silent, f)

			continue
		}

		examined++

		if failure, ok := errorFailure(f, diags); ok {
			failures = append(failures, failure)
		}
	}

	// Reporting the errors found beats reporting the wait that timed out:
	// the diagnostics are true and actionable, and a file that never answered
	// only matters when the run would otherwise be recorded green.
	if len(failures) > 0 {
		return Result{Gate: g.Name(), Level: rc.Selection.Level, Examined: examined, Failures: failures}
	}

	if len(silent) > 0 {
		return ExaminedNothing(g.Name(), rc.Selection.Level, fmt.Sprintf(
			"the language server published no diagnostics for %s within %s",
			strings.Join(silent, ", "), g.timeout,
		))
	}

	return Result{Gate: g.Name(), Level: rc.Selection.Level, Examined: examined, Pass: true}
}

// handledFiles is the changed files a configured server claims, deduplicated,
// ordered, and restricted to paths that still exist: a deleted file cannot be
// opened, and a server has nothing to say about one.
func (g *LSPGate) handledFiles(changes []tool.Change) []string {
	seen := make(map[string]struct{}, len(changes))
	out := make([]string, 0, len(changes))

	for _, c := range changes {
		if c.Path == "" || !g.pool.Handles(c.Path) {
			continue
		}

		if _, ok := seen[c.Path]; ok {
			continue
		}

		seen[c.Path] = struct{}{}

		path := c.Path
		if !filepath.IsAbs(path) {
			path = filepath.Join(g.repoRoot, path)
		}

		if _, err := os.Stat(path); err != nil {
			continue
		}

		out = append(out, c.Path)
	}

	sort.Strings(out)

	return out
}

func syncAll(ctx context.Context, client *lsp.Client, files []string) (map[string]int, error) {
	versions := make(map[string]int, len(files))

	for _, f := range files {
		version, err := client.Sync(ctx, f)
		if err != nil {
			return nil, fmt.Errorf("the language server rejected %s: %w", f, err)
		}

		versions[f] = version
	}

	return versions, nil
}

// errorFailure keeps only the diagnostics at error severity, which is the
// floor this gate reports on: gopls raises a hint on every unused parameter
// and a warning on every shadowed identifier, and those belong to the linter
// pre-pass rather than to a gate whose failure blocks a run.
func errorFailure(file string, diags []lsp.Diagnostic) (TrimmedFailure, bool) {
	frames := make([]string, 0, len(diags))

	for _, d := range diags {
		if d.Severity != lsp.SeverityError {
			continue
		}

		frames = append(frames, d.String())
	}

	if len(frames) == 0 {
		return TrimmedFailure{}, false
	}

	if len(frames) > maxLSPFrames {
		dropped := len(frames) - maxLSPFrames
		frames = append(frames[:maxLSPFrames:maxLSPFrames],
			fmt.Sprintf("... [%d more diagnostics in %s] ...", dropped, file))
	}

	return TrimmedFailure{Test: file, Frames: frames}, true
}

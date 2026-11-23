package gate

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/kyleking/wavez/internal/config"
)

// Option adjusts the Runner New returns.
type Option func(*runner)

// WithBudget sets how long one stop-stage run may take in all, which is
// DefaultStopBudget otherwise. The time a Go test binary and a Go command
// get are fixed shares of it. A budget of zero or less is ignored.
func WithBudget(budget time.Duration) Option {
	return func(r *runner) {
		if budget > 0 {
			r.budget = budget
		}
	}
}

// New returns the Runner that loads the project's configuration from root on
// each run.
//
//nolint:ireturn // Runner is the frozen surface internal/hook and cmd/wavez consume
func New(opts ...Option) Runner {
	r := &runner{lookPath: exec.LookPath, noCache: cacheDisabled(), budget: DefaultStopBudget}
	for _, opt := range opts {
		opt(r)
	}

	return r
}

type runner struct {
	lookPath func(name string) (string, error)
	budget   time.Duration
	noCache  bool
}

func (r *runner) cacheFor(root string) cache {
	return cache{root: root, off: r.noCache}
}

// project is one run's fixed inputs: where it runs, what the project
// configured, and what changed.
type project struct {
	root    string
	changed []string
	cfg     config.Config
}

// load reads root's configuration into a project. A root without a go.mod
// has no Go built-ins.
func load(root string, changed []string) (project, error) {
	root, err := resolveRoot(root)
	if err != nil {
		return project{}, err
	}

	cfg, err := config.Load(root)
	if err != nil {
		return project{}, fmt.Errorf("loading configuration: %w", err)
	}

	proj := project{root: root, changed: changed, cfg: cfg}
	if !isFile(filepath.Join(root, "go.mod")) {
		proj.cfg.Disabled = config.Builtins()
	}

	return proj, nil
}

// Run loads root's configuration and runs the checks changed selects for
// stage. Fixers run first and one at a time, since two of them may rewrite
// the same file, and the Go formatter is one of them at both stages: a file
// a shell command wrote never passed through the edit stage. The checks
// then run side by side and report in a fixed order: the built-ins, then
// the project's own in file order. A blocking failure at the stop stage is
// then compared with the baseline the context carries, if it carries one.
// A stop-stage run ends inside the runner's budget, and what had not
// answered by then abstains. The error is for a configuration or root that
// cannot be read, never for a check.
func (r *runner) Run(ctx context.Context, root string, changed []string, stage Stage) (Report, error) {
	changed = normalize(changed)
	if len(changed) == 0 {
		return Report{}, nil
	}

	proj, err := load(root, changed)
	if err != nil {
		return Report{}, err
	}

	if stage == StageStop {
		var cancel context.CancelFunc

		ctx, cancel = context.WithTimeout(ctx, r.budget)
		defer cancel()
	}

	results := r.format(ctx, proj, stage)

	checks := selectChecks(proj, stage)
	for i := range checks {
		checks[i].fixed = r.fix(ctx, proj.root, checks[i])
	}

	slots := make([][]Result, len(checks)+1)

	var wg sync.WaitGroup

	if stage == StageStop {
		wg.Go(func() { slots[0] = r.goStop(ctx, proj) })
	}

	for i := range checks {
		wg.Go(func() { slots[i+1] = []Result{r.command(ctx, proj.root, checks[i])} })
	}

	wg.Wait()

	results = append(results, slices.Concat(slots...)...)

	// Only a stop can block, and only a session has a tree it started from.
	if base, ok := baselineFrom(ctx); ok && stage == StageStop {
		results = r.compare(ctx, proj, base, results)
	}

	return Report{Results: results}, nil
}

// Checks names what a stop in the project at root would run: the Go
// built-ins that are on, in the order they report, and then the names of
// the project's own stop-stage checks. Lint counts only where the project configured a
// linter, since without a configuration it never runs.
func Checks(root string) ([]string, []string, error) {
	proj, err := load(root, nil)
	if err != nil {
		return nil, nil, err
	}

	var builtins, custom []string

	for _, name := range config.Builtins() {
		if proj.cfg.Enabled(name) && (name != config.BuiltinLint || lintConfigured(proj.root)) {
			builtins = append(builtins, name)
		}
	}

	for _, check := range proj.cfg.Checks {
		if Stage(check.Stage) == StageStop {
			custom = append(custom, check.Name)
		}
	}

	return builtins, custom, nil
}

// resolveRoot returns root absolute with symlinks followed, which is the
// form go and git report their own paths in.
func resolveRoot(root string) (string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolving root %s: %w", root, err)
	}

	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolving root %s: %w", root, err)
	}

	return resolved, nil
}

// normalize cleans the changed paths to slash-separated relative form, once
// each, dropping any that climb out of the root.
func normalize(changed []string) []string {
	out := make([]string, 0, len(changed))

	for _, p := range changed {
		p = path.Clean(filepath.ToSlash(p))
		if p == "." || p == ".." || strings.HasPrefix(p, "../") || path.IsAbs(p) || slices.Contains(out, p) {
			continue
		}

		out = append(out, p)
	}

	return out
}

func isFile(name string) bool {
	info, err := os.Stat(name)

	return err == nil && info.Mode().IsRegular()
}

// present returns the files that still exist under root. A deleted file
// selects its checks and cannot be handed to one.
func present(root string, files []string) []string {
	var out []string

	for _, f := range files {
		if isFile(filepath.Join(root, filepath.FromSlash(f))) {
			out = append(out, f)
		}
	}

	return out
}

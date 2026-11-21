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

	"github.com/kyleking/wavez/internal/config"
)

// Option adjusts the Runner New returns.
type Option func(*runner)

// WithLookPath replaces how the runner finds a tool's binary, which is
// exec.LookPath by default. A lookup that fails makes every check needing
// that tool abstain.
func WithLookPath(lookPath func(name string) (string, error)) Option {
	return func(r *runner) { r.lookPath = lookPath }
}

// WithoutCache makes the runner neither read nor write .wavez/cache, as
// setting WAVEZ_NO_CACHE does.
func WithoutCache() Option {
	return func(r *runner) { r.noCache = true }
}

// New returns the Runner that loads the project's configuration from root on
// each run.
//
//nolint:ireturn // Runner is the frozen surface internal/hook and cmd/wavez consume
func New(opts ...Option) Runner {
	r := &runner{lookPath: exec.LookPath, noCache: cacheDisabled()}
	for _, opt := range opts {
		opt(r)
	}

	return r
}

type runner struct {
	lookPath func(name string) (string, error)
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

// Run loads root's configuration and runs the checks changed selects for
// stage. Fixers run first and one at a time, since two of them may rewrite
// the same file; the checks then run side by side and report in a fixed
// order: the built-ins, then the project's own in file order. A blocking
// failure at the stop stage is then compared with the baseline the context
// carries, if it carries one. The error is for a configuration or root that
// cannot be read, never for a check.
func (r *runner) Run(ctx context.Context, root string, changed []string, stage Stage) (Report, error) {
	changed = normalize(changed)
	if len(changed) == 0 {
		return Report{}, nil
	}

	root, err := resolveRoot(root)
	if err != nil {
		return Report{}, err
	}

	cfg, err := config.Load(root)
	if err != nil {
		return Report{}, fmt.Errorf("loading configuration: %w", err)
	}

	proj := project{root: root, changed: changed, cfg: cfg}
	if !isFile(filepath.Join(root, "go.mod")) {
		proj.cfg.Disabled = config.Builtins()
	}

	var results []Result
	if stage == StageEdit {
		results = r.format(ctx, proj)
	}

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

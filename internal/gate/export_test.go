package gate

// WithLookPath replaces how the runner finds a tool's binary, which is
// exec.LookPath outside tests. A lookup that fails makes every check needing
// that tool abstain.
func WithLookPath(lookPath func(name string) (string, error)) Option {
	return func(r *runner) { r.lookPath = lookPath }
}

// WithoutCache makes the runner neither read nor write .wavez/cache, as
// setting WAVEZ_NO_CACHE does.
func WithoutCache() Option {
	return func(r *runner) { r.noCache = true }
}

// DirsSelected exposes which package directories a change selects.
func DirsSelected(root string, changed []string) []string {
	return packageDirs(root, changed)
}

package hook

// WithLookPath replaces how the handler finds gopls, which the command a
// rename denial suggests cannot run without. A nil find is ignored.
func WithLookPath(find func(name string) (string, error)) Option {
	return func(h *Handler) {
		if find != nil {
			h.lookPath = find
		}
	}
}

// WithChecks replaces how the handler learns which checks a project has.
func WithChecks(checks func(root string) (builtins, custom []string, err error)) Option {
	return func(h *Handler) { h.checks = checks }
}

// AnnouncementFor exposes the SessionStart sentence for a set of checks.
func AnnouncementFor(builtins, custom []string) string { return announcement(builtins, custom) }

// RenamesIn exposes the renames the detector reads in a shell command, as
// "old new" pairs.
func RenamesIn(command string) []string {
	renames := textRenames(command)
	pairs := make([]string, 0, len(renames))

	for _, rename := range renames {
		pairs = append(pairs, rename.pair.line())
	}

	return pairs
}

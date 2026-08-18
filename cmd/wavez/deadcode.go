package main

import (
	"context"
	"fmt"
	"os"

	"github.com/kyleking/wavez/internal/config"
	"github.com/kyleking/wavez/internal/deadcode"
)

// deadcodeCheck reports the functions no binary's main can reach. It is a
// command and not a gate: whether an unreachable function is an orphan or a
// deliberate test seam is a judgment, and a blocking check would only earn
// itself an allowlist entry per finding.
//
// It exits nonzero when anything outside the allowlist is unreachable, so a
// caller that wants it enforced can have that without wavez deciding.
func deadcodeCheck(ctx context.Context, root string, cfg config.Config) error {
	report, err := deadcode.New(root, deadcode.WithAllow(cfg.DeadcodeAllow)).Run(ctx, "./cmd/...")
	if err != nil {
		return fmt.Errorf("analyzing reachability: %w", err)
	}

	actionable := report.Actionable()
	allowed := len(report.Unreached) - len(actionable)

	if len(actionable) == 0 {
		fmt.Printf("every function is reachable from a main (%d allowlisted)\n", allowed)

		return nil
	}

	fmt.Printf("%d function(s) no main reaches, %d more allowlisted:\n\n", len(actionable), allowed)

	for _, f := range actionable {
		fmt.Printf("  %s:%d %s.%s\n", f.File, f.Line, shortPkg(f.Package), f.Name)
	}

	fmt.Fprintln(os.Stderr,
		"\nEach is either an orphan to wire up or delete, or deliberate API to add to deadcodeAllow.")

	return errUnreachableCode
}

func shortPkg(importPath string) string {
	for i := len(importPath) - 1; i >= 0; i-- {
		if importPath[i] == '/' {
			return importPath[i+1:]
		}
	}

	return importPath
}

package gate

import (
	"fmt"
	"os"

	"github.com/kyleking/wavez/internal/gofix"
)

// parallelizeFile rewrites path in place when a changed test is missing the
// t.Parallel() call the project requires.
func parallelizeFile(path string) error {
	//nolint:gosec // path comes from this gate's own changed-file list
	src, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	out, err := gofix.AddParallelCalls(path, src)
	if err != nil {
		return fmt.Errorf("adding t.Parallel to %s: %w", path, err)
	}
	if out == nil {
		return nil
	}

	//nolint:gosec // containedPath already refused anything outside repoRoot
	if err := os.WriteFile(path, out, formattedFilePerm); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}

	return nil
}

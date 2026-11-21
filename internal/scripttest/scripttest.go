// Package scripttest writes the fake programs tests run in place of a real
// tool.
package scripttest

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// Write creates an executable file at path holding content.
//
// A child process does the writing. A file this process had open for
// writing can be inherited by another test's fork that is midway to its
// exec, and running the file then fails with "text file busy" until that
// fork finishes. Writing from a child means no descriptor for the file ever
// exists here to inherit.
func Write(tb testing.TB, path, content string) {
	tb.Helper()

	const (
		target = "SCRIPTTEST_TARGET"
		script = `cat > "$` + target + `" && chmod 700 "$` + target + `"`
	)

	cmd := exec.CommandContext(tb.Context(), "sh", "-c", script)
	cmd.Env = append(os.Environ(), target+"="+path)
	cmd.Stdin = strings.NewReader(content)

	if out, err := cmd.CombinedOutput(); err != nil {
		tb.Fatalf("writing the script %s: %v: %s", path, err, out)
	}
}

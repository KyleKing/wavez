package config_test

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/config"
)

// TestRoutines_ReadableFromAnImportingModule covers what DESIGN.md's
// Routines section promises about `hk.pkl` importing `.wavez.pkl`: another
// module can import the project config and read a routine's argv out of it,
// so a git hook runs the same command the agent does. It asserts the import
// rather than driving hk, because hk itself needs a downloaded package and
// its default `pklr` backend still fails on this file (see the note in
// DESIGN.md).
func TestRoutines_ReadableFromAnImportingModule(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, config.FileName), `
amends ".wavez/Wavez.pkl"

routines {
  ["fmt"] {
    triggers { "change" }
    steps {
      new {
        name = "gofmt"
        action = "run"
        params { ["argv"] = new Listing { "gofmt"; "-l"; "-w"; "." } }
      }
    }
  }
}
`)

	loader, err := config.NewLoader(context.Background())
	if err != nil {
		t.Fatalf("NewLoader: %v", err)
	}
	t.Cleanup(func() {
		if err := loader.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	// Load materializes the schema into .wavez/, which is what makes the
	// amending file resolvable from anywhere else in the project.
	if _, _, err := loader.Load(context.Background(), root); err != nil {
		t.Fatalf("Load: %v", err)
	}

	importer := filepath.Join(root, "importer.pkl")
	writeFile(t, importer, `
import ".wavez.pkl" as wavez

fixCommand = wavez.routines["fmt"].steps[0].params["argv"].join(" ")
`)

	//nolint:gosec // importer is a path this test wrote into its own temp dir
	out, err := exec.CommandContext(context.Background(), "pkl", "eval", importer).CombinedOutput()
	if err != nil {
		t.Fatalf("pkl eval %s: %v: %s", importer, err, out)
	}

	if want := `fixCommand = "gofmt -l -w ."`; !strings.Contains(string(out), want) {
		t.Errorf("pkl eval output = %q, want it to contain %q", out, want)
	}
}

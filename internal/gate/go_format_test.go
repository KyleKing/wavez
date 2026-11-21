package gate_test

import (
	"slices"
	"testing"

	"github.com/kyleking/wavez/internal/gate"
)

const (
	unformatted = "package a\n\nfunc  A( ) int {return 1}\n"
	formatted   = "package a\n\nfunc A() int { return 1 }\n"
)

func TestRun_EditStageFormatsGo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		files   map[string]string
		changed []string
		opts    []gate.Option
		wants   []want
		fixed   []string
	}{
		{
			name:    "an unformatted file is rewritten and reported",
			files:   map[string]string{"a.go": unformatted, "b.go": formatted},
			changed: []string{"a.go", "b.go"},
			wants:   []want{{"format", gate.StatusPass, ""}}, fixed: []string{"a.go"},
		},
		{
			name: "a formatted file is left alone", files: map[string]string{"a.go": formatted},
			changed: []string{"a.go"}, wants: []want{{"format", gate.StatusPass, ""}},
		},
		{
			name:    "a file that does not parse fails and the rest are still formatted",
			files:   map[string]string{"a.go": unformatted, "bad.go": "package a\n\nfunc X( {\n"},
			changed: []string{"a.go", "bad.go"},
			wants:   []want{{"format", gate.StatusFail, "bad.go:3:"}}, fixed: []string{"a.go"},
		},
		{
			name: "no formatter abstains", files: map[string]string{"a.go": unformatted},
			changed: []string{"a.go"}, opts: []gate.Option{without("gofmt")},
			wants: []want{{"format", gate.StatusAbstained, "gofmt not on PATH"}},
		},
		{
			name: "fixtures, vendored code, and deleted files are not formatted",
			files: map[string]string{
				"testdata/a.go": unformatted, "vendor/x/a.go": unformatted, "_x/a.go": unformatted,
			},
			changed: []string{"testdata/a.go", "vendor/x/a.go", "_x/a.go", "gone.go", "notes.md"},
		},
		{
			name:    "a disabled format does not run",
			files:   map[string]string{"a.go": unformatted, ".wavez.toml": "disable = [\"format\"]\n"},
			changed: []string{"a.go"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tt.files["go.mod"] = goMod
			root := project(t, tt.files)
			report := runGate(t.Context(), t, root, gate.StageEdit, tt.changed, tt.opts...)

			expect(t, report, tt.wants...)

			if len(report.Results) > 0 && !slices.Equal(report.Results[0].Fixed, tt.fixed) {
				t.Errorf("Fixed = %v, want %v", report.Results[0].Fixed, tt.fixed)
			}

			for _, name := range tt.fixed {
				if got := readFile(t, root, name); got != formatted {
					t.Errorf("%s was not formatted:\n%s", name, got)
				}
			}

			if len(tt.wants) == 0 && readFile(t, root, tt.changed[0]) != unformatted {
				t.Errorf("%s was rewritten", tt.changed[0])
			}
		})
	}
}

func TestRun_FormatterThatCannotRunAbstains(t *testing.T) {
	t.Parallel()

	root := project(t, map[string]string{
		"go.mod": goMod, "a.go": formatted,
		"bin/gofmt": "#!/bin/sh\necho 'gofmt: cannot find GOROOT' >&2\nexit 2\n",
	})

	report := runGate(t.Context(), t, root, gate.StageEdit, []string{"a.go"},
		replacing("gofmt", root+"/bin/gofmt"))
	expect(t, report, want{"format", gate.StatusAbstained, "cannot find GOROOT"})
}

package gate_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/gate"
)

var errSelect = errors.New("selector broke")

// fakeSelector answers with a fixed selection and records what it was asked.
type fakeSelector struct {
	err     error
	sel     gate.Selection
	root    string
	changed []string
}

func (f *fakeSelector) Select(_ context.Context, root, _ string, changed []string) (gate.Selection, error) {
	f.root, f.changed = root, slices.Clone(changed)

	return f.sel, f.err
}

func TestRun_TestSelection(t *testing.T) {
	t.Parallel()

	// A value of three in package a fails TestA, and TestB through b.
	tests := []struct {
		name     string
		selector *fakeSelector
		// absent is a test the report must not name.
		absent string
		wants  []want
	}{
		{
			name: "no selector runs every affected test", selector: nil,
			wants: []want{{"build", gate.StatusPass, ""}, {"test", gate.StatusFail, "TestA"}},
		},
		{
			name:     "a complete selection runs only what it names",
			selector: &fakeSelector{sel: gate.Selection{Complete: true, Tests: map[string][]string{"./b": {"TestB"}}}},
			absent:   "TestA",
			wants:    []want{{"build", gate.StatusPass, ""}, {"test", gate.StatusFail, "TestB"}},
		},
		{
			name:     "a complete selection of nothing runs no test",
			selector: &fakeSelector{sel: gate.Selection{Complete: true}},
			wants:    []want{{"build", gate.StatusPass, ""}, {"test", gate.StatusPass, ""}},
		},
		{
			name:     "an incomplete selection runs every affected test",
			selector: &fakeSelector{sel: gate.Selection{Tests: map[string][]string{"./lone": {"TestNone"}}}},
			wants:    []want{{"build", gate.StatusPass, ""}, {"test", gate.StatusFail, "TestA"}},
		},
		{
			name: "a selector that fails runs every affected test",
			selector: &fakeSelector{
				err: errSelect, sel: gate.Selection{Complete: true, Tests: map[string][]string{"./lone": {"TestNone"}}},
			},
			wants: []want{{"build", gate.StatusPass, ""}, {"test", gate.StatusFail, "TestA"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := project(t, module(map[string]string{"a/a.go": srcAThree, ".wavez.toml": "disable = [\"lint\"]\n"}))

			var opts []gate.Option
			if tt.selector != nil {
				opts = append(opts, gate.WithSelector(tt.selector))
			}

			report := runGate(t.Context(), t, root, gate.StageStop, []string{"a/a.go"}, opts...)
			expect(t, report, tt.wants...)

			if tt.selector != nil && !slices.Equal(tt.selector.changed, []string{"a/a.go"}) {
				t.Errorf("the selector was asked about %v, want [a/a.go]", tt.selector.changed)
			}

			if tt.absent != "" && strings.Contains(report.Render(), tt.absent) {
				t.Errorf("%s ran though it was not selected: %s", tt.absent, report.Render())
			}
		})
	}
}

func TestRun_NearTestsStayInTheChangedPackages(t *testing.T) {
	t.Parallel()

	// A value of nine in package a breaks only TestC, in a package that
	// imports it.
	nine := map[string]string{"a/a.go": srcANine, "a/a_test.go": removed}

	tests := []struct {
		name  string
		opts  []gate.Option
		wants []want
	}{
		{
			name:  "every importer is tested",
			wants: []want{{"build", gate.StatusPass, ""}, {"test", gate.StatusFail, "TestC"}},
		},
		{
			name: "near the change nothing fails", opts: []gate.Option{gate.WithNearTests()},
			wants: passed("build", "test"),
		},
		{
			name: "a selection elsewhere is left for later",
			opts: []gate.Option{gate.WithNearTests(), gate.WithSelector(&fakeSelector{sel: gate.Selection{
				Complete: true, Tests: map[string][]string{"./c": {"TestC"}},
			}})},
			wants: passed("build", "test"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := project(t, module(nine))
			expect(t, runGate(t.Context(), t, root, gate.StageStop, []string{"a/a.go"}, tt.opts...), tt.wants...)
		})
	}

	// A failure in the changed package is still found.
	root := project(t, module(map[string]string{"a/a.go": srcAThree}))
	expect(t, runGate(t.Context(), t, root, gate.StageStop, []string{"a/a.go"}, gate.WithNearTests()),
		want{"build", gate.StatusPass, ""}, want{"test", gate.StatusFail, "A() is not 1"})
}

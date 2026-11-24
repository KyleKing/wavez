package refactor_test

import (
	"go/format"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/refactor"
)

const inlineSource = `package lib

// twice doubles x.
func twice(x int) int {
	y := x * 2
	return y
}

// Both calls twice two times on one line.
func Both(x int) int {
	return twice(x) + twice(1)
}

// One calls twice once.
func One() int {
	v := twice(3)
	return v
}
`

func TestInline(t *testing.T) {
	t.Parallel()
	needGopls(t)

	cases := []struct {
		name, target, callee string
		gone                 string
		want                 refactor.InlineResult
	}{
		{
			name: "one call", target: "lib/inl.go:16", callee: "twice", gone: "twice(3)",
			want: refactor.InlineResult{At: "lib/inl.go:16", Name: "twice", Calls: 1},
		},
		{
			name: "every call on a line", target: "lib/inl.go:11", callee: "twice", gone: "twice(x)",
			want: refactor.InlineResult{At: "lib/inl.go:11", Name: "twice", Calls: 2},
		},
		{
			name: "every call on a range", target: "lib/inl.go:10-17", callee: "twice", gone: "twice(1)",
			want: refactor.InlineResult{At: "lib/inl.go:10-17", Name: "twice", Calls: 3},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root := fixture(t, map[string]string{"lib/inl.go": inlineSource})

			res, err := newRefactorer(t, root).Inline(t.Context(), tc.target, tc.callee)
			require.NoError(t, err)

			assert.Equal(t, tc.want, res)

			src := read(t, root, "lib/inl.go")
			assert.NotContains(t, src, tc.gone)
			assert.Contains(t, src, "func twice(x int) int {", "the declaration stays")
			assert.True(t, isGofmt(src), "the inlined file is formatted:\n%s", src)
			builds(t, root)
		})
	}
}

func TestInline_Refusals(t *testing.T) {
	t.Parallel()
	needGopls(t)

	cases := []struct {
		check                         func(t *testing.T, err error)
		name, target, callee, wantMsg string
	}{
		{
			name: "no call on the line", target: "lib/inl.go:5", callee: "twice",
			check: isType[*refactor.InlineError], wantMsg: "no call to twice there",
		},
		{
			name: "a qualified name", target: "app/app.go:7", callee: "lib.Greet",
			check: isType[*refactor.InlineError], wantMsg: "without its package or receiver",
		},
		{
			name: "a body that uses names the caller cannot see", target: "app/app.go:7", callee: "Greet",
			check: isType[*refactor.GoplsError], wantMsg: "non-exported shout",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root := fixture(t, map[string]string{"lib/inl.go": inlineSource})
			before := contents(t, root)

			_, err := newRefactorer(t, root).Inline(t.Context(), tc.target, tc.callee)
			refused(t, root, before, err, tc.check, tc.wantMsg)
		})
	}
}

func TestInline_RollsBackWhenItAddsAnError(t *testing.T) {
	t.Parallel()

	root := calcFixture(t)
	before := contents(t, root)

	_, err := newRefactorer(t, root, refactor.WithGopls(fakeExtractor(t))).
		Inline(t.Context(), "lib/calc.go:13", "Repeat")

	var build *refactor.BuildError

	require.ErrorAs(t, err, &build)
	assert.Contains(t, build.Output, "undefined: missing")
	assert.Equal(t, before, contents(t, root))
}

func isGofmt(src string) bool {
	formatted, err := format.Source([]byte(src))

	return err == nil && string(formatted) == src
}

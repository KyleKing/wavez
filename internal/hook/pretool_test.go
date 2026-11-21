package hook_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/hook"
)

var errNotFound = errors.New("executable file not found in $PATH")

func found(name string) (string, error) { return "/usr/bin/" + name, nil }

func missing(string) (string, error) { return "", errNotFound }

const statsSource = `package bench

// MaxRuns bounds a sample.
const MaxRuns = 5

var (
	defaultName = "x"
)

// Memory is a reading.
type Memory struct {
	Total int
	free  int
}

// Reader reads.
type Reader interface {
	Next() bool
}

// Read reads a log.
func Read() int { return MaxRuns }

// Free is what is left.
func (m *Memory) Free() int { return m.free }

// Pair is generic.
type Pair[K comparable, V any] struct{ key K }

func (p Pair[K, V]) Key() K { return p.key }

type one[T any] struct{ v T }

func (o *one[T]) Value() T { return o.v }
`

// module returns a Go module that declares the names the rename cases use,
// along with the trees a search must stay out of.
func module(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	write(t, root, "go.mod", "module example.com/m\n\ngo 1.26\n")
	write(t, root, "internal/bench/stats.go", statsSource)
	write(t, root, "internal/bench/stats_test.go", "package bench\n\nfunc helper() int { return Read() }\n")
	write(t, root, "internal/other/read.go", "package other\n\n// Scan is declared twice.\nfunc Scan() {}\n")
	write(t, root, "cmd/tool/main.go", "package main\n\nfunc main() {}\n\n// Scan is declared twice.\nfunc Scan() {}\n")
	write(t, root, "internal/bench/broken.go", "package bench\n\nfunc Vanished( {\n")
	write(t, root, "vendor/dep/dep.go", "package dep\n\nfunc Vendored() {}\n")
	write(t, root, "internal/bench/testdata/fixture.go", "package fixture\n\nfunc Fixture() {}\n")
	write(t, root, "nested/go.mod", "module example.com/nested\n")
	write(t, root, "nested/n.go", "package nested\n\nfunc Nested() {}\n")
	write(t, root, ".cache/c.go", "package c\n\nfunc Hidden() {}\n")
	write(t, root, "README.md", "Read the docs.\n")

	return root
}

func shell(root, session, command string) map[string]any {
	return map[string]any{
		"session_id": session, "cwd": root, "hook_event_name": "PreToolUse", "tool_name": "Bash",
		"tool_input":  map[string]any{"command": command, "description": "x", "timeout": 120000},
		"tool_use_id": "toolu_01",
	}
}

// renameCase is one shell command put to the PreToolUse hook.
type renameCase struct {
	name    string
	command string
	// want is the command lines a denial must offer. Nil is the line that
	// renames Read to ReadLog.
	want []string
}

// renamesBySed are sed commands the hook must deny.
func renamesBySed() []renameCase {
	return []renameCase{
		{name: "sed over a glob", command: `sed -i 's/\bRead\b/ReadLog/g' internal/bench/*.go`},
		{name: "sed in double quotes", command: `sed -i "s/\bRead\b/ReadLog/g" internal/bench/stats.go`},
		{name: "sed with no boundaries", command: `sed -i 's/Read/ReadLog/g' ./internal/bench/stats.go`},
		{
			name:    "sed with GNU word marks and another delimiter",
			command: `sed -i 's|\<Read\>|ReadLog|g' cmd/tool/main.go`,
		},
		{name: "BSD sed with an empty suffix", command: `sed -i '' -e 's/Read/ReadLog/g' internal/bench/stats.go`},
		{name: "sed with a backup suffix and -E", command: `sed -E -i.bak 's/\bRead\b/ReadLog/' a.go`},
		{name: "sed with flags in one cluster", command: `sed -Ei 's/\bRead\b/ReadLog/g' a.go`},
		{name: "sed with a long option", command: `sed --in-place --expression='s/Read/ReadLog/g' a.go`},
		{name: "sed -e glued to its script", command: `sed -i -es/Read/ReadLog/g a.go`},
		{name: "a call site pattern", command: `sed -i 's/Read(/ReadLog(/g' a.go`},
		{name: "an escaped call site pattern", command: `sed -i 's/Read\(/ReadLog(/g' a.go`},
		{name: "a qualified pattern", command: `sed -i 's/bench\.Read/bench.ReadLog/g' cmd/tool/main.go`},
		{name: "find piped to xargs", command: `find . -name '*.go' | xargs sed -i 's/\bRead\b/ReadLog/g'`},
		{
			name:    "grep piped to xargs with flags",
			command: `grep -rl Read --include='*.go' . | xargs -n 10 -r sed -i 's/Read/ReadLog/g'`,
		},
		{
			name:    "a recursive grep with no file filter",
			command: `grep -rl 'Read' . | xargs sed -i 's/\bRead\b/ReadLog/g'`,
		},
		{name: "find -exec", command: `find . -name '*.go' -exec sed -i 's/\bRead\b/ReadLog/g' {} +`},
		{
			name:    "find -exec ended by a semicolon",
			command: `find . -type f -name "*.go" -exec sed -i 's/Read/ReadLog/' {} \;`,
		},
		{
			name:    "a command substitution for the files",
			command: `sed -i 's/Read/ReadLog/g' $(grep -rl Read --include='*.go' .)`,
		},
		{
			name:    "a loop over files",
			command: `for f in $(git ls-files '*.go'); do sed -i 's/Read/ReadLog/g' "$f"; done`,
		},
		{
			name:    "after cd and an assignment",
			command: `cd internal && LC_ALL=C sed -i 's/Read/ReadLog/g' bench/stats.go`,
		},
	}
}

// renamesByOtherMeans are commands the hook must deny that use another
// tool than sed, or rename something other than a function.
func renamesByOtherMeans() []renameCase {
	return []renameCase{
		{name: "perl", command: `perl -pi -e 's/\bRead\b/ReadLog/g' internal/bench/*.go`},
		{name: "perl with flags apart", command: `perl -i -pe 's/\bRead\b/ReadLog/g' $(git grep -l Read -- '*.go')`},
		{name: "gofmt rewrite", command: `gofmt -r 'Read -> ReadLog' -w ./internal`},
		{name: "gofmt rewrite, qualified", command: `gofmt -w -r 'bench.Read -> bench.ReadLog' .`},
		{name: "an absolute sed", command: `/usr/bin/sed -i 's/Read/ReadLog/g' a.go`},
		{
			name:    "a method",
			command: `sed -i 's/\bFree\b/Available/g' internal/bench/*.go`,
			want:    []string{"  wavez rename internal/bench/stats.go:Memory.Free Available"},
		},
		{
			name:    "a method on a generic type",
			command: `sed -i 's/Key/Name/g' a.go`,
			want:    []string{"  wavez rename internal/bench/stats.go:Pair.Key Name"},
		},
		{
			name:    "a method on a generic pointer",
			command: `sed -i 's/Value/Val/g' a.go`,
			want:    []string{"  wavez rename internal/bench/stats.go:one.Value Val"},
		},
		{
			name:    "a struct field",
			command: `sed -i 's/\bTotal\b/TotalBytes/g' internal/bench/*.go`,
			want:    []string{"  wavez rename internal/bench/stats.go:Memory.Total TotalBytes"},
		},
		{
			name:    "an interface method",
			command: `sed -i 's/\bNext\b/Advance/g' internal/bench/*.go`,
			want:    []string{"  wavez rename internal/bench/stats.go:Reader.Next Advance"},
		},
		{
			name:    "a constant",
			command: `sed -i 's/MaxRuns/RunLimit/g' internal/bench/*.go`,
			want:    []string{"  wavez rename internal/bench/stats.go:MaxRuns RunLimit"},
		},
		{
			name:    "a variable in a block",
			command: `sed -i 's/defaultName/fallbackName/g' a.go`,
			want:    []string{"  wavez rename internal/bench/stats.go:defaultName fallbackName"},
		},
		{
			name:    "a type",
			command: `sed -i 's/\bMemory\b/Reading/g' a.go`,
			want:    []string{"  wavez rename internal/bench/stats.go:Memory Reading"},
		},
		{
			name:    "a name declared twice lists both",
			command: `sed -i 's/\bScan\b/Sweep/g' cmd/tool/*.go`,
			want: []string{
				"(Scan is declared more than once; use the line for the one you mean)",
				"  wavez rename cmd/tool/main.go:Scan Sweep",
				"  wavez rename internal/other/read.go:Scan Sweep",
			},
		},
		{
			name:    "two renames in one script",
			command: `sed -i 's/Read/ReadLog/g; s/"x"/"y"/; s/MaxRuns/RunLimit/g' a.go`,
			want: []string{
				"  wavez rename internal/bench/stats.go:Read ReadLog",
				"  wavez rename internal/bench/stats.go:MaxRuns RunLimit",
			},
		},
		{name: "two -e scripts", command: `sed -i -e 's/Read/ReadLog/g' -e 's/Nothing/Something/g' a.go`},
	}
}

// notRenamesByText are commands the hook must leave alone.
func notRenamesByText() []renameCase {
	return []renameCase{
		{name: "not a shell rename at all", command: `go test ./internal/bench/`},
		{name: "sed that only prints", command: `sed 's/Read/ReadLog/g' internal/bench/stats.go`},
		{name: "sed on a file that is not Go", command: `sed -i 's/Read/ReadLog/g' README.md`},
		{name: "sed fed markdown files", command: `find . -name '*.md' | xargs sed -i 's/Read/ReadLog/g'`},
		{name: "a loop over markdown", command: `for f in docs/*.md; do sed -i 's/Read/ReadLog/g' "$f"; done`},
		{name: "a name the module does not declare", command: `sed -i 's/\bMissing\b/Present/g' internal/bench/*.go`},
		{name: "a name declared only in a file that does not parse", command: `sed -i 's/Vanished/Gone/g' a.go`},
		{name: "a name declared only under vendor", command: `sed -i 's/Vendored/Owned/g' a.go`},
		{name: "a name declared only under testdata", command: `sed -i 's/Fixture/Sample/g' a.go`},
		{name: "a name declared only in a nested module", command: `sed -i 's/Nested/Inner/g' a.go`},
		{name: "a name declared only in a hidden directory", command: `sed -i 's/Hidden/Shown/g' a.go`},
		{name: "a pattern that is not one identifier", command: `sed -i 's/Read the/ReadLog the/g' a.go`},
		{name: "a regular expression", command: `sed -i 's/Read[A-Z]*/ReadLog/g' a.go`},
		{name: "a replacement that is not an identifier", command: `sed -i 's/Read/bench.Read/g' a.go`},
		{name: "a call pattern without a call replacement", command: `sed -i 's/Read(/ReadLog/g' a.go`},
		{name: "the same name on both sides", command: `sed -i 's/Read/Read/g' a.go`},
		{name: "a keyword", command: `sed -i 's/func/fn/g' a.go`},
		{name: "an addressed substitution", command: `sed -i '3,9s/Read/ReadLog/' a.go`},
		{name: "a delete command", command: `sed -i '/Read/d' a.go`},
		{name: "a script with another command after it", command: `sed -i 's/Read/ReadLog/g;p' a.go`},
		{name: "a substitution with no end", command: `sed -i 's/Read/ReadLog' a.go`},
		{name: "a substitution with no replacement", command: `sed -i 's/Read' a.go`},
		{name: "a script in a file", command: `sed -i -f rename.sed a.go`},
		{name: "sed with nothing after it", command: `sed -i`},
		{name: "perl that does not print", command: `perl -i -e 's/Read/ReadLog/g' a.go`},
		{name: "perl that does not write", command: `perl -pe 's/Read/ReadLog/g' a.go`},
		{name: "gofmt without -w", command: `gofmt -r 'Read -> ReadLog' .`},
		{name: "gofmt with a rule that is not a rename", command: `gofmt -w -r 'a[b:len(a)] -> a[b:]' .`},
		{name: "gofmt with no rule", command: `gofmt -w .`},
		{name: "find with no -exec", command: `find . -name '*.go' -newer go.mod`},
		{name: "find with an empty -exec", command: `find . -name '*.go' -exec`},
		{name: "xargs with nothing to run", command: `find . -name '*.go' | xargs -n 1`},
		{name: "an unterminated quote", command: `sed -i 's/Read/ReadLog/g a.go`},
		{name: "an unterminated substitution", command: `sed -i 's/Read/ReadLog/g' $(ls`},
		{name: "only an assignment", command: `FOO=bar`},
		{name: "an empty command", command: ``},
	}
}

func TestHandle_PreToolUse_DeniesARenameByText(t *testing.T) {
	t.Parallel()

	root := module(t)
	handler := hook.New(&fakeRunner{}, hook.WithLookPath(found))

	for i, tt := range append(renamesBySed(), renamesByOtherMeans()...) {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			res, err := fire(t, handler, shell(root, fmt.Sprintf("rename-%d", i), tt.command))
			require.NoError(t, err)

			require.Equal(t, "deny", res.Specific.PermissionDecision, res.raw)
			assert.Equal(t, "PreToolUse", res.Specific.EventName)
			assert.ElementsMatch(t, []string{"hookEventName", "permissionDecision", "permissionDecisionReason"},
				res.specificKeys)
			assert.Empty(t, res.Decision, "PreToolUse decides inside hookSpecificOutput, not at the top level")

			want := tt.want
			if want == nil {
				want = []string{"  wavez rename internal/bench/stats.go:Read ReadLog"}
			}

			lines := strings.Split(res.Specific.PermissionReason, "\n")
			require.Greater(t, len(lines), 2)
			assert.True(t, strings.HasPrefix(lines[0], "wavez: this renames a Go symbol by rewriting text."), lines[0])
			assert.Equal(t, want, lines[1:len(lines)-1])
			assert.Equal(t, "The command you sent will run if you send it again.", lines[len(lines)-1])
		})
	}
}

func TestHandle_PreToolUse_LeavesOtherCommandsAlone(t *testing.T) {
	t.Parallel()

	root := module(t)
	handler := hook.New(&fakeRunner{}, hook.WithLookPath(found))

	for i, tt := range notRenamesByText() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			res, err := fire(t, handler, shell(root, fmt.Sprintf("other-%d", i), tt.command))
			require.NoError(t, err)
			assert.Empty(t, res.raw, "a command that is not surely a Go rename runs untouched")
		})
	}
}

func TestHandle_PreToolUse_DeniesOncePerSessionAndPair(t *testing.T) {
	t.Parallel()

	root := module(t)
	handler := hook.New(&fakeRunner{}, hook.WithLookPath(found))
	rename := `sed -i 's/\bRead\b/ReadLog/g' internal/bench/*.go`

	deny := func(session, command string) bool {
		res, err := fire(t, handler, shell(root, session, command))
		require.NoError(t, err)

		return res.Specific.PermissionDecision == "deny"
	}

	assert.True(t, deny("one", rename))
	assert.False(t, deny("one", rename), "the same command sent again runs")
	assert.False(t, deny("one", `perl -pi -e 's/Read/ReadLog/g' a.go`), "the pair is what was denied, not the spelling")
	assert.True(t, deny("one", `sed -i 's/\bRead\b/ReadFile/g' a.go`), "another new name is another rename")
	assert.True(t, deny("one", `sed -i 's/Read/ReadLog/g; s/MaxRuns/RunLimit/g' a.go`),
		"a pair not yet denied still is")
	assert.False(t, deny("one", `sed -i 's/MaxRuns/RunLimit/g' a.go`))
	assert.True(t, deny("two", rename), "another session has its own list")

	assert.Equal(t, "Read ReadLog\nRead ReadFile\nMaxRuns RunLimit\n", read(t, root, ".wavez/sessions/one.renames"))
	assert.NoFileExists(t, filepath.Join(root, ".wavez", "sessions", "one.json"), "a denial takes no baseline")
}

func TestHandle_PreToolUse_StaysOutOfTheWayWhenTheSuggestionCannotWork(t *testing.T) {
	t.Parallel()

	rename := `sed -i 's/\bRead\b/ReadLog/g' internal/bench/*.go`

	tests := []struct {
		setup func(t *testing.T, root string) map[string]any
		find  func(string) (string, error)
		name  string
	}{
		{
			name:  "gopls is not installed",
			setup: func(_ *testing.T, root string) map[string]any { return shell(root, "s", rename) },
			find:  missing,
		},
		{
			name: "the project is not a Go module",
			setup: func(t *testing.T, root string) map[string]any {
				t.Helper()
				require.NoError(t, os.Remove(filepath.Join(root, "go.mod")))

				return shell(root, "s", rename)
			},
		},
		{
			name: "another tool",
			setup: func(_ *testing.T, root string) map[string]any {
				ev := shell(root, "s", rename)
				ev["tool_name"] = "Read"

				return ev
			},
		},
		{
			name:  "a session id that cannot name a file",
			setup: func(_ *testing.T, root string) map[string]any { return shell(root, "../s", rename) },
		},
		{
			name:  "no session id",
			setup: func(_ *testing.T, root string) map[string]any { return shell(root, "", rename) },
		},
		{
			name: "the list of denials cannot be read",
			setup: func(t *testing.T, root string) map[string]any {
				t.Helper()
				require.NoError(t, os.MkdirAll(filepath.Join(root, ".wavez", "sessions", "s.renames"), 0o750))

				return shell(root, "s", rename)
			},
		},
		{
			name: "the list of denials cannot be written",
			setup: func(t *testing.T, root string) map[string]any {
				t.Helper()
				write(t, root, ".wavez/sessions", "a file where the directory belongs\n")

				return shell(root, "s", rename)
			},
		},
		{
			name: "the list of denials cannot be opened",
			setup: func(t *testing.T, root string) map[string]any {
				t.Helper()

				dir := filepath.Join(root, ".wavez", "sessions")
				require.NoError(t, os.MkdirAll(dir, 0o750))
				require.NoError(t, os.Symlink(filepath.Join("gone", "x"), filepath.Join(dir, "s.renames")))

				return shell(root, "s", rename)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := module(t)
			ev := tt.setup(t, root)

			find := tt.find
			if find == nil {
				find = found
			}

			res, err := fire(t, hook.New(&fakeRunner{}, hook.WithLookPath(find), hook.WithLookPath(nil)), ev)
			require.NoError(t, err, "a shell command is never failed by this hook")
			assert.Empty(t, res.raw, "a denial that cannot be backed or recorded is not made")
		})
	}
}

func TestHandle_PreToolUse_FromASubdirectoryOfTheModule(t *testing.T) {
	t.Parallel()

	root := module(t)
	git(t, root, "init", "-q")

	res, err := fire(t, hook.New(&fakeRunner{}, hook.WithLookPath(found)),
		shell(filepath.Join(root, "internal", "bench"), "sub", `sed -i 's/\bRead\b/ReadLog/g' *.go`))
	require.NoError(t, err)
	assert.Contains(t, res.Specific.PermissionReason, "\n  wavez rename internal/bench/stats.go:Read ReadLog\n",
		"the target is relative to the module root, which wavez rename reads from any directory under it")
	assert.FileExists(t, filepath.Join(root, ".wavez", "sessions", "sub.renames"))
}

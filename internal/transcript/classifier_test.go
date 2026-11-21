package transcript_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/transcript"
)

// readShell attributes a session of one shell call, which is how the
// classifier table is reached from outside the package.
func readShell(t *testing.T, tool, input string) transcript.Stats {
	t.Helper()

	got, err := transcript.Read(strings.NewReader(use("m", "t", tool, input)))
	require.NoError(t, err)

	return got
}

func command(line string) string {
	quoted := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(line)

	return `{"command":"` + quoted + `"}`
}

func TestClassifier_Tools(t *testing.T) {
	t.Parallel()

	tests := []struct {
		tool string
		want transcript.Class
	}{
		{"Read", transcript.ClassRetrieval},
		{"Grep", transcript.ClassRetrieval},
		{"Glob", transcript.ClassRetrieval},
		{"WebFetch", transcript.ClassRetrieval},
		{"Edit", transcript.ClassEdit},
		{"Write", transcript.ClassEdit},
		{"MultiEdit", transcript.ClassEdit},
		{"NotebookEdit", transcript.ClassEdit},
		{"Bash", transcript.ClassShell},
		{"Task", transcript.ClassOther},
		{"mcp__server__tool", transcript.ClassOther},
	}

	for _, tt := range tests {
		t.Run(tt.tool, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, classOf(readShell(t, tt.tool, `{}`)))
		})
	}
}

func classOf(s transcript.Stats) transcript.Class {
	switch {
	case s.Edit == 1:
		return transcript.ClassEdit
	case s.Shell == 1:
		return transcript.ClassShell
	case s.Retrieval == 1:
		return transcript.ClassRetrieval
	case s.Other == 1:
		return transcript.ClassOther
	default:
		return transcript.ClassProse
	}
}

func TestClassifier_ShellCommands(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		line string
		want transcript.Class
	}{
		{"list", "ls -la", transcript.ClassRetrieval},
		{"pipeline of readers", "cat a.go | head -20", transcript.ClassRetrieval},
		{"cd then search", "cd /work && rg 'a|b' internal", transcript.ClassRetrieval},
		{"absolute program path", "/usr/bin/grep -rn x .", transcript.ClassRetrieval},
		{"stderr discarded", "ls missing 2>/dev/null", transcript.ClassRetrieval},
		{"stderr joined", "git status 2>&1 | head", transcript.ClassRetrieval},
		{"all output discarded", "which go &>/dev/null", transcript.ClassRetrieval},
		{"git read", "git log --oneline -5", transcript.ClassRetrieval},
		{"go doc", "go doc fmt.Println", transcript.ClassRetrieval},
		{"sed print", "sed -n 1,20p a.go", transcript.ClassRetrieval},
		{"find", "find . -name '*.go'", transcript.ClassRetrieval},
		{"env prefix", "LC_ALL=C sort names.txt", transcript.ClassRetrieval},
		{"escaped space", `cat my\ file.txt`, transcript.ClassRetrieval},
		{"redirect into a file", "cat a.go > b.go", transcript.ClassShell},
		{"append into a file", "echo x >> notes.txt", transcript.ClassShell},
		{"sed in place", "sed -i s/a/b/ a.go", transcript.ClassShell},
		{"find delete", "find . -name '*.tmp' -delete", transcript.ClassShell},
		{"git write", "git commit -m msg", transcript.ClassShell},
		{"bare git", "git", transcript.ClassShell},
		{"reader then writer", "ls; rm -rf build", transcript.ClassShell},
		{"background writer", "ls & touch x", transcript.ClassShell},
		{"go test", "go test ./internal/x/...", transcript.ClassShell},
		{"only cd", "cd /work", transcript.ClassShell},
		{"empty", "", transcript.ClassShell},
		{"unterminated quote", "cat 'a.go", transcript.ClassRetrieval},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, classOf(readShell(t, "Bash", command(tt.line))))
		})
	}
}

func TestClassifier_Sweeps(t *testing.T) {
	t.Parallel()

	tests := []struct {
		want map[string]int
		name string
		line string
	}{
		{map[string]int{"go build": 1}, "go build whole tree", "go build ./..."},
		{map[string]int{"go test": 1}, "go test whole tree", "go test ./... 2>&1 | tail -20"},
		{map[string]int{"go test": 1}, "go test with flags", "go test -race -count=1 ./..."},
		{map[string]int{"go vet": 1}, "go vet", "go vet ./..."},
		{map[string]int{"go build": 1, "go test": 1}, "build and test", "go build ./... && go test ./..."},
		{map[string]int{"go test": 1}, "wrapped in mise", "mise exec -- go test ./..."},
		{map[string]int{"go test": 1}, "env and cd", "cd /work && CGO_ENABLED=0 go test ./..."},
		{map[string]int{"go test": 1}, "subshell", "(cd sub && go test ./...)"},
		{map[string]int{"golangci-lint run": 1}, "golangci bare", "golangci-lint run"},
		{map[string]int{"golangci-lint run": 1}, "golangci whole tree", "mise exec -- golangci-lint run ./..."},
		{map[string]int{"pytest": 1}, "bare pytest", "pytest"},
		{map[string]int{"pytest": 1}, "pytest quiet", "python -m pytest -q"},
		{map[string]int{"npm test": 1}, "npm test", "npm test"},
		{map[string]int{"npm run test": 1}, "npm run test", "npm run test"},
		{map[string]int{"cargo test": 1}, "cargo test", "cargo test"},
		{map[string]int{"make test": 1}, "make test", "make test"},
		{map[string]int{"hk check": 1}, "hk check", "hk check --all"},
		{nil, "go test one package", "go test ./internal/bench/..."},
		{nil, "go test one test", "go test ./... -run TestOne"},
		{nil, "go test here", "go test"},
		{nil, "pytest one file", "pytest tests/test_a.py"},
		{nil, "npm test one file", "npm test -- a.test.js"},
		{nil, "golangci one package", "golangci-lint run ./internal/x/..."},
		{nil, "quoted mention", `echo "go test ./..."`},
		{nil, "not go", "gofmt -l ."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := readShell(t, "Bash", command(tt.line))
			assert.Equal(t, tt.want, got.SweepCommands)
			assert.Equal(t, len(tt.want), got.Sweeps)
		})
	}
}

func TestClassifier_RefactorCalls(t *testing.T) {
	t.Parallel()

	tests := []struct {
		want map[string]int
		name string
		line string
	}{
		{map[string]int{"rename": 1}, "rename", "wavez rename Old New"},
		{map[string]int{"move": 1}, "move by path", "/opt/bin/wavez move Thing other.go"},
		{map[string]int{"delete": 1, "rename": 1}, "delete then rename", "wavez delete Dead && wavez rename A B"},
		{nil, "gate is not a refactor", "wavez gate"},
		{nil, "bare", "wavez"},
		{nil, "another program", "mv rename delete"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, readShell(t, "Bash", command(tt.line)).Refactors)
		})
	}
}

package tools_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/tool"
	"github.com/kyleking/wavez/internal/tools"
)

func TestStrReplace_Success(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "file.go")
	if err := os.WriteFile(path, []byte("package foo\n\nfunc a() {}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	s := tools.NewStrReplace(dir, nil)
	result, err := s.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "file.go", "old_string": "func a() {}", "new_string": "func b() {}",
	}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.IsError {
		t.Fatalf("IsError = true, want false: %q", result.Content)
	}
	if strings.Contains(result.Content, "func b") {
		t.Errorf("Content = %q, want terse line-count summary, not the diff", result.Content)
	}
	if !strings.Contains(result.Content, "+1") || !strings.Contains(result.Content, "-1") {
		t.Errorf("Content = %q, want it to report +1 -1 lines", result.Content)
	}
	if len(result.Changes) != 1 || result.Changes[0].Added != 1 || result.Changes[0].Removed != 1 {
		t.Errorf("Changes = %+v, want one change with Added=1 Removed=1", result.Changes)
	}
}

func TestStrReplace_NonUniqueErrorCarriesLineNumbers(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "file.go")
	content := "a := 1\nb := a + 1\na := 1\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	s := tools.NewStrReplace(dir, nil)
	result, err := s.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "file.go", "old_string": "a := 1", "new_string": "a := 2",
	}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.IsError {
		t.Fatalf("IsError = false, want true for a non-unique match")
	}
	if !strings.Contains(result.Content, "2 matches") {
		t.Errorf("Content = %q, want it to report 2 matches", result.Content)
	}
	if !strings.Contains(result.Content, "[1 3]") {
		t.Errorf("Content = %q, want it to name lines 1 and 3", result.Content)
	}
}

func TestStrReplace_RefusesPathOutsideRoot(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	s := tools.NewStrReplace(dir, nil)

	result, err := s.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "/etc/passwd", "old_string": "a", "new_string": "b",
	}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.IsError {
		t.Errorf("IsError = false, want true for a path outside the root")
	}
}

// A batch of edits with no path is a missing field, and reporting it as a
// containment failure sent one run looking for a path problem it did not
// have.
func TestStrReplace_NamesAMissingPath(t *testing.T) {
	t.Parallel()

	s := tools.NewStrReplace(t.TempDir(), nil)

	result, err := s.Run(context.Background(), mustJSON(t, map[string]any{
		"edits": []map[string]string{{"old_string": "a", "new_string": "b"}},
	}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.IsError || !strings.Contains(result.Content, "path is required") {
		t.Errorf("result = %+v, want an error naming the missing path", result)
	}

	// An empty call is missing its path too, and a hosted tier sends one:
	// it is not grammar-constrained, so nothing stops it. Naming the absent
	// replacement first would send the reader past the earlier absence.
	empty, err := s.Run(context.Background(), mustJSON(t, map[string]any{}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !empty.IsError || !strings.Contains(empty.Content, "path is required") {
		t.Errorf("result = %+v, want an empty call to name the missing path", empty)
	}
}

// One call per replacement costs a turn each, and the turns are what a run
// spends its budget on.
func TestStrReplace_AppliesSeveralEditsInOneCall(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "f.go")
	if err := os.WriteFile(path, []byte("package p\n\nconst A = 1\nconst B = 2\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	s := tools.NewStrReplace(dir, nil)
	result, err := s.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "f.go",
		"edits": []map[string]string{
			{"old_string": "const A = 1", "new_string": "const A = 10"},
			{"old_string": "const B = 2", "new_string": "const B = 20"},
		},
	}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.IsError {
		t.Fatalf("IsError = true: %q", result.Content)
	}
	if !strings.Contains(result.Content, "across 2 edits") {
		t.Errorf("Content = %q, want it to name the batch", result.Content)
	}

	data, err := os.ReadFile(path) //nolint:gosec // dir is a t.TempDir() fixture
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if got := string(data); !strings.Contains(got, "A = 10") || !strings.Contains(got, "B = 20") {
		t.Errorf("file = %q, want both edits applied", got)
	}

	both, err := s.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "f.go", "old_string": "x", "new_string": "y",
		"edits": []map[string]string{{"old_string": "a", "new_string": "b"}},
	}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !both.IsError || !strings.Contains(both.Content, "not both") {
		t.Errorf("Content = %q (IsError=%v), want the two shapes refused together", both.Content, both.IsError)
	}
}

// The taxonomy exists to aim work at a class of failure, and str_replace is
// where the failures are: it errored on 78 of 140 calls across this
// project's first 77 recorded runs. Ambiguity, a missing field, and a
// containment refusal are three different problems and were one number.
func TestStrReplace_ErrorsSayWhichKindOfFailureTheyAre(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "file.go")

	if err := os.WriteFile(path, []byte("a := 1\nb := a + 1\na := 1\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	s := tools.NewStrReplace(dir, nil)

	tests := []struct {
		input map[string]any
		name  string
		want  tool.Cause
	}{
		{
			name:  "text that occurs twice",
			input: map[string]any{"path": "file.go", "old_string": "a := 1", "new_string": "a := 2"},
			want:  tool.CauseAmbiguous,
		},
		{
			name:  "text that occurs nowhere",
			input: map[string]any{"path": "file.go", "old_string": "z := 9", "new_string": "z := 8"},
			want:  tool.CauseNoMatch,
		},
		{
			name:  "a path outside the project",
			input: map[string]any{"path": "/etc/passwd", "old_string": "a", "new_string": "b"},
			want:  tool.CauseRefused,
		},
		{
			name:  "no path at all",
			input: map[string]any{"edits": []map[string]string{{"old_string": "a", "new_string": "b"}}},
			want:  tool.CauseBadInput,
		},
		{
			name:  "a replacement with no new_string",
			input: map[string]any{"path": "file.go", "old_string": "b := a + 1"},
			want:  tool.CauseBadInput,
		},
		{
			name:  "an empty object",
			input: map[string]any{},
			want:  tool.CauseBadInput,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result, err := s.Run(context.Background(), mustJSON(t, tt.input))
			if err != nil {
				t.Fatalf("Run: %v", err)
			}

			if !result.IsError {
				t.Fatalf("IsError = false, want an error")
			}

			if result.Cause != tt.want {
				t.Errorf("Cause = %q, want %q (content: %s)", result.Cause, tt.want, result.Content)
			}
		})
	}
}

// A call that names old_string and no new_string is one cut short, not a
// deletion. Across this project's thread logs the fast tier sent 52 of
// them and no well-formed pair at all, and the one that matched deleted a
// README line and reported it as a change.
func TestStrReplace_AbsentNewStringNeverDeletes(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "file.go")
	source := "package foo\n\nfunc a() {}\n"

	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	s := tools.NewStrReplace(dir, nil)
	result, err := s.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "file.go", "old_string": "func a() {}",
	}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !result.IsError {
		t.Fatalf("IsError = false, want the call refused: %q", result.Content)
	}

	if !strings.Contains(result.Content, "new_string") {
		t.Errorf("Content = %q, want it to name new_string as what is missing", result.Content)
	}

	after, err := os.ReadFile(path) //nolint:gosec // dir is a t.TempDir() fixture
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	if string(after) != source {
		t.Errorf("file = %q, want it untouched at %q", after, source)
	}
}

// An empty new_string still deletes, since that is how a deletion is
// spelled once absence stops meaning it.
func TestStrReplace_EmptyNewStringDeletes(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "file.go")

	if err := os.WriteFile(path, []byte("package foo\n\nfunc a() {}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	s := tools.NewStrReplace(dir, nil)
	result, err := s.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "file.go", "old_string": "func a() {}", "new_string": "",
	}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.IsError {
		t.Fatalf("IsError = true, want the deletion applied: %q", result.Content)
	}

	after, err := os.ReadFile(path) //nolint:gosec // dir is a t.TempDir() fixture
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	if strings.Contains(string(after), "func a") {
		t.Errorf("file = %q, want func a deleted", after)
	}
}

// A local turn decodes tool arguments under a grammar compiled from this
// schema, so a property a branch leaves out of required is an exit the
// model can take mid-call. Measured against llama-server on qwen3:8b:
// asked for a path-only call, the schema as it was accepted one 6 times
// out of 6, and branches that require every property they declare forced
// the pair 5 times out of 5.
func TestStrReplace_EveryBranchRequiresEveryPropertyItDeclares(t *testing.T) {
	t.Parallel()

	var schema jsonSchema
	if err := json.Unmarshal(tools.NewStrReplace(t.TempDir(), nil).Schema(), &schema); err != nil {
		t.Fatalf("Schema() is not valid JSON: %v", err)
	}

	if len(schema.OneOf) < 2 {
		t.Fatalf("oneOf has %d branches, want the pair and the edits shapes stated separately", len(schema.OneOf))
	}

	for i, b := range schema.OneOf {
		required := make(map[string]bool, len(b.Required))
		for _, name := range b.Required {
			required[name] = true
		}

		for name := range b.Properties {
			if !required[name] {
				t.Errorf("branch %d leaves %q optional, which lets a call close without it", i, name)
			}
		}
	}
}

// A batch that anchors in two files fails with only "not found", which
// names nothing the model can change, so it resends the same call. One
// replay lane spent three of its eight turns that way: `path` was
// memory.go and two of the three edits anchored in memory_test.go.
func TestStrReplace_ABatchSaysItAppliesToOneFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package p\n\nfunc A() {}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	testFile := filepath.Join(dir, "a_test.go")
	if err := os.WriteFile(testFile, []byte("package p\n\nfunc TestA(t *testing.T) {}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	s := tools.NewStrReplace(dir, nil)
	result, err := s.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "a.go",
		"edits": []map[string]string{
			{"old_string": "func A() {}", "new_string": "func A() { _ = 1 }"},
			{"old_string": "func TestA(t *testing.T) {}", "new_string": "func TestA(t *testing.T) { _ = 1 }"},
		},
	}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !result.IsError {
		t.Fatalf("IsError = false, want the batch refused: %q", result.Content)
	}

	if !strings.Contains(result.Content, "a.go") || !strings.Contains(result.Content, "own path") {
		t.Errorf("Content = %q, want it to say the edit can name its own path", result.Content)
	}

	if result.Cause != tool.CauseNoMatch {
		t.Errorf("Cause = %q, want %q", result.Cause, tool.CauseNoMatch)
	}

	// The same call lands once the second edit says where it belongs. Every
	// recorded `e2` failure on the fast tier was this shape: a test file's
	// anchor in a call whose path was the source file.
	ok, err := s.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "a.go",
		"edits": []map[string]string{
			{"old_string": "func A() {}", "new_string": "func A() { _ = 1 }"},
			{
				"old_string": "func TestA(t *testing.T) {}",
				"new_string": "func TestA(t *testing.T) { _ = 1 }",
				"path":       "a_test.go",
			},
		},
	}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if ok.IsError {
		t.Fatalf("a two-file batch was refused: %q", ok.Content)
	}

	if len(ok.Changes) != 2 {
		t.Fatalf("Changes = %+v, want one per file", ok.Changes)
	}

	for _, want := range []string{"a.go", "a_test.go"} {
		if !strings.Contains(ok.Content, want) {
			t.Errorf("Content = %q, want it to report %s", ok.Content, want)
		}
	}
}

// A pair repeated exactly is the fast tier repeating itself, and applying
// it once is what the call asked for. Measured on `h6`, one run sent the
// same pair five times and the next sent it twice, and naming the
// repetition to the model did not stop it. A repeat carrying a different
// replacement is undecidable and stays refused.
func TestStrReplace_CollapsesARepeatedPairAndRefusesAConflictingOne(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")

	if err := os.WriteFile(path, []byte("package p\n\nfunc A() {}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	s := tools.NewStrReplace(dir, nil)
	same := map[string]string{"old_string": "func A() {}", "new_string": "func A() { _ = 1 }"}
	result, err := s.Run(context.Background(), mustJSON(t, map[string]any{
		"path":  "a.go",
		"edits": []map[string]string{same, same, same},
	}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.IsError {
		t.Fatalf("IsError = true, want the repeated pair applied once: %q", result.Content)
	}

	if strings.Contains(result.Content, "across") {
		t.Errorf("Content = %q, want it to report one edit rather than a batch", result.Content)
	}

	after, err := os.ReadFile(path) //nolint:gosec // dir is a t.TempDir() fixture
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	if got := string(after); !strings.Contains(got, "_ = 1") {
		t.Errorf("file = %q, want the edit applied", got)
	}
}

// A no-op edit beside a real one cost three h6 runs every change they had
// made, because the batch is atomic and one edit that replaces text with
// itself failed all of them.
func TestStrReplace_DropsANoOpEditAndKeepsTheRest(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")

	if err := os.WriteFile(path, []byte("package p\n\nfunc A() {}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	s := tools.NewStrReplace(dir, nil)
	result, err := s.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "a.go",
		"edits": []map[string]string{
			{"old_string": "func A() {}", "new_string": "func A() { _ = 1 }"},
			{"old_string": "package p", "new_string": "package p"},
		},
	}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.IsError {
		t.Fatalf("IsError = true, want the real edit applied: %q", result.Content)
	}

	after, err := os.ReadFile(path) //nolint:gosec // dir is a t.TempDir() fixture
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	if got := string(after); !strings.Contains(got, "_ = 1") {
		t.Errorf("file = %q, want the real edit applied", got)
	}

	// A batch with nothing but no-ops has nothing to apply and says so.
	empty, err := s.Run(context.Background(), mustJSON(t, map[string]any{
		"path":  "a.go",
		"edits": []map[string]string{{"old_string": "package p", "new_string": "package p"}},
	}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !empty.IsError || !strings.Contains(empty.Content, "nothing") {
		t.Errorf("result = %+v, want an all-no-op batch refused", empty)
	}
}

func TestStrReplace_RefusesABatchThatRepeatsAnAnchor(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")
	source := "package p\n\nfunc A() {}\n"

	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	s := tools.NewStrReplace(dir, nil)
	result, err := s.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "a.go",
		"edits": []map[string]string{
			{"old_string": "func A() {}", "new_string": "func A() { _ = 1 }"},
			{"old_string": "func A() {}", "new_string": "func A() { _ = 2 }"},
		},
	}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !result.IsError {
		t.Fatalf("IsError = false, want the batch refused: %q", result.Content)
	}

	if !strings.Contains(result.Content, "undecidable") {
		t.Errorf("Content = %q, want it to name the conflict", result.Content)
	}

	after, err := os.ReadFile(path) //nolint:gosec // dir is a t.TempDir() fixture
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	if string(after) != source {
		t.Errorf("file = %q, want it untouched at %q", after, source)
	}
}

// A pair whose two halves match is the single largest failure str_replace
// records, 81 of 322 across this project's thread logs, and mostly from the
// hosted tiers rather than the fast one. The error alone says only that the
// fields matched, which leaves the run to guess which of the two mistakes
// it made.
func TestStrReplace_TellsAnAlreadyWrittenFileFromALostAnchor(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "file.go"), []byte("a := 1\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	s := tools.NewStrReplace(dir, nil)

	tests := []struct {
		name string
		text string
		want string
	}{
		{name: "the file already reads that way", text: "a := 1", want: "already holds exactly that text"},
		{name: "the text is the replacement", text: "z := 9", want: "send the text it should replace"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result, err := s.Run(context.Background(), mustJSON(t, map[string]any{
				"path": "file.go", "old_string": tt.text, "new_string": tt.text,
			}))
			if err != nil {
				t.Fatalf("Run: %v", err)
			}

			if !result.IsError || result.Cause != tool.CauseBadInput {
				t.Fatalf("Cause = %q, IsError = %v, want bad_input error", result.Cause, result.IsError)
			}

			if !strings.Contains(result.Content, tt.want) {
				t.Errorf("content = %q, want it to contain %q", result.Content, tt.want)
			}
		})
	}
}

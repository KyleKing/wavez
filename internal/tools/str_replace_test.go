package tools_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
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

// The batch shape's own description says an edit may name its own path, and
// a local turn can only send what the schema declares, so a description
// promising what the grammar forbids is a capability that does not exist
// where it is needed most.
func TestStrReplace_ABatchEditCanDeclareItsOwnPath(t *testing.T) {
	t.Parallel()

	var schema jsonSchema
	if err := json.Unmarshal(tools.NewStrReplace(t.TempDir(), nil).Schema(), &schema); err != nil {
		t.Fatalf("Schema() is not valid JSON: %v", err)
	}

	var found bool

	for _, b := range schema.branches() {
		raw, ok := b.Properties["edits"]
		if !ok {
			continue
		}

		var edits struct {
			Items struct {
				Properties map[string]property `json:"properties"`
			} `json:"items"`
		}
		if err := json.Unmarshal(raw, &edits); err != nil {
			t.Fatalf("decoding the edits property: %v", err)
		}

		if _, ok := edits.Items.Properties["path"]; !ok {
			t.Errorf("an edit declares %v, want path among them", keysOf(edits.Items.Properties))
		}

		found = true
	}

	if !found {
		t.Fatal("no branch declares edits")
	}
}

func keysOf(m map[string]property) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}

	sort.Strings(out)

	return out
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

	// A batch of nothing but no-ops asked for text the file already holds,
	// which is the state it asked for.
	empty, err := s.Run(context.Background(), mustJSON(t, map[string]any{
		"path":  "a.go",
		"edits": []map[string]string{{"old_string": "package p", "new_string": "package p"}},
	}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if empty.IsError || !strings.Contains(empty.Content, "already holds") {
		t.Errorf("result = %+v, want the already-satisfied batch reported as done", empty)
	}
	if len(empty.Changes) != 0 {
		t.Errorf("Changes = %v, want none for a call that wrote nothing", empty.Changes)
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
// it made. Only one of them is a mistake: a call naming text the file
// already holds asked for the state the file is in, and reporting that as a
// failure sent one run round the same block three times.
func TestStrReplace_TellsAnAlreadyWrittenFileFromALostAnchor(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "file.go"), []byte("a := 1\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	s := tools.NewStrReplace(dir, nil)

	tests := []struct {
		name    string
		text    string
		want    string
		isError bool
	}{
		{
			name: "the file already reads that way",
			text: "a := 1",
			want: "already holds exactly that text",
		},
		{
			name:    "the text is the replacement",
			text:    "z := 9",
			want:    "send the text it should replace",
			isError: true,
		},
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

			if result.IsError != tt.isError {
				t.Fatalf("IsError = %v, want %v (%q)", result.IsError, tt.isError, result.Content)
			}
			if tt.isError && result.Cause != tool.CauseBadInput {
				t.Fatalf("Cause = %q, want bad_input", result.Cause)
			}
			if !strings.Contains(result.Content, tt.want) {
				t.Errorf("content = %q, want it to contain %q", result.Content, tt.want)
			}
		})
	}
}

// A part-finished rename leaves sites the anchor no longer matches and the
// replacement already does. The near-match report then faces the model with
// the suffix the two strings share, which reads as a typo: one h3 run took
// it that way and re-sent the same anchor.
func TestStrReplace_SaysAnEditIsAlreadyMade(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "file.go"),
		[]byte("events, err := bench.ReadLog(path)\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	s := tools.NewStrReplace(dir, nil)

	result, err := s.Run(context.Background(), mustJSON(t, map[string]any{
		"path":       "file.go",
		"old_string": "events, err := bench.Read(path)",
		"new_string": "events, err := bench.ReadLog(path)",
	}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !result.IsError || result.Cause != tool.CauseNoMatch {
		t.Fatalf("IsError = %v, Cause = %q, want a no_match failure", result.IsError, result.Cause)
	}

	if !strings.Contains(result.Content, "already been made") &&
		!strings.Contains(result.Content, "has been made") {
		t.Errorf("content = %q, want it to say the edit is already made", result.Content)
	}
}

// `ambiguous` went from 1 across the first corpus to 20, and every case is a
// rename asked as a text edit. The h3 shape: four sites written identically,
// a pair that changes one identifier, and no tool result anywhere naming the
// tool built for it. Where the new name is already declared the run has
// hand-edited the declaration, and the advice names the file holding it,
// which is the one argument a bare rename cannot supply while another
// package declares the old name.
func TestStrReplace_AmbiguousRenamePointsAtRename(t *testing.T) {
	t.Parallel()

	call := map[string]any{
		"path":       "file.go",
		"old_string": "events, err := bench.Read(path)",
		"new_string": "events, err := bench.ReadLog(path)",
	}
	callers := "package p\n\nfunc a() { events, err := bench.Read(path) }\n" +
		"func b() { events, err := bench.Read(path) }\n"

	tests := []struct {
		name     string
		declared string
		wantPath bool
	}{
		{
			name:     "declaration still named Read",
			declared: "func Read(path string) error { return nil }\n",
		},
		{
			name: "declaration already renamed, another package still declares Read",
			declared: "func ReadLog(path string) error { return nil }\n" +
				"type Read struct{}\n",
			wantPath: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "file.go"), []byte(callers), 0o600); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}

			index := openTestIndex(t, map[string]string{"bench/stats.go": "package bench\n\n" + tc.declared})
			s := tools.NewStrReplace(dir, nil, tools.WithSymbols(index))

			result, err := s.Run(context.Background(), mustJSON(t, call))
			if err != nil {
				t.Fatalf("Run: %v", err)
			}

			if !result.IsError || result.Cause != tool.CauseAmbiguous {
				t.Fatalf("IsError = %v, Cause = %q, want an ambiguous failure", result.IsError, result.Cause)
			}

			wantRenameAdvice(t, result.Content, tc.wantPath)
		})
	}
}

func wantRenameAdvice(t *testing.T, content string, wantPath bool) {
	t.Helper()

	for _, want := range []string{"rename", `symbol: "Read"`, `to: "ReadLog"`} {
		if !strings.Contains(content, want) {
			t.Errorf("content = %q, want it to carry %s", content, want)
		}
	}

	if got := strings.Contains(content, `path: "bench/stats.go"`); got != wantPath {
		t.Errorf("content = %q, naming the declaring file = %v, want %v", content, got, wantPath)
	}
}

// A pair that rewrites the text rather than substituting one name is an
// edit, and pointing it at rename would send it somewhere it cannot go.
func TestStrReplace_AmbiguousRewriteSaysNothingAboutRename(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	body := "package p\n\nfunc a() { x := 1 }\nfunc b() { x := 1 }\n"

	if err := os.WriteFile(filepath.Join(dir, "file.go"), []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	s := tools.NewStrReplace(dir, nil)

	result, err := s.Run(context.Background(), mustJSON(t, map[string]any{
		"path":       "file.go",
		"old_string": "x := 1",
		"new_string": "x, y := 1, 2",
	}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if strings.Contains(result.Content, "rename") {
		t.Errorf("content = %q, want no rename advice for a rewrite", result.Content)
	}
}

// json names Go types when a field arrives in the wrong shape
// ("[]tools.editPair"), which says nothing about the JSON a model has to
// change. Two logged runs sent edits as a string holding the array and got
// that message back, and neither changed the shape on the next attempt.
func TestStrReplace_AWrongShapeIsNamedInJSONTerms(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package p\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	result, err := tools.NewStrReplace(dir, nil).Run(context.Background(),
		json.RawMessage(`{"path":"a.go","edits":"[{\"old_string\":\"p\",\"new_string\":\"q\"}]"}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !result.IsError {
		t.Fatalf("IsError = false, want the call refused: %q", result.Content)
	}

	for _, want := range []string{"edits", "an array", "string"} {
		if !strings.Contains(result.Content, want) {
			t.Errorf("Content = %q, want it to mention %q", result.Content, want)
		}
	}

	if strings.Contains(result.Content, "tools.editPair") {
		t.Errorf("Content = %q, want no Go type name in a message a model reads", result.Content)
	}
}

// The h3 shape: a rename whose call sites are written identically, where
// "widen old_string to make it unique" asks for something the file cannot
// give. One replay lane died stagnant after two of these.
func TestStrReplace_ReplaceAllReachesIdenticalSites(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "a_test.go")
	source := "package p\n\nfunc one() {\n\tevents := bench.Read(path)\n}\n" +
		"\nfunc two() {\n\tevents := bench.Read(path)\n}\n"

	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	s := tools.NewStrReplace(dir, nil)
	args := map[string]any{
		"path": "a_test.go", "old_string": "bench.Read(path)", "new_string": "bench.ReadLog(path)",
	}

	refused, err := s.Run(context.Background(), mustJSON(t, args))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !refused.IsError || refused.Cause != tool.CauseAmbiguous {
		t.Fatalf("Cause = %q, IsError = %v, want an ambiguous refusal", refused.Cause, refused.IsError)
	}
	if !strings.Contains(refused.Content, "replace_all: true") {
		t.Errorf("content = %q, want the refusal to name the way through", refused.Content)
	}

	// A lane that read that sentence sent the flag as the string "true",
	// was refused for the type, and never sent the call again.
	for _, flag := range []any{true, "true"} {
		if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		args["replace_all"] = flag

		result, err := s.Run(context.Background(), mustJSON(t, args))
		if err != nil {
			t.Fatalf("Run with replace_all %#v: %v", flag, err)
		}

		if result.IsError {
			t.Fatalf("replace_all %#v refused: %q", flag, result.Content)
		}

		after, err := os.ReadFile(path) //nolint:gosec // dir is a t.TempDir() fixture
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}

		if n := strings.Count(string(after), "bench.ReadLog(path)"); n != 2 {
			t.Errorf("replace_all %#v left %q, want both call sites renamed", flag, string(after))
		}
	}
}

// A call carrying another tool's fields is that tool's call sent here. One
// lane sent source three times and another sent content three times, and
// both died stagnant reading about new_string.
func TestStrReplace_NamesTheToolThatOwnsFieldsItDoesNotHave(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		field string
		want  string
	}{
		{name: "source belongs to declare", field: "source", want: "declare"},
		{name: "content belongs to write", field: "content", want: "write"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := tools.NewStrReplace(t.TempDir(), nil)

			result, err := s.Run(context.Background(), mustJSON(t, map[string]any{
				"path": "a.go", tc.field: "func A() {}",
			}))
			if err != nil {
				t.Fatalf("Run: %v", err)
			}

			if !result.IsError || !strings.Contains(result.Content, tc.want) {
				t.Errorf("content = %q, want it to name %s", result.Content, tc.want)
			}
		})
	}
}

// The largest single cause of a wasted turn recorded here is an anchor
// taken from a read the run has since written over: 15 of 18 misses, and 6
// of them spent a further turn re-reading the file. The harness has the
// bytes, so both cases are answered with the text rather than with an
// instruction to go and fetch it, and the formatter runs inside the call so
// what is shown is what the next anchor will be matched against.
func TestStrReplace_ShowsTheTextRatherThanAskingForAReread(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "file.go")
	const src = "package p\n\nfunc truncate(s string, limit int) string {\n\treturn s[:limit]\n}\n"

	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	scope := tools.NewScope(false)
	scope.Observe(path)

	s := tools.NewStrReplace(dir, scope)

	// The edit needs unicode/utf8 and does not import it. goimports adds
	// the import inside the call, so `undefined: utf8` never reaches a gate
	// and the caller is shown the file it actually produced.
	result, err := s.Run(context.Background(), mustJSON(t, map[string]any{
		"path":       "file.go",
		"old_string": "\treturn s[:limit]",
		"new_string": "\tfor !utf8.RuneStart(s[limit]) {\n\t\tlimit--\n\t}\n\n\treturn s[:limit]",
	}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.IsError {
		t.Fatalf("IsError = true, want false: %q", result.Content)
	}

	after, err := os.ReadFile(path) //nolint:gosec // dir is a t.TempDir() fixture
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(after), `"unicode/utf8"`) {
		t.Errorf("file = %q, want the import goimports should have added", after)
	}
	if !strings.Contains(result.Content, `unicode/utf8`) {
		t.Errorf("content = %q, want the rewritten region shown", result.Content)
	}

	// Anchoring on the file as it was first read is now stale. The answer
	// carries what those lines hold, so the next call needs no read.
	stale, err := s.Run(context.Background(), mustJSON(t, map[string]any{
		"path":       "file.go",
		"old_string": "package p\n\nfunc truncate(s string, limit int) string {",
		"new_string": "package p\n\n// truncate cuts on a boundary.\nfunc truncate(s string, limit int) string {",
	}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !stale.IsError || stale.Cause != tool.CauseNoMatch {
		t.Fatalf("IsError=%v Cause=%q, want a no_match failure: %q", stale.IsError, stale.Cause, stale.Content)
	}
	if !strings.Contains(stale.Content, "now reads") {
		t.Errorf("content = %q, want the current text of the region", stale.Content)
	}
	if strings.Contains(stale.Content, "Read it again") {
		t.Errorf("content = %q, want the text rather than an instruction to re-read", stale.Content)
	}
}

// The loop's repeat detection reaches only a call that immediately follows
// its twin, and 4 of the 19 recorded re-sends since 2026-08-26 were adjacent.
// One run sent the same whole-declaration anchor at three separate turns,
// reading past the advice to use declare each time.
func TestStrReplace_RefusesAnAnchorItAlreadyRefusedForAnUnchangedFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "file.go")
	write(t, path, "package p\n\nfunc a() { x := 1 }\n")

	s := tools.NewStrReplace(dir, tools.NewScope(false))
	miss := map[string]any{"path": "file.go", "old_string": "func b()", "new_string": "func c()"}

	first := runEdit(t, s, miss)
	if !first.IsError || first.Cause == tool.CauseRepeat {
		t.Fatalf("first call = %+v, want the anchor's own refusal", first)
	}

	second := runEdit(t, s, miss)
	if second.Cause != tool.CauseRepeat {
		t.Errorf("second call Cause = %q, want %q: %s", second.Cause, tool.CauseRepeat, second.Content)
	}
	if !strings.Contains(second.Content, first.Content) {
		t.Errorf("the repeat dropped what the first refusal said:\n%s", second.Content)
	}

	// A run that changes the file and tries the same anchor is asking a
	// different question, and the answer is the anchor's own again.
	write(t, path, "package p\n\nfunc b() { x := 1 }\n")

	if again := runEdit(t, s, miss); again.IsError {
		t.Errorf("the anchor was refused after the file changed to hold it: %s", again.Content)
	}
}

func runEdit(t *testing.T, s *tools.StrReplace, args map[string]any) tool.Result {
	t.Helper()

	res, err := s.Run(t.Context(), mustJSON(t, args))
	if err != nil {
		t.Fatalf("Run(%v): %v", args, err)
	}

	return res
}

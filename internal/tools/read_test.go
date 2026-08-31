package tools_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/tool"
	"github.com/kyleking/wavez/internal/tools"
)

// A repeat read returns the content again, whole file or range. Answering
// with a reference sent the model to the shell for the same file four times
// out of four on a dogfood run; keeping the repeat out of the history is
// compaction's DedupeToolReads, which the model never sees.
func TestRead_RepeatReadReturnsTheContentAgain(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "file.go")
	if err := os.WriteFile(path, []byte("package foo\nfunc A() {}\nfunc B() {}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	r := tools.NewRead(dir, nil)
	ctx := context.Background()

	read := func(args map[string]any) string {
		t.Helper()
		res, err := r.Run(ctx, mustJSON(t, args))
		if err != nil {
			t.Fatalf("Run(%v): %v", args, err)
		}

		return res.Content
	}

	whole := map[string]any{"path": "file.go"}
	if first, second := read(whole), read(whole); first != second {
		t.Errorf("second whole-file read = %q, want the same content as the first %q", second, first)
	}

	ranged := map[string]any{"path": "file.go", "start_line": 2, "end_line": 3}
	if first, second := read(ranged), read(ranged); first != second || !strings.Contains(second, "func A() {}") {
		t.Errorf("second ranged read = %q, want the same content as the first %q", second, first)
	}
}

// A read naming a directory used to cost a turn to learn it was one, and
// the next call was always the listing.
func TestRead_OnADirectoryListsIt(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "pkg", "sub"), 0o750); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pkg", "a.go"), []byte("package pkg\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	r := tools.NewRead(dir, nil)
	result, err := r.Run(context.Background(), mustJSON(t, map[string]any{"path": "pkg"}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.IsError {
		t.Fatalf("IsError = true: %q", result.Content)
	}
	for _, want := range []string{"pkg is a directory", "a.go", "sub/"} {
		if !strings.Contains(result.Content, want) {
			t.Errorf("Content = %q, want it to hold %q", result.Content, want)
		}
	}
}

func TestRead_RefusesPathOutsideRoot(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	r := tools.NewRead(dir, nil)

	result, err := r.Run(context.Background(), mustJSON(t, map[string]any{"path": "../outside.go"}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.IsError {
		t.Errorf("IsError = false, want true for a path outside the root")
	}
	if !strings.Contains(result.Content, "outside the project root") {
		t.Errorf("Content = %q, want it to mention the project root", result.Content)
	}
}

func TestRead_LineRange(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "file.go")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\nfour\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	r := tools.NewRead(dir, nil)
	result, err := r.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "file.go", "start_line": 2, "end_line": 3,
	}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.IsError {
		t.Fatalf("IsError = true, want false: %q", result.Content)
	}
	if !strings.Contains(result.Content, "2\ttwo\n3\tthree") {
		t.Errorf("Content = %q, want lines two and three carrying their file line numbers", result.Content)
	}
	if strings.Contains(result.Content, "one") || strings.Contains(result.Content, "four") {
		t.Errorf("Content = %q, want lines outside the range excluded", result.Content)
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()

	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	return data
}

// The model routinely sends start_line alone. Rejecting that cost a whole turn.
func TestRead_OmittedEndLineReadsToEndOfFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "a.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\nfour\n"), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	res, err := tools.NewRead(root, nil).Run(t.Context(), json.RawMessage(`{"path":"a.txt","start_line":3}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.IsError {
		t.Fatalf("omitting end_line was rejected: %s", res.Content)
	}
	if !strings.Contains(res.Content, "three") || !strings.Contains(res.Content, "four") {
		t.Fatalf("did not read to end of file: %s", res.Content)
	}
	if strings.Contains(res.Content, "two") {
		t.Fatalf("read started before start_line: %s", res.Content)
	}
}

// A model that copies a numbered read back into an anchor or a new file
// would otherwise get "not found" with no clue why, or a file holding the
// numbers.
func TestNumberedTextIsRefusedAsFileContent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "file.go")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	sr := tools.NewStrReplace(dir, nil)
	result, err := sr.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "file.go", "old_string": "2\ttwo", "new_string": "TWO",
	}))
	if err != nil {
		t.Fatalf("str_replace Run: %v", err)
	}
	if !result.IsError || !strings.Contains(result.Content, "still carries the line numbers") {
		t.Errorf("str_replace Content = %q (IsError=%v), want the line-number hint", result.Content, result.IsError)
	}

	w := tools.NewWrite(dir, nil)
	result, err = w.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "new.go", "content": "1\tpackage x\n2\t\n3\tfunc A() {}\n",
	}))
	if err != nil {
		t.Fatalf("write Run: %v", err)
	}
	if !result.IsError || !strings.Contains(result.Content, "carries the line numbers") {
		t.Errorf("write Content = %q (IsError=%v), want the line-number refusal", result.Content, result.IsError)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "new.go")); statErr == nil {
		t.Error("write created the file despite refusing the content")
	}
}

// A model batches by repeating a JSON key or by listing paths in one string;
// decoding keeps only the last repeat, so a call meaning two files silently
// returned one.
func TestRead_ReadsSeveralFilesInOneCall(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for name, body := range map[string]string{
		"a.go": "package a\n",
		"b.go": "package b\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}

	r := tools.NewRead(dir, nil)
	run := func(input string) tool.Result {
		t.Helper()
		result, err := r.Run(context.Background(), json.RawMessage(input))
		if err != nil {
			t.Fatalf("Run(%s): %v", input, err)
		}

		return result
	}

	for _, input := range []string{
		`{"path":"a.go,b.go"}`,
		`{"path":"a.go","path":"b.go"}`,
	} {
		result := run(input)
		if result.IsError {
			t.Fatalf("Run(%s) errored: %q", input, result.Content)
		}
		if !strings.Contains(result.Content, "package a") || !strings.Contains(result.Content, "package b") {
			t.Errorf("Run(%s) = %q, want both files", input, result.Content)
		}
	}

	ranged := run(`{"path":"a.go,b.go","start_line":1,"end_line":1}`)
	if !ranged.IsError || !strings.Contains(ranged.Content, "give each path its own range") {
		t.Errorf("a range over two paths = %q (IsError=%v), want a refusal", ranged.Content, ranged.IsError)
	}
}

// assertCarries checks one result for the lines it must hold and the ones it
// must not.
func assertCarries(t *testing.T, content string, wants, lacks []string) {
	t.Helper()

	for _, w := range wants {
		if !strings.Contains(content, w) {
			t.Errorf("result = %q, want it to carry %q", content, w)
		}
	}

	for _, l := range lacks {
		if strings.Contains(content, l) {
			t.Errorf("result = %q, want it to leave out %q", content, l)
		}
	}
}

// A run reads a different range of each of two files by writing the range on
// the path. Without it that is two calls, and 39 of 94 back-to-back read
// sequences over this project's logs were exactly that shape.
func TestRead_RangeOnEachPath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	body := "one\ntwo\nthree\nfour\nfive\n"
	for _, name := range []string{"a.go", "b.go"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}

	r := tools.NewRead(dir, nil)

	tests := []struct {
		name    string
		input   string
		wants   []string
		lacks   []string
		isError bool
	}{
		{
			name:  "each path keeps its own range",
			input: `{"path":"a.go:2-3,b.go:5"}`,
			wants: []string{"2\ttwo", "3\tthree", "5\tfive"},
			lacks: []string{"1\tone", "4\tfour"},
		},
		{
			name:  "an open end reads to the end of that file",
			input: `{"path":"a.go:4-"}`,
			wants: []string{"4\tfour", "5\tfive"},
			lacks: []string{"3\tthree"},
		},
		{
			name:  "a path with no range follows the call's own",
			input: `{"path":"a.go","start_line":1,"end_line":1}`,
			wants: []string{"1\tone"},
			lacks: []string{"2\ttwo"},
		},
		{
			name:    "a backwards range is refused rather than silently widened",
			input:   `{"path":"a.go:4-2"}`,
			isError: true,
			wants:   []string{"1 <= start <= end"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := r.Run(context.Background(), json.RawMessage(tc.input))
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if got.IsError != tc.isError {
				t.Fatalf("IsError = %v, want %v (%q)", got.IsError, tc.isError, got.Content)
			}
			assertCarries(t, got.Content, tc.wants, tc.lacks)
		})
	}
}

// A whole-file read of a long file comes back as its outline. Measured over
// this project's thread logs, 522 whole-file reads of Go files that still
// exist cost 3,906 KB numbered, and answering the 148 of them past 300 lines
// with an outline costs 1,625 KB: 58% of the bytes for 28% of the calls.
func TestRead_OutlinesALongFileAndReadsAShortOneWhole(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	var long strings.Builder

	long.WriteString("package p\n\nimport \"fmt\"\n")

	for i := range 40 {
		fmt.Fprintf(&long, "\n// Fn%02d does a thing.\nfunc Fn%02d() { fmt.Println(%d) }\n", i, i, i)

		for range 5 {
			long.WriteString("// filler that makes this file worth outlining\n")
		}
	}

	write(t, filepath.Join(dir, "long.go"), long.String())
	write(t, filepath.Join(dir, "short.go"), "package p\n\nfunc Only() {}\n")

	r := tools.NewRead(dir, tools.NewScope(dir, false))

	read := func(args string) string {
		t.Helper()

		res, err := r.Run(t.Context(), []byte(args))
		if err != nil {
			t.Fatalf("Run(%s): %v", args, err)
		}

		return res.Content
	}

	outlined := read(`{"path":"long.go"}`)
	if strings.Contains(outlined, "fmt.Println(7)") {
		t.Errorf("a long file came back as its text:\n%s", outlined)
	}

	for _, want := range []string{"is 324 lines", "func Fn07()", "1\tpackage p"} {
		if !strings.Contains(outlined, want) {
			t.Errorf("the outline does not carry %q:\n%s", want, outlined)
		}
	}

	if whole := read(`{"path":"short.go"}`); !strings.Contains(whole, "func Only() {}") {
		t.Errorf("a short file was not read whole:\n%s", whole)
	}

	// A range says what it wants, so it is answered with the lines rather
	// than with the map of the file the caller already has.
	if ranged := read(`{"path":"long.go","start_line":1,"end_line":3}`); !strings.Contains(ranged, "import") {
		t.Errorf("a line range was outlined:\n%s", ranged)
	}
}

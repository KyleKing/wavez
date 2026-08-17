package mention_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/mention"
)

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()

	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func numberedLines(count int) string {
	var b strings.Builder
	for i := 1; i <= count; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}

	return b.String()
}

func expand(t *testing.T, root, prompt string, opts ...mention.Option) mention.Result {
	t.Helper()

	result, err := mention.New(root, nil, opts...).Expand(context.Background(), prompt)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}

	return result
}

func TestExpand_FileMention(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, root, "internal/lease/lease.go", "package lease\n\nconst TTL = 30\n")

	result := expand(t, root, "what does @internal/lease/lease.go do?")

	if len(result.Mentions) != 1 || result.Mentions[0].Kind != mention.KindFile {
		t.Fatalf("mentions = %+v", result.Mentions)
	}
	if result.Mentions[0].Truncated {
		t.Error("a 3-line file must not report truncation")
	}
	for _, want := range []string{
		"what does @internal/lease/lease.go do?",
		"@internal/lease/lease.go (file, 3 lines):",
		"const TTL = 30",
	} {
		if !strings.Contains(result.Prompt, want) {
			t.Errorf("prompt is missing %q:\n%s", want, result.Prompt)
		}
	}
}

func TestExpand_FileBudgets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		prompt     string
		opts       []mention.Option
		wantInSect []string
		wantAbsent string
		truncated  []bool
	}{
		{
			name:       "per mention budget truncates loudly",
			prompt:     "@big.go",
			opts:       []mention.Option{mention.WithFileLineBudget(5)},
			wantInSect: []string{"file, 40 lines, showing 1-5", "line 5", "[35 lines not shown"},
			wantAbsent: "line 6",
			truncated:  []bool{true},
		},
		{
			name:   "total budget stops the second mention",
			prompt: "@big.go and @small.go",
			opts: []mention.Option{
				mention.WithFileLineBudget(40),
				mention.WithTotalLineBudget(40),
			},
			wantInSect: []string{"@small.go (file): 1 line, not expanded", "budget for one prompt is already spent"},
			truncated:  []bool{false, true},
		},
		{
			name:       "binary content is never dumped",
			prompt:     "@bin.dat",
			wantInSect: []string{"binary file (4 bytes), not expanded"},
			wantAbsent: "\x00",
			truncated:  []bool{true},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			writeFile(t, root, "big.go", numberedLines(40))
			writeFile(t, root, "small.go", "package small\n")
			writeFile(t, root, "bin.dat", "a\x00b\n")

			result := expand(t, root, tc.prompt, tc.opts...)

			assertTruncation(t, result, tc.truncated)
			assertPrompt(t, result.Prompt, tc.wantInSect, tc.wantAbsent)
		})
	}
}

func assertTruncation(t *testing.T, result mention.Result, want []bool) {
	t.Helper()

	if len(result.Mentions) != len(want) {
		t.Fatalf("mentions = %+v", result.Mentions)
	}
	for i, w := range want {
		if result.Mentions[i].Truncated != w {
			t.Errorf("mention %d truncated = %v, want %v (%+v)", i, !w, w, result.Mentions[i])
		}
	}
}

func assertPrompt(t *testing.T, prompt string, want []string, absent string) {
	t.Helper()

	for _, w := range want {
		if !strings.Contains(prompt, w) {
			t.Errorf("prompt is missing %q:\n%s", w, prompt)
		}
	}
	if absent != "" && strings.Contains(prompt, absent) {
		t.Errorf("prompt should not contain %q:\n%s", absent, prompt)
	}
}

func TestExpand_UnresolvedIsReportedAndKept(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, root, "pkg/keep.go", "package pkg\n")

	tests := []struct {
		name       string
		prompt     string
		wantDetail string
	}{
		{"missing path", "@internal/gone.go", "no file at internal/gone.go"},
		{"directory", "@pkg", "pkg is a directory"},
		{"escapes root", "@../outside.go", "path is outside the project root"},
		{"absolute path", "@/etc/passwd", "path is outside the project root"},
		{"symbol without an index", "@TTL", "no code index is attached"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			result := expand(t, root, "please explain "+tc.prompt+" for me")

			unresolved := result.Unresolved()
			if len(unresolved) != 1 {
				t.Fatalf("unresolved = %+v", result.Mentions)
			}
			if !strings.Contains(unresolved[0].Detail, tc.wantDetail) {
				t.Errorf("detail = %q, want it to contain %q", unresolved[0].Detail, tc.wantDetail)
			}
			if !strings.Contains(result.Prompt, tc.prompt) {
				t.Errorf("an unresolved mention must stay in the prompt:\n%s", result.Prompt)
			}
			if !strings.Contains(result.Prompt, tc.wantDetail) {
				t.Errorf("the reason must reach the model:\n%s", result.Prompt)
			}
		})
	}
}

func TestExpand_MentionCountIsCapped(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	prompt := "@a.go @b.go @c.go @d.go"
	result := expand(t, root, prompt, mention.WithMaxMentions(2))

	if len(result.Mentions) != 4 {
		t.Fatalf("every reference must be reported, got %+v", result.Mentions)
	}
	if !strings.Contains(result.Prompt, "@c.go, @d.go: not expanded: a prompt expands at most 2 mentions") {
		t.Errorf("overflow was not reported:\n%s", result.Prompt)
	}
}

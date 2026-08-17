package mention_test

import (
	"context"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/mention"
)

// refsOf expands prompt against an empty root with no index, so every
// reference the grammar accepts comes back as an unresolved mention and the
// test observes exactly what was recognized.
func refsOf(t *testing.T, prompt string) []string {
	t.Helper()

	result, err := mention.New(t.TempDir(), nil).Expand(context.Background(), prompt)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}

	refs := make([]string, 0, len(result.Mentions))
	for _, m := range result.Mentions {
		refs = append(refs, m.Ref)
	}

	return refs
}

func TestExpand_Grammar(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		prompt string
		want   []string
	}{
		{"bare path", "look at @internal/lease/lease.go please", []string{"internal/lease/lease.go"}},
		{"bare symbol", "explain @TTL", []string{"TTL"}},
		{"qualified symbol", "explain @lease.TTL.", []string{"lease.TTL"}},
		{"at line start", "@Foo is the entry point", []string{"Foo"}},
		{"in parentheses", "the lease type (@Lease) expires", []string{"Lease"}},
		{"quoted", "open \"@a/b.go\" now", []string{"a/b.go"}},
		{"trailing comma", "@Foo, @Bar and @Baz", []string{"Foo", "Bar", "Baz"}},
		{"deduplicated", "@Foo then @Foo again", []string{"Foo"}},
		{"across lines", "first @Foo\nthen @internal/b.go", []string{"Foo", "internal/b.go"}},

		{"email", "mail kyle@example.com about it", nil},
		{"scp target", "copy from user@host:/srv/app", nil},
		{"decorator call", "@pytest.mark.parametrize(\"x\", [1])", nil},
		{"bare at", "it costs 5 @ once", nil},
		{"at end of text", "what is this @", nil},
		{"mid word", "foo@bar", nil},
		{"inline code span", "the tag `json:\"n\"` and `@Foo` stay literal", nil},
		{"fenced block", "look:\n```go\ntype T struct {\n\tN string `v:\"@required\"`\n}\n@Decorator\n```\n", nil},
		{"tilde fence", "~~~\n@Foo\n~~~\n", nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := refsOf(t, tc.prompt)
			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Errorf("refs = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestExpand_NoMentionsLeavesPromptAlone(t *testing.T) {
	t.Parallel()

	prompt := "mail kyle@example.com and read the docs"
	result, err := mention.New(t.TempDir(), nil).Expand(context.Background(), prompt)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if result.Prompt != prompt {
		t.Errorf("prompt was rewritten:\n%s", result.Prompt)
	}
	if len(result.Mentions) != 0 {
		t.Errorf("expected no mentions, got %+v", result.Mentions)
	}
}

func TestExpand_CanceledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := mention.New(t.TempDir(), nil).Expand(ctx, "@Foo"); err == nil {
		t.Fatal("expected a canceled context to fail the expansion")
	}
}

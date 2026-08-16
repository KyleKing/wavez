package palette

import "testing"

func TestKindNameTag(t *testing.T) {
	t.Parallel()
	r := &findResult{kind: findTag}
	if got := r.kindName(); got != "tag" {
		t.Errorf("expected %q, got %q", "tag", got)
	}
}

func TestKindNameRepo(t *testing.T) {
	t.Parallel()
	r := &findResult{kind: findRepo}
	if got := r.kindName(); got != "repo" {
		t.Errorf("expected %q, got %q", "repo", got)
	}
}

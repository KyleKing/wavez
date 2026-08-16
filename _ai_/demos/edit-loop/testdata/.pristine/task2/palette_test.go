package palette

import "testing"

func TestHomogeneousKindEmpty(t *testing.T) {
	t.Parallel()
	if got := homogeneousKind(nil); got != findAny {
		t.Errorf("expected findAny for empty results, got %v", got)
	}
}

func TestHomogeneousKindMixed(t *testing.T) {
	t.Parallel()
	results := []findResult{{kind: findRepo}, {kind: findBranch}}
	if got := homogeneousKind(results); got != findAny {
		t.Errorf("expected findAny for mixed results, got %v", got)
	}
}

func TestHomogeneousKindSame(t *testing.T) {
	t.Parallel()
	results := []findResult{{kind: findPR}, {kind: findPR}}
	if got := homogeneousKind(results); got != findPR {
		t.Errorf("expected findPR, got %v", got)
	}
}

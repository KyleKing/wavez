package astgrep_test

import (
	"testing"

	"github.com/kyleking/wavez/internal/astgrep"
)

func TestTrimForModel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		want astgrep.ModelFinding
		name string
		in   astgrep.Finding
	}{
		{
			name: "no fix",
			in: astgrep.Finding{
				RuleID:   "no-fmt-println",
				Severity: "error",
				Message:  "use the project logger instead of fmt.Println",
				Note:     "internal note ast-grep attaches, not shown to the model",
				File:     "internal/example/main.go",
				Start:    astgrep.Position{Line: 13, Column: 2},
			},
			want: astgrep.ModelFinding{
				RuleID:   "no-fmt-println",
				Message:  "use the project logger instead of fmt.Println",
				Location: "internal/example/main.go:13",
			},
		},
		{
			name: "with fix",
			in: astgrep.Finding{
				RuleID:  "wrap-errors",
				Message: "wrap errors with %w",
				File:    "internal/example/thing.go",
				Fix:     "fmt.Errorf(\"doing thing: %w\", err)",
				Start:   astgrep.Position{Line: 41, Column: 10},
			},
			want: astgrep.ModelFinding{
				RuleID:   "wrap-errors",
				Message:  "wrap errors with %w",
				Location: "internal/example/thing.go:41",
				Fix:      "fmt.Errorf(\"doing thing: %w\", err)",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := astgrep.TrimForModel(tt.in)
			if got != tt.want {
				t.Errorf("TrimForModel(%+v) = %+v, want %+v", tt.in, got, tt.want)
			}
		})
	}
}

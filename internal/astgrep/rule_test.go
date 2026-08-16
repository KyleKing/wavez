package astgrep_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kyleking/wavez/internal/astgrep"
)

func writeRule(t *testing.T, dir, name, content string) string {
	t.Helper()

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing rule fixture %s: %v", path, err)
	}

	return path
}

func TestLoadRuleFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		content   string
		wantField string
		want      astgrep.RuleFile
		wantErr   bool
	}{
		{
			name: "valid rule with fix",
			content: `
id: no-fmt-println
language: Go
severity: error
message: use the project logger instead of fmt.Println
rule:
  pattern: fmt.Println($$$ARGS)
fix: logger.Info($$$ARGS)
`,
			want: astgrep.RuleFile{
				ID:       "no-fmt-println",
				Language: "Go",
				Severity: "error",
				Message:  "use the project logger instead of fmt.Println",
				HasFix:   true,
			},
		},
		{
			name: "valid rule without fix",
			content: `
id: no-raw-sql
language: Go
message: no raw SQL outside db/
rule:
  pattern: db.Query($SQL)
`,
			want: astgrep.RuleFile{
				ID:       "no-raw-sql",
				Language: "Go",
				Message:  "no raw SQL outside db/",
			},
		},
		{
			name: "missing id",
			content: `
language: Go
message: wrap errors with %w
rule:
  pattern: return $ERR
`,
			wantErr:   true,
			wantField: "id",
		},
		{
			name: "missing rule",
			content: `
id: wrap-errors
language: Go
message: wrap errors with %w
`,
			wantErr:   true,
			wantField: "rule",
		},
		{
			name: "malformed yaml",
			content: `
id: [this is not
`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			runLoadRuleFileCase(t, tt.content, tt.want, tt.wantErr, tt.wantField)
		})
	}
}

func runLoadRuleFileCase(t *testing.T, content string, want astgrep.RuleFile, wantErr bool, wantField string) {
	t.Helper()

	dir := t.TempDir()
	path := writeRule(t, dir, "rule.yml", content)

	got, err := astgrep.LoadRuleFile(path)
	if wantErr {
		if err == nil {
			t.Fatalf("LoadRuleFile(%s): want error, got nil", path)
		}

		if wantField != "" && !errors.Is(err, astgrep.ErrInvalidRule) {
			t.Errorf("LoadRuleFile(%s) error = %v, want wrapping ErrInvalidRule", path, err)
		}

		return
	}

	if err != nil {
		t.Fatalf("LoadRuleFile(%s): %v", path, err)
	}

	want.Path = path
	if got != want {
		t.Errorf("LoadRuleFile(%s) = %+v, want %+v", path, got, want)
	}
}

func TestLoadRuleFile_MissingFile(t *testing.T) {
	t.Parallel()

	if _, err := astgrep.LoadRuleFile(filepath.Join(t.TempDir(), "missing.yml")); err == nil {
		t.Fatal("LoadRuleFile on a missing file: want error, got nil")
	}
}

func TestLoadRuleFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeRule(t, dir, "a.yml", `
id: rule-a
language: Go
message: a
rule:
  pattern: a
`)
	writeRule(t, dir, "b.yml", `
id: rule-b
language: Go
message: b
rule:
  pattern: b
`)
	writeRule(t, dir, "not-a-rule.txt", "ignored by the glob, not by content")

	got, err := astgrep.LoadRuleFiles([]string{filepath.Join(dir, "*.yml")})
	if err != nil {
		t.Fatalf("LoadRuleFiles: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("LoadRuleFiles returned %d rules, want 2: %+v", len(got), got)
	}

	if got[0].ID != "rule-a" || got[1].ID != "rule-b" {
		t.Errorf("LoadRuleFiles IDs = [%s, %s], want [rule-a, rule-b] (sorted by path)", got[0].ID, got[1].ID)
	}
}

func TestLoadRuleFiles_GlobMatchesNothing(t *testing.T) {
	t.Parallel()

	got, err := astgrep.LoadRuleFiles([]string{filepath.Join(t.TempDir(), "*.yml")})
	if err != nil {
		t.Fatalf("LoadRuleFiles with an empty glob: %v", err)
	}

	if len(got) != 0 {
		t.Errorf("LoadRuleFiles with an empty glob = %+v, want empty", got)
	}
}

func TestLoadRuleFiles_PropagatesInvalidRule(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeRule(t, dir, "bad.yml", `
language: Go
message: missing id
rule:
  pattern: x
`)

	if _, err := astgrep.LoadRuleFiles([]string{filepath.Join(dir, "*.yml")}); !errors.Is(err, astgrep.ErrInvalidRule) {
		t.Errorf("LoadRuleFiles error = %v, want wrapping ErrInvalidRule", err)
	}
}

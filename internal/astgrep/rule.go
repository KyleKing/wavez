package astgrep

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ErrInvalidRule is wrapped by LoadRuleFile when a rule file is missing a
// field the runner needs before it can invoke ast-grep against it.
var ErrInvalidRule = errors.New("astgrep: invalid rule file")

// RuleFile is one project convention rule, validated but not itself
// executed: ast-grep does the pattern matching, this package only checks
// the file is well-formed enough to hand to it.
type RuleFile struct {
	Path     string
	ID       string
	Language string
	Message  string
	Severity string
	HasFix   bool
}

// yamlRule is the subset of ast-grep's rule schema
// (https://ast-grep.github.io/reference/yaml.html) this package validates.
// Rule and Fix are left as yaml.Node so their arbitrary nested shape is
// preserved for ast-grep to interpret, while still letting this loader
// detect whether either was set at all.
type yamlRule struct {
	ID       string    `yaml:"id"`
	Language string    `yaml:"language"`
	Message  string    `yaml:"message"`
	Severity string    `yaml:"severity"`
	Rule     yaml.Node `yaml:"rule"`
	Fix      yaml.Node `yaml:"fix"`
}

// LoadRuleFile parses and validates one rule YAML file, failing with a
// message naming every missing required field rather than letting a typo
// surface later as an opaque ast-grep exit code.
func LoadRuleFile(path string) (RuleFile, error) {
	//nolint:gosec // path is caller-supplied project configuration, not attacker-controlled input
	data, err := os.ReadFile(path)
	if err != nil {
		return RuleFile{}, fmt.Errorf("reading rule file %s: %w", path, err)
	}

	var r yamlRule
	if err := yaml.Unmarshal(data, &r); err != nil {
		return RuleFile{}, fmt.Errorf("parsing rule file %s: %w", path, err)
	}

	if missing := missingFields(r); len(missing) > 0 {
		return RuleFile{}, fmt.Errorf("%w: %s missing required field(s): %s",
			ErrInvalidRule, path, strings.Join(missing, ", "))
	}

	return RuleFile{
		Path:     path,
		ID:       r.ID,
		Language: r.Language,
		Message:  r.Message,
		Severity: r.Severity,
		HasFix:   r.Fix.Kind != 0,
	}, nil
}

func missingFields(r yamlRule) []string {
	var missing []string

	if r.ID == "" {
		missing = append(missing, "id")
	}

	if r.Language == "" {
		missing = append(missing, "language")
	}

	if r.Rule.Kind == 0 {
		missing = append(missing, "rule")
	}

	if r.Message == "" {
		missing = append(missing, "message")
	}

	return missing
}

// LoadRuleFiles expands each glob pattern relative to the caller's working
// directory, loads and validates every matched file, and returns them
// sorted and de-duplicated by path. A pattern matching nothing is not an
// error: an empty glob is how a project opts a language out of convention
// rules.
func LoadRuleFiles(patterns []string) ([]RuleFile, error) {
	seen := make(map[string]struct{})

	var paths []string

	for _, p := range patterns {
		matches, err := filepath.Glob(p)
		if err != nil {
			return nil, fmt.Errorf("expanding rule glob %q: %w", p, err)
		}

		for _, m := range matches {
			if _, ok := seen[m]; ok {
				continue
			}

			seen[m] = struct{}{}

			paths = append(paths, m)
		}
	}

	sort.Strings(paths)

	rules := make([]RuleFile, 0, len(paths))

	for _, p := range paths {
		rf, err := LoadRuleFile(p)
		if err != nil {
			return nil, err
		}

		rules = append(rules, rf)
	}

	return rules, nil
}

package config

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"
)

// problemEmpty is what is wrong with a required key that holds nothing.
const problemEmpty = "is empty"

// rawConfig mirrors the file's shape before validation. Pointers and
// strings stand in for values whose absence means a default.
type rawConfig struct {
	MaxRounds *int `toml:"max_rounds"`
	// Announce is a pointer because leaving it out means true.
	Announce *bool `toml:"announce"`
	// Parallel is a pointer because leaving it out means one per CPU.
	Parallel     *int       `toml:"parallel"`
	Decide       rawDecide  `toml:"decide"`
	StopBudget   string     `toml:"stop_budget"`
	Disable      []string   `toml:"disable"`
	Checks       []rawCheck `toml:"check"`
	Route        bool       `toml:"route"`
	StopChecks   bool       `toml:"stop_checks"`
	CommitChecks bool       `toml:"commit_checks"`
}

type rawDecide struct {
	Endpoint string `toml:"endpoint"`
	Model    string `toml:"model"`
	Timeout  string `toml:"timeout"`
}

type rawCheck struct {
	// Inputs is a pointer because an empty list declares that the command
	// reads nothing beyond what its globs match, and leaving it out says
	// nothing.
	Inputs    *[]string `toml:"inputs"`
	Name      string    `toml:"name"`
	Command   string    `toml:"command"`
	Fix       string    `toml:"fix"`
	Level     string    `toml:"level"`
	Stage     string    `toml:"stage"`
	Timeout   string    `toml:"timeout"`
	Globs     []string  `toml:"globs"`
	Exclusive bool      `toml:"exclusive"`
}

func (r rawConfig) parse() (Config, error) {
	cfg := Config{
		MaxRounds: DefaultMaxRounds, Disabled: r.Disable, Route: r.Route, Announce: true, StopChecks: r.StopChecks,
		CommitChecks: r.CommitChecks,
	}

	if r.Announce != nil {
		cfg.Announce = *r.Announce
	}

	if err := r.parseLimits(&cfg); err != nil {
		return Config{}, err
	}

	decide, err := r.Decide.parse()
	if err != nil {
		return Config{}, err
	}

	cfg.Decide = decide

	if r.MaxRounds != nil {
		if *r.MaxRounds < 1 {
			return Config{}, &Error{Key: "max_rounds", Problem: "must be at least 1"}
		}

		cfg.MaxRounds = *r.MaxRounds
	}

	for _, name := range r.Disable {
		if !slices.Contains(Builtins(), name) {
			return Config{}, &Error{
				Key:     "disable",
				Problem: fmt.Sprintf("names %q, which is not a built-in (%s)", name, strings.Join(Builtins(), ", ")),
			}
		}
	}

	seen := map[string]bool{}

	for i := range r.Checks {
		check, err := r.Checks[i].parse(i)
		if err != nil {
			return Config{}, err
		}

		switch {
		case seen[check.Name]:
			return Config{}, &Error{Key: checkKey(check.Name, "name"), Problem: "is declared twice"}
		case slices.Contains(Builtins(), check.Name) && cfg.Enabled(check.Name):
			return Config{}, &Error{
				Key:     checkKey(check.Name, "name"),
				Problem: "is a built-in; add it to disable to replace it",
			}
		}

		seen[check.Name] = true

		cfg.Checks = append(cfg.Checks, check)
	}

	return cfg, nil
}

// parseLimits reads the keys that bound a run: how many checks at once and
// how long in all.
func (r rawConfig) parseLimits(cfg *Config) error {
	if r.Parallel != nil {
		if *r.Parallel < 1 {
			return &Error{Key: "parallel", Problem: "must be at least 1"}
		}

		cfg.Parallel = *r.Parallel
	}

	if r.StopBudget != "" {
		budget, err := time.ParseDuration(r.StopBudget)
		if err != nil || budget <= 0 {
			return &Error{
				Key:     "stop_budget",
				Problem: fmt.Sprintf("is %q, want a positive duration such as 90s or 2m30s", r.StopBudget),
			}
		}

		cfg.StopBudget = budget
	}

	return nil
}

func (r rawDecide) parse() (Decide, error) {
	decide := Decide{
		Endpoint: strings.TrimSpace(r.Endpoint),
		Model:    strings.TrimSpace(r.Model),
		Timeout:  DefaultDecideTimeout,
	}

	if decide.Endpoint != "" {
		parsed, err := url.Parse(decide.Endpoint)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return Decide{}, &Error{
				Key:     "decide.endpoint",
				Problem: fmt.Sprintf("is %q, want an http or https URL such as http://localhost:11434/v1", r.Endpoint),
			}
		}

		if decide.Model == "" {
			return Decide{}, &Error{Key: "decide.model", Problem: "is empty, and an endpoint needs a model to ask"}
		}
	}

	if r.Timeout == "" {
		return decide, nil
	}

	timeout, err := time.ParseDuration(r.Timeout)
	if err != nil || timeout <= 0 {
		return Decide{}, &Error{
			Key:     "decide.timeout",
			Problem: fmt.Sprintf("is %q, want a positive duration such as 500ms or 3s", r.Timeout),
		}
	}

	decide.Timeout = timeout

	return decide, nil
}

func (r rawCheck) parse(index int) (Check, error) {
	if strings.TrimSpace(r.Name) == "" {
		return Check{}, &Error{Key: fmt.Sprintf("check[%d].name", index), Problem: problemEmpty}
	}

	if strings.TrimSpace(r.Command) == "" {
		return Check{}, &Error{Key: checkKey(r.Name, "command"), Problem: problemEmpty}
	}

	if len(r.Globs) == 0 {
		return Check{}, &Error{Key: checkKey(r.Name, "globs"), Problem: "is empty, so no change would select the check"}
	}

	check := Check{
		Name: r.Name, Command: r.Command, Fix: r.Fix, Exclusive: r.Exclusive,
		Level: LevelBlock, Stage: StageStop, Timeout: DefaultTimeout,
	}

	globs, err := parseGlobs(r.Name, "globs", r.Globs)
	if err != nil {
		return Check{}, err
	}

	check.Globs = globs

	if r.Inputs != nil {
		inputs, err := parseGlobs(r.Name, "inputs", *r.Inputs)
		if err != nil {
			return Check{}, err
		}

		check.Inputs, check.Cacheable = inputs, true
	}

	if err := r.parseEnums(&check); err != nil {
		return Check{}, err
	}

	return check, nil
}

func parseGlobs(name, key string, patterns []string) ([]Glob, error) {
	globs := make([]Glob, 0, len(patterns))

	for _, pattern := range patterns {
		glob, err := ParseGlob(pattern)
		if err != nil {
			return nil, &Error{Key: checkKey(name, key), Problem: err.Error()}
		}

		globs = append(globs, glob)
	}

	return globs, nil
}

func (r rawCheck) parseEnums(check *Check) error {
	switch Level(r.Level) {
	case "":
	case LevelBlock, LevelAdvise:
		check.Level = Level(r.Level)
	default:
		return &Error{Key: checkKey(r.Name, "level"), Problem: fmt.Sprintf("is %q, want block or advise", r.Level)}
	}

	switch Stage(r.Stage) {
	case "":
	case StageEdit, StageStop:
		check.Stage = Stage(r.Stage)
	default:
		return &Error{Key: checkKey(r.Name, "stage"), Problem: fmt.Sprintf("is %q, want edit or stop", r.Stage)}
	}

	if r.Timeout == "" {
		return nil
	}

	timeout, err := time.ParseDuration(r.Timeout)
	if err != nil || timeout <= 0 {
		return &Error{
			Key:     checkKey(r.Name, "timeout"),
			Problem: fmt.Sprintf("is %q, want a positive duration such as 90s or 2m", r.Timeout),
		}
	}

	check.Timeout = timeout

	return nil
}

func checkKey(name, key string) string {
	return fmt.Sprintf("check %q %s", name, key)
}

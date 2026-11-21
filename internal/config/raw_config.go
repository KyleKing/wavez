package config

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// problemEmpty is what is wrong with a required key that holds nothing.
const problemEmpty = "is empty"

// rawConfig mirrors the file's shape before validation. Pointers and
// strings stand in for values whose absence means a default.
type rawConfig struct {
	MaxRounds *int       `toml:"max_rounds"`
	Disable   []string   `toml:"disable"`
	Checks    []rawCheck `toml:"check"`
}

type rawCheck struct {
	Name    string   `toml:"name"`
	Command string   `toml:"command"`
	Fix     string   `toml:"fix"`
	Level   string   `toml:"level"`
	Stage   string   `toml:"stage"`
	Timeout string   `toml:"timeout"`
	Globs   []string `toml:"globs"`
}

func (r rawConfig) parse() (Config, error) {
	cfg := Config{MaxRounds: DefaultMaxRounds, Disabled: r.Disable}

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

	for i, rc := range r.Checks {
		check, err := rc.parse(i)
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
		Name: r.Name, Command: r.Command, Fix: r.Fix,
		Level: LevelBlock, Stage: StageStop, Timeout: DefaultTimeout,
	}

	for _, pattern := range r.Globs {
		glob, err := ParseGlob(pattern)
		if err != nil {
			return Check{}, &Error{Key: checkKey(r.Name, "globs"), Problem: err.Error()}
		}

		check.Globs = append(check.Globs, glob)
	}

	if err := r.parseEnums(&check); err != nil {
		return Check{}, err
	}

	return check, nil
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

// Package config loads a project's optional .wavez.toml: the checks its
// changed files select, and which built-in checks it has turned off.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/BurntSushi/toml"
)

// FileName is the configuration file read from a project root.
const FileName = ".wavez.toml"

// Defaults a check or a project takes for a key it leaves out.
const (
	DefaultMaxRounds = 3
	DefaultTimeout   = 2 * time.Minute
	// DefaultDecideTimeout bounds one call to the decision model. A hook
	// waits on it, so it is short.
	DefaultDecideTimeout = 3 * time.Second
)

// Level says whether a failing check holds the agent.
type Level string

// The levels a check can declare. An advisory failure is reported and never
// blocks.
const (
	LevelBlock  Level = "block"
	LevelAdvise Level = "advise"
)

// Stage says when a check runs: after every write, or once before the agent
// finishes its turn.
type Stage string

// The stages a check can declare.
const (
	StageEdit Stage = "edit"
	StageStop Stage = "stop"
)

// The built-in checks a project with a go.mod gets without declaring them.
// A project turns one off by naming it under disable.
const (
	BuiltinFormat = "format"
	BuiltinBuild  = "build"
	BuiltinTest   = "test"
	BuiltinLint   = "lint"
)

// Builtins lists the built-in check names in the order they report.
func Builtins() []string {
	return []string{BuiltinFormat, BuiltinBuild, BuiltinTest, BuiltinLint}
}

// Check is one project-declared command and the changed files that select
// it.
type Check struct {
	Name string
	// Command runs through `sh -c` in the project root. Each `{files}` in
	// it is replaced by the matched changed files, shell-quoted.
	Command string
	// Fix is optional and runs before Command with the same substitution,
	// so a finding a tool can repair itself never reaches the agent.
	Fix   string
	Level Level
	Stage Stage
	Globs []Glob
	// Inputs match the other files Command reads, such as its own
	// configuration and the lockfile that pins its version. A pass is
	// reused only for a check that declares them, and only while every
	// file the globs or the inputs match is byte for byte what it was.
	Inputs  []Glob
	Timeout time.Duration
	// Cacheable is true when the check declared its inputs, even as an
	// empty list.
	Cacheable bool
	// Exclusive keeps every other check from running while this one
	// does, for a command that contends with others over a port, a
	// database, or a directory.
	Exclusive bool
}

// Select returns the paths in changed that any of the check's globs match,
// in the order given.
func (c Check) Select(changed []string) []string {
	var out []string

	for _, path := range changed {
		if slices.ContainsFunc(c.Globs, func(g Glob) bool { return g.Match(path) }) {
			out = append(out, path)
		}
	}

	return out
}

// Config is a project's parsed configuration. The zero value is what a
// project with no file gets, apart from MaxRounds, which Load fills in.
type Config struct {
	Decide     Decide
	Checks     []Check
	Disabled   []string
	MaxRounds  int
	StopBudget time.Duration
	// Parallel bounds how many checks run at once after the fixers. Zero
	// means one per CPU.
	Parallel   int
	StopChecks bool
	Route      bool
	Announce   bool
}

// Decide locates the decision model: an OpenAI-compatible chat completions
// endpoint, which is what a local model server exposes.
//
// The model answers with one letter inside a few tokens and a few seconds,
// so it should be a non-thinking model, or a hybrid one served with thinking
// off. Every request asks the server to turn thinking off, and a model that
// thinks anyway spends its tokens before it answers and falls back.
//
// This file is checked in, so it is the repository speaking and not the
// person running wavez. An endpoint set here is asked only when its host is
// loopback, and is never sent an API key. Any other host, and any key, has
// to be named by the user's own environment, where WAVEZ_DECIDE_ENDPOINT
// replaces Endpoint and WAVEZ_DECIDE_API_KEY is sent only to it. The rule
// is enforced where the model is asked, and this package only carries the
// values.
type Decide struct {
	// Endpoint is the base URL, such as http://localhost:11434/v1. Empty
	// means no model.
	Endpoint string
	// Model is the name the endpoint serves it under. Load requires it
	// whenever Endpoint is set.
	Model string
	// Timeout bounds one call. Load fills in DefaultDecideTimeout.
	Timeout time.Duration
}

// Enabled reports whether the built-in check name has not been disabled.
func (c Config) Enabled(name string) bool {
	return !slices.Contains(c.Disabled, name)
}

// Load reads root's .wavez.toml. A missing file is not an error and yields
// the defaults; a file that is present and wrong fails with an *Error naming
// the key, so a typo never silently turns a check off.
func Load(root string) (Config, error) {
	path := filepath.Join(root, FileName)

	data, err := fs.ReadFile(os.DirFS(root), FileName)
	if errors.Is(err, fs.ErrNotExist) {
		return Config{MaxRounds: DefaultMaxRounds, Announce: true, Decide: Decide{Timeout: DefaultDecideTimeout}}, nil
	}

	if err != nil {
		return Config{}, fmt.Errorf("reading %s: %w", path, err)
	}

	return Parse(path, data)
}

// Parse reads the body of a configuration file, naming path in its errors.
func Parse(path string, data []byte) (Config, error) {
	var raw rawConfig

	meta, err := toml.Decode(string(data), &raw)
	if err != nil {
		return Config{}, fmt.Errorf("parsing %s: %w", path, err)
	}

	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		return Config{}, &Error{Path: path, Key: undecoded[0].String(), Problem: "is not a known key"}
	}

	cfg, err := raw.parse()
	if err != nil {
		var invalid *Error
		if errors.As(err, &invalid) {
			invalid.Path = path
		}

		return Config{}, err
	}

	return cfg, nil
}

// Error is one invalid key in a configuration file.
type Error struct {
	// Path is the file the key was read from.
	Path string
	// Key is the dotted key, with a check named by its name when it has
	// one and by its position when it does not.
	Key string
	// Problem says what is wrong with the key's value.
	Problem string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s %s", e.Path, e.Key, e.Problem)
}

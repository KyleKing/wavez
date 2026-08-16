package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	gateTest   = "test"
	gateLint   = "lint"
	gateFormat = "format"
)

// Inference is a set of gate commands guessed from a project file, per
// DESIGN.md's Gates section ("Config discovered from package.json,
// Makefile, pyproject.toml, mise.toml, with a one-time prompt to confirm").
// A caller must show it to the user before acting on it; Discover and Load
// never adopt it silently.
type Inference struct {
	// Commands maps a gate name ("test", "lint", "format") to the shell
	// command line inferred for it.
	Commands map[string]string
	// Source is the project file the inference came from.
	Source string
}

// discoverers runs in DESIGN.md's listed order; the first file found on
// disk wins, so a project with more than one of these is not double-guessed
// from two ecosystems at once.
var discoverers = []struct {
	invoke func(path string) (map[string]string, error)
	file   string
}{
	{discoverPackageJSON, "package.json"},
	{discoverMakefile, "Makefile"},
	{discoverPyproject, "pyproject.toml"},
	{discoverMise, "mise.toml"},
}

// Discover infers gate commands from the first of package.json, Makefile,
// pyproject.toml, or mise.toml found under root. It reports ok=false when
// none of those files exist or none yields a recognizable command.
func Discover(root string) (Inference, bool) {
	for _, d := range discoverers {
		path := filepath.Join(root, d.file)
		if _, err := os.Stat(path); err != nil {
			continue
		}

		commands, err := d.invoke(path)
		if err != nil || len(commands) == 0 {
			continue
		}

		return Inference{Source: d.file, Commands: commands}, true
	}

	return Inference{}, false
}

var gateScriptNames = map[string][]string{
	gateTest:   {gateTest},
	gateLint:   {gateLint},
	gateFormat: {gateFormat, "fmt"},
}

func discoverPackageJSON(path string) (map[string]string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is a caller-configured project file
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	var manifest struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}

	names := make(map[string]struct{}, len(manifest.Scripts))
	for name := range manifest.Scripts {
		names[name] = struct{}{}
	}

	return commandsForNames(names, "npm run "), nil
}

var makeTargetPattern = regexp.MustCompile(`(?m)^([a-zA-Z0-9_-]+):`)

func discoverMakefile(path string) (map[string]string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is a caller-configured project file
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	targets := matchedNames(makeTargetPattern, data)

	return commandsForNames(targets, "make "), nil
}

func discoverPyproject(path string) (map[string]string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is a caller-configured project file
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	commands := map[string]string{gateTest: "pytest"}

	if strings.Contains(string(data), "ruff") {
		commands[gateLint] = "ruff check ."
		commands[gateFormat] = "ruff format ."
	}

	return commands, nil
}

var miseTaskPattern = regexp.MustCompile(`(?m)^\[tasks\.([a-zA-Z0-9_-]+)\]`)

func discoverMise(path string) (map[string]string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is a caller-configured project file
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	tasks := matchedNames(miseTaskPattern, data)

	return commandsForNames(tasks, "mise run "), nil
}

// matchedNames returns the set of pattern's first capture group across
// every match in data.
func matchedNames(pattern *regexp.Regexp, data []byte) map[string]struct{} {
	names := make(map[string]struct{})
	for _, m := range pattern.FindAllStringSubmatch(string(data), -1) {
		names[m[1]] = struct{}{}
	}

	return names
}

// commandsForNames maps each gate whose name (or alias) appears in names to
// prefix followed by the matched name, the shape every regex-based
// discoverer shares.
func commandsForNames(names map[string]struct{}, prefix string) map[string]string {
	commands := make(map[string]string)

	for gate, aliases := range gateScriptNames {
		for _, alias := range aliases {
			if _, ok := names[alias]; ok {
				commands[gate] = prefix + alias

				break
			}
		}
	}

	return commands
}

package bench

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/kyleking/wavez/internal/config"
)

// Arm is one side of the comparison.
type Arm string

// The arms: the harness alone, the harness with wavez wired in, and wired
// variants that each change one thing.
const (
	ArmBaseline Arm = "baseline"
	ArmWavez    Arm = "wavez"
	// ArmWavezQuiet is the wavez arm with the hooks' announcements turned
	// off through the harness's environment.
	ArmWavezQuiet Arm = "wavez-quiet"
	// ArmWavezRoute is the wavez arm with the route hint turned on in the
	// workspace's .wavez.toml.
	ArmWavezRoute Arm = "wavez-route"
	// ArmWavezGates is the wavez arm with the stop-stage checks turned on
	// in the workspace's .wavez.toml.
	ArmWavezGates Arm = "wavez-gates"
	// ArmWavezWarm is the wavez-gates arm with the checks also run in the
	// background each time a file is written.
	ArmWavezWarm Arm = "wavez-warm"
	// ArmWavezLocate is the wavez arm that is told, with each prompt, where
	// the identifiers it names are declared and tested.
	ArmWavezLocate Arm = "wavez-locate"
	// ArmWavezBrief is the wavez-locate arm that is handed the source of
	// what was found as well, and the command that edits and checks at once.
	ArmWavezBrief Arm = "wavez-brief"
	// ArmWavezLean is the wavez-brief arm with the harness cut down to what
	// such a session uses: a system prompt of a few lines in place of the
	// harness's own, and the tools to run a command, read, search, and edit.
	ArmWavezLean Arm = "wavez-lean"
)

// setting is one top-level key of a .wavez.toml and the value an arm gives
// it, spelled as TOML.
type setting struct {
	key   string
	value string
}

// armSpec is what makes an arm differ from the harness alone.
type armSpec struct {
	name Arm
	// settings are written into the workspace's .wavez.toml before wavez is
	// wired in, replacing what the pinned tree set for the same keys.
	settings []setting
	// env is added to the environment of the harness, and so of every hook
	// it runs.
	env []string
	// args are added to the harness's command line.
	args []string
	// wired runs `wavez init` in the workspace. An arm that is not wired
	// must find no wavez there at all.
	wired bool
}

// leanPrompt stands in for the harness's own system prompt in the lean arm.
const leanPrompt = "You are a coding agent working in a git checkout from a terminal. " +
	"Make the change or answer the question you are given, check it, and stop. " +
	"Run independent commands and reads in one message. Say what you did in a sentence or two."

// on is a setting turned on, as TOML and as git write it.
const on = "true"

// armSpecs is every arm, in the order a report shows them. A new variant
// of the wavez arm is one more line here: the settings and environment it
// changes.
var armSpecs = []armSpec{
	{name: ArmBaseline},
	{name: ArmWavez, wired: true},
	{name: ArmWavezQuiet, wired: true, env: []string{"WAVEZ_ANNOUNCE=0"}},
	{name: ArmWavezRoute, wired: true, settings: []setting{{key: "route", value: on}}},
	{name: ArmWavezGates, wired: true, settings: []setting{{key: "stop_checks", value: on}}},
	{
		name: ArmWavezWarm, wired: true,
		settings: []setting{{key: "stop_checks", value: on}, {key: "warm", value: on}},
	},
	{name: ArmWavezLocate, wired: true, settings: []setting{{key: "locate", value: on}}},
	{name: ArmWavezBrief, wired: true, settings: []setting{{key: "brief", value: on}}},
	{
		name: ArmWavezLean, wired: true, settings: []setting{{key: "brief", value: on}},
		args: []string{"--system-prompt", leanPrompt, "--tools", "Bash,Read,Edit,Grep,Glob"},
	},
}

// Arms lists the arms in the order a report shows them.
func Arms() []Arm {
	arms := make([]Arm, 0, len(armSpecs))
	for i := range armSpecs {
		arms = append(arms, armSpecs[i].name)
	}

	return arms
}

// UnknownArmError says an arm name is none of the arms.
type UnknownArmError struct {
	Name string
}

func (e *UnknownArmError) Error() string {
	names := make([]string, 0, len(armSpecs))
	for _, arm := range Arms() {
		names = append(names, string(arm))
	}

	return fmt.Sprintf("unknown arm %q: want one of %s", e.Name, strings.Join(names, ", "))
}

// ParseArm reads an arm name.
func ParseArm(name string) (Arm, error) {
	spec, err := specOf(Arm(name))
	if err != nil {
		return "", err
	}

	return spec.name, nil
}

func specOf(arm Arm) (*armSpec, error) {
	for i := range armSpecs {
		if armSpecs[i].name == arm {
			return &armSpecs[i], nil
		}
	}

	return nil, &UnknownArmError{Name: string(arm)}
}

// configLines is the arm's settings as the lines written into .wavez.toml.
func (s *armSpec) configLines() []string {
	if len(s.settings) == 0 {
		return nil
	}

	lines := make([]string, 0, len(s.settings))
	for _, set := range s.settings {
		lines = append(lines, set.key+" = "+set.value)
	}

	return lines
}

// tableHeader matches the first table of a TOML file. Keys above it are
// top-level, and keys below it belong to a table.
var tableHeader = regexp.MustCompile(`(?m)^[ \t]*\[`)

const settingsFileMode = 0o644

// applySettings writes an arm's settings into the workspace's .wavez.toml,
// keeping whatever else a pinned tree configured. The keys go first,
// because a TOML key written after a table would belong to the table, and
// only a top-level line that already sets one of them is dropped.
func applySettings(dir string, settings []setting) error {
	if len(settings) == 0 {
		return nil
	}

	workspace, err := os.OpenRoot(dir)
	if err != nil {
		return fmt.Errorf("opening the workspace: %w", err)
	}

	body, err := workspace.ReadFile(config.FileName)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.Join(fmt.Errorf("reading %s: %w", config.FileName, err), workspace.Close())
	}

	head, tables := body, []byte(nil)
	if at := tableHeader.FindIndex(body); at != nil {
		head, tables = body[:at[0]], body[at[0]:]
	}

	var out bytes.Buffer

	for _, set := range settings {
		line := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(set.key) + `[ \t]*=.*\n?`)
		head = line.ReplaceAll(head, nil)

		out.WriteString(set.key + " = " + set.value + "\n")
	}

	out.Write(head)
	out.Write(tables)

	err = workspace.WriteFile(config.FileName, out.Bytes(), settingsFileMode)
	if err = errors.Join(err, workspace.Close()); err != nil {
		return fmt.Errorf("writing the arm's settings to %s: %w", config.FileName, err)
	}

	return nil
}

// appendGates adds a task's [[check]] tables to the end of the workspace's
// .wavez.toml, where a table can always go, and refuses a result the gates
// could not load, such as a check whose name the pinned tree already uses.
func appendGates(dir, gates string) error {
	if gates == "" {
		return nil
	}

	workspace, err := os.OpenRoot(dir)
	if err != nil {
		return fmt.Errorf("opening the workspace: %w", err)
	}

	body, err := workspace.ReadFile(config.FileName)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.Join(fmt.Errorf("reading %s: %w", config.FileName, err), workspace.Close())
	}

	if len(body) > 0 && !bytes.HasSuffix(body, []byte("\n")) {
		body = append(body, '\n')
	}

	body = append(body, gates...)

	err = workspace.WriteFile(config.FileName, body, settingsFileMode)
	if err = errors.Join(err, workspace.Close()); err != nil {
		return fmt.Errorf("writing the task's gates to %s: %w", config.FileName, err)
	}

	if _, err := config.Load(dir); err != nil {
		return fmt.Errorf("adding the task's gates: %w", err)
	}

	return nil
}

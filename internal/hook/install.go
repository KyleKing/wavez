package hook

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

// The files `wavez init` owns, relative to the project directory.
const (
	SettingsPath  = ".claude/settings.json"
	SkillPath     = ".claude/skills/wavez-refactor/SKILL.md"
	GitignorePath = ".wavez/.gitignore"
)

const (
	defaultBin  = "wavez"
	hookArg     = " hook"
	installDirs = 0o750
)

// ErrSettingsShape reports a settings.json whose hooks are not laid out the
// way Claude Code documents, which init will not guess its way around.
var ErrSettingsShape = errors.New("unexpected settings.json shape")

// Install describes one run of `wavez init`.
type Install struct {
	// Dir is the project directory.
	Dir string
	// Bin is the wavez binary the hooks call. Empty means "wavez" found on
	// PATH; a value with a path separator is made absolute, because a hook
	// runs in whatever directory the agent has moved to.
	Bin string
	// Skill is the SKILL.md to install. Empty installs none, which is what
	// a project the skill's commands cannot work in gets.
	Skill string
	// StopBudget is the project's stop budget, which sets the Stop hook's
	// timeout. Zero means the default.
	StopBudget time.Duration
}

// Change is one file init would create, rewrite, or remove.
type Change struct {
	// Path is relative to the project directory, slash-separated.
	Path    string
	Content []byte
	// Created is true when the file does not exist yet.
	Created bool
	// Removed is true when the file is to be deleted.
	Removed bool
}

// Action names what the change does to its file.
func (c Change) Action() string {
	switch {
	case c.Removed:
		return "removed"
	case c.Created:
		return "created"
	default:
		return "updated"
	}
}

// Command is the shell command the installed hooks run.
func (i Install) Command() (string, error) {
	bin := i.Bin
	if bin == "" {
		return defaultBin + hookArg, nil
	}

	if strings.ContainsRune(bin, filepath.Separator) || strings.Contains(bin, "/") {
		abs, err := filepath.Abs(bin)
		if err != nil {
			return "", fmt.Errorf("resolving --bin: %w", err)
		}

		bin = abs
	}

	return quote(bin) + hookArg, nil
}

// quote makes a path one shell word.
func quote(word string) string {
	special := func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune("_./-+:@%", r)
	}

	if !strings.ContainsFunc(word, special) {
		return word
	}

	return "'" + strings.ReplaceAll(word, "'", `'\''`) + "'"
}

// Plan works out which files differ from what init wants, without writing
// anything. A second run over its own result plans nothing.
func (i Install) Plan() ([]Change, error) {
	command, err := i.Command()
	if err != nil {
		return nil, err
	}

	current, err := i.read(SettingsPath)
	if err != nil {
		return nil, err
	}

	settings, err := mergeSettings(current, command, i.StopBudget)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", SettingsPath, err)
	}

	wanted := []Change{{Path: SettingsPath, Content: settings}}
	if i.Skill != "" {
		wanted = append(wanted, Change{Path: SkillPath, Content: []byte(i.Skill)})
	}

	wanted = append(wanted, Change{Path: GitignorePath, Content: []byte("*\n")})

	var changes []Change

	for _, change := range wanted {
		existing, err := i.read(change.Path)
		if err != nil {
			return nil, err
		}

		if existing != nil && bytes.Equal(existing, change.Content) {
			continue
		}

		change.Created = existing == nil
		changes = append(changes, change)
	}

	return changes, nil
}

// PlanRemoval works out how to take out what init put in, without writing
// anything: the hook entries that run wavez, and the skill file while it is
// still the one init wrote. Skill is what init would have written. A
// settings file left holding nothing is removed. Hooks that are not
// wavez's, a skill someone edited, and the .wavez directory with its
// session state are all left as they are.
func (i Install) PlanRemoval() ([]Change, error) {
	command, err := i.Command()
	if err != nil {
		return nil, err
	}

	var changes []Change

	current, err := i.read(SettingsPath)
	if err != nil {
		return nil, err
	}

	if current != nil {
		settings, err := unmergeSettings(current, command)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", SettingsPath, err)
		}

		switch {
		case settings == nil:
			changes = append(changes, Change{Path: SettingsPath, Removed: true})
		case !bytes.Equal(settings, current):
			changes = append(changes, Change{Path: SettingsPath, Content: settings})
		}
	}

	skill, err := i.read(SkillPath)
	if err != nil {
		return nil, err
	}

	if skill != nil && bytes.Equal(skill, []byte(i.Skill)) {
		changes = append(changes, Change{Path: SkillPath, Removed: true})
	}

	return changes, nil
}

// read returns a project file's content, and nil when it does not exist.
func (i Install) read(rel string) ([]byte, error) {
	raw, err := os.ReadFile(filepath.Join(i.Dir, filepath.FromSlash(rel)))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", rel, err)
	}

	if raw == nil {
		raw = []byte{}
	}

	return raw, nil
}

// Apply writes the planned changes, each through a temporary file so an
// interrupted run leaves the previous settings intact. A removed file takes
// with it the directories init made for it that are now empty.
func (i Install) Apply(changes []Change) error {
	for _, change := range changes {
		path := filepath.Join(i.Dir, filepath.FromSlash(change.Path))

		if change.Removed {
			if err := i.remove(path); err != nil {
				return fmt.Errorf("removing %s: %w", change.Path, err)
			}

			continue
		}

		if err := os.MkdirAll(filepath.Dir(path), installDirs); err != nil {
			return fmt.Errorf("creating directory for %s: %w", change.Path, err)
		}

		if err := replaceFile(path, change.Content); err != nil {
			return fmt.Errorf("writing %s: %w", change.Path, err)
		}
	}

	return nil
}

// remove deletes a file and then each empty directory above it, up to the
// project directory.
func (i Install) remove(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("removing file: %w", err)
	}

	root := filepath.Clean(i.Dir)

	// A directory that still holds something is not init's to remove, and
	// os.Remove refuses it.
	for dir := filepath.Dir(path); dir != root && strings.HasPrefix(dir, root) && removed(dir); {
		dir = filepath.Dir(dir)
	}

	return nil
}

func removed(dir string) bool {
	return os.Remove(dir) == nil
}

func replaceFile(path string, content []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("creating temporary file: %w", err)
	}

	_, err = tmp.Write(content)
	err = errors.Join(err, tmp.Close())

	// A file that already exists keeps its mode; a new one is a project
	// file like any other in the checkout.
	mode := fs.FileMode(newFileMode)
	if info, statErr := os.Stat(path); statErr == nil {
		mode = info.Mode().Perm()
	}

	if err == nil {
		err = os.Chmod(tmp.Name(), mode)
	}

	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}

	if err != nil {
		return errors.Join(err, os.Remove(tmp.Name()))
	}

	return nil
}

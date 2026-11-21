package hook

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
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
	// Skill is the SKILL.md to install.
	Skill string
}

// Change is one file init would create or rewrite.
type Change struct {
	// Path is relative to the project directory, slash-separated.
	Path    string
	Content []byte
	// Created is true when the file does not exist yet.
	Created bool
}

// Action names what the change does to its file.
func (c Change) Action() string {
	if c.Created {
		return "created"
	}

	return "updated"
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

	settings, err := mergeSettings(current, command)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", SettingsPath, err)
	}

	wanted := []Change{
		{Path: SettingsPath, Content: settings},
		{Path: SkillPath, Content: []byte(i.Skill)},
		{Path: GitignorePath, Content: []byte("*\n")},
	}

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
// interrupted run leaves the previous settings intact.
func (i Install) Apply(changes []Change) error {
	for _, change := range changes {
		path := filepath.Join(i.Dir, filepath.FromSlash(change.Path))

		if err := os.MkdirAll(filepath.Dir(path), installDirs); err != nil {
			return fmt.Errorf("creating directory for %s: %w", change.Path, err)
		}

		if err := replaceFile(path, change.Content); err != nil {
			return fmt.Errorf("writing %s: %w", change.Path, err)
		}
	}

	return nil
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

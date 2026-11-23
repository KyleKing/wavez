package goal

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// ErrExists reports a goal file that Init would have replaced.
var ErrExists = errors.New("a goal is already set")

// ErrAddition reports a condition Add could not write from what it was
// given.
var ErrAddition = errors.New("cannot add the condition")

const starter = `# A goal that wavez checks with code. ` + "`wavez goal`" + ` says how much of it is done.
goal = %s
# true: an unmet goal holds the end of the turn, and the agent is told what is unmet
block = %t

# One [[done]] per condition, each with a name and exactly one check:
#   test    = "./internal/config -run TestParseGlob_Empty"       go test: at least one test runs, and all pass
#   grep    = { file = "README.md", pattern = "empty pattern" }   the regular expression is found in the file
#   absent  = { glob = "internal/**/*.go", pattern = "TODO" }     it is found in no file the glob selects
#   file    = "docs/glob.md"                                      the path exists
#   command = "golangci-lint run ./..."                           exits 0, run by sh -c in the project root
# A test or a command also takes timeout = "2m". ` + "`wavez goal add <kind> ...`" + ` appends one.
`

// Init writes a starter goal file holding sentence and returns its path.
// It refuses to replace a goal unless force is set, and a replaced goal's
// cached verdicts go with it.
func Init(root, sentence string, block, force bool) (string, error) {
	file := Path(root)

	if _, err := os.Lstat(file); err == nil && !force {
		return "", fmt.Errorf("%w in %s", ErrExists, file)
	}

	sentence = strings.TrimSpace(sentence)

	quoted, err := quote(sentence)
	if err != nil || sentence == "" {
		return "", &Error{Path: file, Key: "goal", Problem: "must be one sentence on one line"}
	}

	content := fmt.Appendf(nil, starter, quoted, block)
	if _, err := parse(file, content); err != nil {
		return "", err
	}

	if err := ignoreState(root); err != nil {
		return "", err
	}

	if err := writeAtomic(file, content); err != nil {
		return "", err
	}

	return file, remove(StatePath(root))
}

// ignoreState makes .wavez ignore itself in git, which is what keeps a goal
// file out of a repository.
func ignoreState(root string) error {
	ignore := filepath.Join(root, Dir, ".gitignore")
	if _, err := os.Lstat(ignore); err == nil {
		return nil
	}

	return writeAtomic(ignore, []byte("*\n"))
}

// Addition is one condition to append, as a command line gives it.
type Addition struct {
	Kind Kind
	// Name is optional, and derived from Args when empty.
	Name string
	// Timeout is optional and read as the file's timeout key is.
	Timeout string
	// Args are the kind's values: a test's packages and -run, a grep's file
	// and pattern, an absent's glob and pattern, a file's path, or a
	// command's words.
	Args []string
}

// Add appends one condition to root's goal file and returns it. The file's
// existing text is kept byte for byte, comments included, and nothing is
// written unless the result is a valid goal.
func Add(root string, add Addition) (Item, error) {
	file := Path(root)

	existing, err := fs.ReadFile(os.DirFS(root), Dir+"/"+FileName)
	if errors.Is(err, fs.ErrNotExist) {
		return Item{}, fmt.Errorf("%w: start one with `wavez goal init \"<sentence>\"`", ErrNoGoal)
	}

	if err != nil {
		return Item{}, fmt.Errorf("reading %s: %w", file, err)
	}

	block, err := add.toml()
	if err != nil {
		return Item{}, err
	}

	content := string(existing)
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}

	content += "\n" + block

	goal, err := parse(file, []byte(content))
	if err != nil {
		return Item{}, err
	}

	if err := writeAtomic(file, []byte(content)); err != nil {
		return Item{}, err
	}

	return goal.Items[len(goal.Items)-1], nil
}

// toml renders the addition as one [[done]] table.
func (a Addition) toml() (string, error) {
	check, err := a.check()
	if err != nil {
		return "", err
	}

	name, err := quote(a.name())
	if err != nil {
		return "", err
	}

	table := fmt.Sprintf("[[done]]\nname = %s\n%s = %s\n", name, a.Kind, check)
	if a.Timeout == "" {
		return table, nil
	}

	timeout, err := quote(a.Timeout)
	if err != nil {
		return "", err
	}

	return table + "timeout = " + timeout + "\n", nil
}

// check renders the value of the kind's key.
func (a Addition) check() (string, error) {
	switch a.Kind {
	case KindTest, KindCommand:
		if len(a.Args) == 0 {
			return "", fmt.Errorf("%w: a %s needs a value", ErrAddition, a.Kind)
		}

		return quote(strings.Join(a.Args, " "))
	case KindFile:
		if len(a.Args) != 1 {
			return "", fmt.Errorf("%w: a file takes one path", ErrAddition)
		}

		return quote(a.Args[0])
	case KindGrep:
		return a.match("file")
	case KindAbsent:
		return a.match("glob")
	default:
		return "", fmt.Errorf("%w: %q is not a kind (test, grep, absent, file, command)", ErrAddition, a.Kind)
	}
}

// match renders the inline table a grep or an absent takes.
func (a Addition) match(target string) (string, error) {
	const pair = 2
	if len(a.Args) != pair {
		return "", fmt.Errorf("%w: %s takes a %s and a pattern", ErrAddition, a.Kind, target)
	}

	where, err := quote(a.Args[0])
	if err != nil {
		return "", err
	}

	pattern, err := quote(a.Args[1])
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("{ %s = %s, pattern = %s }", target, where, pattern), nil
}

// name is the condition's name, or one that says what it checks.
func (a Addition) name() string {
	if strings.TrimSpace(a.Name) != "" {
		return strings.TrimSpace(a.Name)
	}

	const nameLimit = 60

	words := strings.Fields(strings.Join(a.Args, " "))

	return clip(string(a.Kind)+" "+strings.Join(words, " "), nameLimit)
}

// quote writes value as a TOML string: literal when it holds a backslash
// and can be, so a regular expression reads as it was typed, and basic
// otherwise.
func quote(value string) (string, error) {
	if strings.ContainsFunc(value, unicode.IsControl) {
		return "", fmt.Errorf("%w: %q holds a control character", ErrAddition, value)
	}

	if strings.Contains(value, `\`) && !strings.Contains(value, "'") {
		return "'" + value + "'", nil
	}

	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value) + `"`, nil
}

// Clear removes root's goal and its cached verdicts, and reports whether
// there was a goal to remove.
func Clear(root string) (bool, error) {
	_, err := os.Lstat(Path(root))
	had := err == nil

	return had, errors.Join(remove(Path(root)), remove(StatePath(root)))
}

func remove(file string) error {
	if err := os.Remove(file); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("removing %s: %w", file, err)
	}

	return nil
}

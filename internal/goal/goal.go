package goal

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/kyleking/wavez/internal/config"
)

// Where a project's goal and its cached verdicts live, under the root.
const (
	Dir       = ".wavez"
	FileName  = "goal.toml"
	StateName = "goal.state.json"
)

// DefaultTimeout bounds a test or command condition that names no timeout.
const DefaultTimeout = 2 * time.Minute

// problemEmpty is what is wrong with a required key that holds nothing.
const problemEmpty = "is empty"

// ErrNoGoal reports a project with no goal file, which is the usual state
// and not a failure.
var ErrNoGoal = errors.New("no goal is set")

// Kind is how one condition is checked.
type Kind string

// The kinds a condition can be. File, grep, and absent are read in process
// and always run. Test and command start a process and run only when asked.
const (
	KindTest    Kind = "test"
	KindGrep    Kind = "grep"
	KindAbsent  Kind = "absent"
	KindFile    Kind = "file"
	KindCommand Kind = "command"
)

// Kinds lists every kind in the order the starter file documents them.
func Kinds() []Kind {
	return []Kind{KindTest, KindGrep, KindAbsent, KindFile, KindCommand}
}

// Expensive reports whether checking the kind starts a process.
func (k Kind) Expensive() bool {
	return k == KindTest || k == KindCommand
}

// Item is one condition of a goal, already validated.
type Item struct {
	// Pattern is what a grep looks for and what an absent must not find.
	Pattern *regexp.Regexp
	Name    string
	Kind    Kind
	// Run is a test's -run pattern, empty for every test in Packages.
	Run string
	// File is the slash-separated path a grep reads or a file kind expects.
	File string
	// Command runs through `sh -c` in the project root.
	Command string
	// Glob selects the files an absent reads.
	Glob config.Glob
	// Packages are a test's package patterns, each `./`-prefixed.
	Packages []string
	// Timeout bounds a test or a command.
	Timeout time.Duration
}

// definition identifies what the item checks, so a cached verdict is
// dropped when the item is edited.
func (i Item) definition() string {
	pattern := ""
	if i.Pattern != nil {
		pattern = i.Pattern.String()
	}

	sum := sha256.Sum256([]byte(strings.Join([]string{
		i.Name, string(i.Kind), strings.Join(i.Packages, " "), i.Run, i.File, i.Glob.String(), pattern, i.Command,
		i.Timeout.String(),
	}, "\x00")))

	return hex.EncodeToString(sum[:8])
}

// Goal is a parsed goal file.
type Goal struct {
	// Sentence says what the task is, for the person.
	Sentence string
	// Items are the conditions in file order.
	Items []Item
	// Block makes an unmet goal hold the end of the turn.
	Block bool
}

// Path returns where root's goal file is.
func Path(root string) string {
	return filepath.Join(root, Dir, FileName)
}

// Error is one thing wrong with a goal file.
type Error struct {
	// Path is the file.
	Path string
	// Key names the key, with a condition named by its name when it has
	// one and by its position when it does not. Empty for a syntax error.
	Key string
	// Problem says what is wrong.
	Problem string
	// Line is the 1-based line, or 0 when it is not known.
	Line int
}

func (e *Error) Error() string {
	where := e.Path
	if e.Line > 0 {
		where = fmt.Sprintf("%s:%d", e.Path, e.Line)
	}

	if e.Key == "" {
		return where + ": " + e.Problem
	}

	return fmt.Sprintf("%s: %s %s", where, e.Key, e.Problem)
}

// Load reads and validates root's goal. A project without one gets
// ErrNoGoal, and a file that is present and wrong fails with an *Error, so
// a typo never silently drops a condition.
func Load(root string) (Goal, error) {
	data, err := fs.ReadFile(os.DirFS(root), path.Join(Dir, FileName))
	if errors.Is(err, fs.ErrNotExist) {
		return Goal{}, ErrNoGoal
	}

	if err != nil {
		return Goal{}, fmt.Errorf("reading %s: %w", Path(root), err)
	}

	return parse(Path(root), data)
}

type rawGoal struct {
	Goal  string    `toml:"goal"`
	Done  []rawItem `toml:"done"`
	Block bool      `toml:"block"`
}

type rawItem struct {
	Grep    *rawMatch `toml:"grep"`
	Absent  *rawMatch `toml:"absent"`
	Name    string    `toml:"name"`
	Test    string    `toml:"test"`
	File    string    `toml:"file"`
	Command string    `toml:"command"`
	Timeout string    `toml:"timeout"`
}

type rawMatch struct {
	File    *string `toml:"file"`
	Glob    *string `toml:"glob"`
	Pattern string  `toml:"pattern"`
}

func parse(file string, data []byte) (Goal, error) {
	var raw rawGoal

	meta, err := toml.Decode(string(data), &raw)
	if err != nil {
		var syntax toml.ParseError
		if errors.As(err, &syntax) {
			return Goal{}, &Error{Path: file, Line: syntax.Position.Line, Problem: syntax.Message}
		}

		return Goal{}, &Error{Path: file, Problem: err.Error()}
	}

	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		key := undecoded[0]

		return Goal{}, &Error{Path: file, Key: key.String(), Problem: "is not a known key", Line: keyLine(data, key)}
	}

	goal := Goal{Sentence: strings.TrimSpace(raw.Goal), Block: raw.Block}
	if goal.Sentence == "" || strings.ContainsAny(goal.Sentence, "\r\n") {
		return Goal{}, &Error{Path: file, Key: "goal", Problem: "must be one sentence on one line"}
	}

	seen := map[string]bool{}

	for index, rawItem := range raw.Done {
		item, err := rawItem.parse(index)
		if err != nil {
			err.Path = file

			return Goal{}, err
		}

		if seen[item.Name] {
			return Goal{}, &Error{Path: file, Key: itemKey(item.Name, "name"), Problem: "is used twice"}
		}

		seen[item.Name] = true

		goal.Items = append(goal.Items, item)
	}

	return goal, nil
}

// keyLine finds the line an unknown key was written on. The decoder keeps
// no positions for keys, so this reads the text: a top-level key sits above
// the first table, a condition's key below it, and a key inside grep or
// absent may share its line with the braces.
func keyLine(data []byte, key toml.Key) int {
	if len(key) == 0 {
		return 0
	}

	name := regexp.QuoteMeta(key[len(key)-1])
	lead := `^\s*`

	const nested = 3
	if len(key) >= nested {
		lead = `(?:^|[{,])\s*`
	}

	assign := regexp.MustCompile(lead + `(?:` + name + `|"` + name + `"|'` + name + `')\s*=`)
	inTable := false

	for index, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "[") {
			inTable = true

			continue
		}

		if inTable == (len(key) > 1) && assign.MatchString(line) {
			return index + 1
		}
	}

	return 0
}

func itemKey(name, key string) string {
	return fmt.Sprintf("done %q %s", name, key)
}

func (r rawItem) parse(index int) (Item, *Error) {
	name := strings.TrimSpace(r.Name)
	if name == "" || strings.ContainsAny(name, "\r\n") {
		return Item{}, &Error{Key: fmt.Sprintf("done[%d].name", index), Problem: "must be a name on one line"}
	}

	item := Item{Name: name}

	kinds, err := r.fill(&item)
	if err != nil {
		return Item{}, err
	}

	switch {
	case len(kinds) == 0:
		return Item{}, &Error{
			Key:     itemKey(name, "has"),
			Problem: "no check: give it one of test, grep, absent, file, or command",
		}
	case len(kinds) > 1:
		return Item{}, &Error{
			Key:     itemKey(name, "has"),
			Problem: fmt.Sprintf("%d checks (%s), and a condition has one", len(kinds), strings.Join(kinds, ", ")),
		}
	}

	if err := r.parseTimeout(&item); err != nil {
		return Item{}, err
	}

	return item, nil
}

// fill validates every check the raw item carries and returns their kinds.
// The item ends up holding the last one, which is the only one when the
// caller accepts it.
func (r rawItem) fill(item *Item) ([]string, *Error) {
	var kinds []string

	steps := []struct {
		fill    func(*Item) *Error
		kind    Kind
		present bool
	}{
		{r.fillTest, KindTest, r.Test != ""},
		{r.fillGrep, KindGrep, r.Grep != nil},
		{r.fillAbsent, KindAbsent, r.Absent != nil},
		{r.fillFile, KindFile, r.File != ""},
		{r.fillCommand, KindCommand, r.Command != ""},
	}

	for _, step := range steps {
		if !step.present {
			continue
		}

		if err := step.fill(item); err != nil {
			return nil, err
		}

		item.Kind = step.kind
		kinds = append(kinds, string(step.kind))
	}

	return kinds, nil
}

func (r rawItem) fillFile(item *Item) *Error {
	if problem := outsideRoot(r.File); problem != "" {
		return &Error{Key: itemKey(item.Name, "file"), Problem: problem}
	}

	item.File = path.Clean(r.File)

	return nil
}

func (r rawItem) fillCommand(item *Item) *Error {
	if strings.TrimSpace(r.Command) == "" {
		return &Error{Key: itemKey(item.Name, "command"), Problem: problemEmpty}
	}

	item.Command = r.Command

	return nil
}

func (r rawItem) fillGrep(item *Item) *Error {
	switch {
	case r.Grep.Glob != nil:
		return &Error{Key: itemKey(item.Name, "grep.glob"), Problem: "is not read: grep takes one file and a pattern"}
	case r.Grep.File == nil:
		return &Error{Key: itemKey(item.Name, "grep.file"), Problem: "is missing"}
	}

	if problem := outsideRoot(*r.Grep.File); problem != "" {
		return &Error{Key: itemKey(item.Name, "grep.file"), Problem: problem}
	}

	item.File = path.Clean(*r.Grep.File)

	return r.Grep.compile(item, "grep")
}

func (r rawItem) fillAbsent(item *Item) *Error {
	switch {
	case r.Absent.File != nil:
		return &Error{Key: itemKey(item.Name, "absent.file"), Problem: "is not read: absent takes a glob and a pattern"}
	case r.Absent.Glob == nil:
		return &Error{Key: itemKey(item.Name, "absent.glob"), Problem: "is missing"}
	}

	if problem := outsideRoot(*r.Absent.Glob); problem != "" {
		return &Error{Key: itemKey(item.Name, "absent.glob"), Problem: problem}
	}

	glob, err := config.ParseGlob(*r.Absent.Glob)
	if err != nil {
		return &Error{Key: itemKey(item.Name, "absent.glob"), Problem: err.Error()}
	}

	item.Glob = glob

	return r.Absent.compile(item, "absent")
}

func (m rawMatch) compile(item *Item, table string) *Error {
	if m.Pattern == "" {
		return &Error{Key: itemKey(item.Name, table+".pattern"), Problem: problemEmpty}
	}

	pattern, err := regexp.Compile(m.Pattern)
	if err != nil {
		return &Error{Key: itemKey(item.Name, table+".pattern"), Problem: "is not a regular expression: " + err.Error()}
	}

	item.Pattern = pattern

	return nil
}

// fillTest reads `<package>... [-run <pattern>]`. Nothing else is accepted,
// because any other flag changes what a pass means and a command condition
// says that in full.
func (r rawItem) fillTest(item *Item) *Error {
	key := itemKey(item.Name, "test")
	words := strings.Fields(r.Test)

	for index := 0; index < len(words); index++ {
		word := words[index]

		switch {
		case word == "-run" && index+1 < len(words) && item.Run == "":
			index++
			item.Run = words[index]
		case strings.HasPrefix(word, "-run=") && item.Run == "":
			item.Run = strings.TrimPrefix(word, "-run=")
		case strings.HasPrefix(word, "-"):
			return &Error{
				Key:     key,
				Problem: fmt.Sprintf("has %q, and only one -run <pattern> is read; use a command for the rest", word),
			}
		case item.Run != "":
			return &Error{Key: key, Problem: fmt.Sprintf("has %q after -run; packages come first", word)}
		default:
			if problem := badPackage(word); problem != "" {
				return &Error{Key: key, Problem: problem}
			}

			item.Packages = append(item.Packages, word)
		}
	}

	if len(item.Packages) == 0 {
		return &Error{Key: key, Problem: "names no package, want something like ./internal/config -run TestName"}
	}

	if _, err := regexp.Compile(item.Run); err != nil {
		return &Error{Key: key, Problem: "has a -run pattern that is not a regular expression: " + err.Error()}
	}

	return nil
}

// badPackage says what is wrong with a package pattern, or nothing. An
// unprefixed path resolves against the standard library, so it is refused
// here and never handed to go.
func badPackage(pattern string) string {
	if pattern != "." && !strings.HasPrefix(pattern, "./") {
		return fmt.Sprintf("names the package %q, want ./%s: go reads an unprefixed path as a standard library package",
			pattern, strings.TrimPrefix(pattern, "/"))
	}

	if !filepath.IsLocal(filepath.FromSlash(pattern)) {
		return fmt.Sprintf("names the package %q, which is outside the project", pattern)
	}

	return ""
}

// outsideRoot says why a path or glob cannot be read as one inside the
// project root, or nothing when it can.
func outsideRoot(name string) string {
	switch {
	case strings.TrimSpace(name) == "":
		return problemEmpty
	case path.IsAbs(name) || filepath.IsAbs(name) || !filepath.IsLocal(filepath.FromSlash(name)):
		return fmt.Sprintf("is %q, want a path inside the project, relative to its root", name)
	}

	return ""
}

func (r rawItem) parseTimeout(item *Item) *Error {
	if r.Timeout == "" {
		if item.Kind.Expensive() {
			item.Timeout = DefaultTimeout
		}

		return nil
	}

	if !item.Kind.Expensive() {
		return &Error{Key: itemKey(item.Name, "timeout"), Problem: "is not read: only a test or a command can time out"}
	}

	timeout, err := time.ParseDuration(r.Timeout)
	if err != nil || timeout <= 0 {
		return &Error{
			Key:     itemKey(item.Name, "timeout"),
			Problem: fmt.Sprintf("is %q, want a positive duration such as 90s or 2m", r.Timeout),
		}
	}

	item.Timeout = timeout

	return nil
}

package transcript

import (
	"path/filepath"
	"slices"
	"strings"
)

// Class is what one tool call, and by extension one turn, was spent on.
type Class string

// The classes, weakest first. A turn takes the strongest class among its
// tool calls, so a turn that reads two files and edits one is an edit turn.
const (
	ClassProse     Class = "prose"
	ClassOther     Class = "other"
	ClassRetrieval Class = "retrieval"
	ClassShell     Class = "shell"
	ClassEdit      Class = "edit"
)

// classOrder lists the classes weakest first, for picking a turn's
// strongest.
var classOrder = []Class{ClassProse, ClassOther, ClassRetrieval, ClassShell, ClassEdit}

// stronger reports whether a outranks b.
func stronger(a, b Class) bool {
	return slices.Index(classOrder, a) > slices.Index(classOrder, b)
}

// shellTool is the tool whose class depends on the command it ran.
const shellTool = "Bash"

// toolClasses maps a tool name to its class. A tool absent from the table
// (a subagent launch, a todo list, an MCP tool) is ClassOther. The shell
// tool is listed as ClassShell and demoted to retrieval per command by
// readOnlyPrograms.
var toolClasses = map[string]Class{
	"Read":         ClassRetrieval,
	"Grep":         ClassRetrieval,
	"Glob":         ClassRetrieval,
	"LS":           ClassRetrieval,
	"NotebookRead": ClassRetrieval,
	"WebFetch":     ClassRetrieval,
	"WebSearch":    ClassRetrieval,
	"Edit":         ClassEdit,
	"Write":        ClassEdit,
	"MultiEdit":    ClassEdit,
	"NotebookEdit": ClassEdit,
	shellTool:      ClassShell,
}

// readOnly says when a program only reads. Subs, when set, limits the
// answer to those subcommands; Unless names argument prefixes that make an
// otherwise read-only program write.
type readOnly struct {
	Subs   []string
	Unless []string
}

// readOnlyPrograms is the shell half of the retrieval class: a command
// made only of these, with no redirect into a file, is retrieval.
var readOnlyPrograms = map[string]readOnly{
	"ls":     {},
	"cat":    {},
	"head":   {},
	"tail":   {},
	"wc":     {},
	"rg":     {},
	"grep":   {},
	"tree":   {},
	"file":   {},
	"stat":   {},
	"pwd":    {},
	"which":  {},
	"echo":   {},
	"sort":   {},
	"uniq":   {},
	"cut":    {},
	"nl":     {},
	"jq":     {},
	"fd":     {},
	"find":   {Unless: []string{"-delete", "-exec"}},
	"sed":    {Unless: []string{"-i"}},
	"git":    {Subs: []string{"status", "log", "diff", "show", "blame", "ls-files", "grep", "rev-parse"}},
	"go":     {Subs: []string{"doc", "list", "env", "version"}},
	"gopls":  {Subs: []string{"definition", "references", "symbols", "workspace_symbol"}},
	"jj":     {Subs: []string{"status", "log", "diff", "show"}},
	"python": {Subs: []string{"--version"}},
}

// Words the tables below repeat.
const (
	wordRun   = "run"
	wordTest  = "test"
	wordCheck = "check"
	wordBuild = "build"
	progMise  = "mise"
	progCargo = "cargo"
)

// wrappers are prefixes that run another program and say nothing about
// what the command does, so they are stripped before a command is read.
var wrappers = [][]string{
	{progMise, "exec", "--"},
	{"uv", wordRun},
	{"poetry", wordRun},
	{"python", "-m"},
	{"python3", "-m"},
	{"npx"},
	{"time"},
	{"sudo"},
	{"command"},
	{"nohup"},
}

// scope is how a sweep rule decides a command named no target.
type scope int

const (
	// ScopeAlways marks a command that is a sweep however it is called.
	scopeAlways scope = iota
	// ScopeBare marks a command that is a sweep when it names no target.
	scopeBare
	// ScopeWholeTree marks a command that is a sweep when a target is the
	// whole module ("./...").
	scopeWholeTree
	// ScopeBareOrWholeTree accepts either of the two above.
	scopeBareOrWholeTree
)

// sweepRule matches one untargeted check. Sub is the words that follow the
// program; Unless names flags that narrow the run enough to stop it being
// a sweep (`go test ./... -run TestOne`).
type sweepRule struct {
	Prog   string
	Sub    []string
	Unless []string
	Scope  scope
}

// sweepRules is every shell command counted as an untargeted sweep: a
// whole-project build, test, or lint run that a change-triggered gate is
// meant to make unnecessary. The count of these is the number the gates
// are supposed to drive to zero.
var sweepRules = []sweepRule{
	{Prog: "go", Sub: []string{wordBuild}, Scope: scopeWholeTree},
	{Prog: "go", Sub: []string{"vet"}, Scope: scopeWholeTree},
	{Prog: "go", Sub: []string{wordTest}, Scope: scopeWholeTree, Unless: []string{"-run"}},
	{Prog: "golangci-lint", Sub: []string{wordRun}, Scope: scopeBareOrWholeTree},
	{Prog: "staticcheck", Scope: scopeWholeTree},
	{Prog: "pytest", Scope: scopeBare},
	{Prog: "ruff", Sub: []string{wordCheck}, Scope: scopeBare},
	{Prog: "npm", Sub: []string{wordTest}, Scope: scopeBare},
	{Prog: "npm", Sub: []string{wordRun, wordTest}, Scope: scopeBare},
	{Prog: "yarn", Sub: []string{wordTest}, Scope: scopeBare},
	{Prog: "pnpm", Sub: []string{wordTest}, Scope: scopeBare},
	{Prog: "bun", Sub: []string{wordTest}, Scope: scopeBare},
	{Prog: progCargo, Sub: []string{wordTest}, Scope: scopeBare},
	{Prog: progCargo, Sub: []string{wordBuild}, Scope: scopeBare},
	{Prog: progCargo, Sub: []string{wordCheck}, Scope: scopeBare},
	{Prog: "make", Sub: []string{wordTest}, Scope: scopeAlways},
	{Prog: "make", Sub: []string{wordCheck}, Scope: scopeAlways},
	{Prog: progMise, Sub: []string{wordRun, "ci"}, Scope: scopeAlways},
	{Prog: progMise, Sub: []string{wordRun, wordTest}, Scope: scopeAlways},
	{Prog: "hk", Sub: []string{wordCheck}, Scope: scopeAlways},
}

// wholeTree are the targets that name every package.
var wholeTree = []string{"./...", "..."}

// refactorProgram and refactorVerbs name the calls counted as uses of the
// name-based refactor commands.
const refactorProgram = "wavez"

var refactorVerbs = []string{"rename", "move", "delete"}

// classify reports the class of one tool call. Only the shell tool looks at
// its command.
func classify(tool, command string) Class {
	class, ok := toolClasses[tool]
	if !ok {
		return ClassOther
	}

	if tool == shellTool && readOnlyCommand(command) {
		return ClassRetrieval
	}

	return class
}

// readOnlyCommand reports whether every simple command in line only reads.
// An empty line is not read-only, since nothing can be said about it.
func readOnlyCommand(line string) bool {
	cmds := splitCommands(line)
	seen := false

	for _, c := range cmds {
		if c.writes {
			return false
		}

		words := unwrap(c.words)
		if len(words) == 0 || words[0] == "cd" {
			continue
		}

		if !readOnlyWords(words) {
			return false
		}

		seen = true
	}

	return seen
}

func readOnlyWords(words []string) bool {
	rule, ok := readOnlyPrograms[filepath.Base(words[0])]
	if !ok {
		return false
	}

	args := words[1:]
	if len(rule.Subs) > 0 && (len(args) == 0 || !slices.Contains(rule.Subs, args[0])) {
		return false
	}

	for _, arg := range args {
		for _, prefix := range rule.Unless {
			if strings.HasPrefix(arg, prefix) {
				return false
			}
		}
	}

	return true
}

// sweeps returns the label of every untargeted sweep in a shell line.
func sweeps(line string) []string {
	var out []string

	for _, c := range splitCommands(line) {
		words := unwrap(c.words)
		for i := range sweepRules {
			if sweepRules[i].matches(words) {
				out = append(out, sweepRules[i].label())

				break
			}
		}
	}

	return out
}

func (r *sweepRule) label() string {
	return strings.Join(append([]string{r.Prog}, r.Sub...), " ")
}

func (r *sweepRule) matches(words []string) bool {
	if len(words) < 1+len(r.Sub) || filepath.Base(words[0]) != r.Prog {
		return false
	}

	if !slices.Equal(words[1:1+len(r.Sub)], r.Sub) {
		return false
	}

	var targets []string

	for _, arg := range words[1+len(r.Sub):] {
		if slices.Contains(r.Unless, arg) {
			return false
		}

		if !strings.HasPrefix(arg, "-") {
			targets = append(targets, arg)
		}
	}

	return r.Scope.holds(targets)
}

func (s scope) holds(targets []string) bool {
	bare := len(targets) == 0
	whole := slices.ContainsFunc(targets, func(t string) bool { return slices.Contains(wholeTree, t) })

	switch s {
	case scopeAlways:
		return true
	case scopeBare:
		return bare
	case scopeWholeTree:
		return whole
	case scopeBareOrWholeTree:
		return bare || whole
	default:
		return false
	}
}

// refactorCalls returns the verb of every `wavez rename|move|delete` in a
// shell line.
func refactorCalls(line string) []string {
	var out []string

	for _, c := range splitCommands(line) {
		words := unwrap(c.words)
		if len(words) == 0 || filepath.Base(words[0]) != refactorProgram {
			continue
		}

		if args := words[1:]; len(args) > 0 && slices.Contains(refactorVerbs, args[0]) {
			out = append(out, args[0])
		}
	}

	return out
}

// unwrap drops leading environment assignments and wrapper prefixes, so
// `FOO=1 mise exec -- go test ./...` reads as `go test ./...`.
func unwrap(words []string) []string {
	for changed := true; changed; {
		changed = false

		for len(words) > 0 && isAssignment(words[0]) {
			words, changed = words[1:], true
		}

		for _, w := range wrappers {
			if len(words) > len(w) && slices.Equal(words[:len(w)], w) {
				words, changed = words[len(w):], true
			}
		}
	}

	return words
}

func isAssignment(word string) bool {
	name, _, ok := strings.Cut(word, "=")
	if !ok || name == "" {
		return false
	}

	return !strings.ContainsAny(name, "-/.\"'")
}

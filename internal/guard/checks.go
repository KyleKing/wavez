package guard

import "strings"

// checkFormat names the formatting check both gofmt and goimports run.
const checkFormat = "format"

// wholeTree is the package pattern that turns a scoped Go command into a
// sweep of the module, which is what the harness's gates already do.
const wholeTree = "./..."

// harnessTools are run by the harness on every change, so a model running
// one spends a turn to learn what it is about to be told.
var harnessTools = map[string]string{
	"golangci-lint": "lint",
	cmdGofmt:        checkFormat,
	"goimports":     checkFormat,
	"hk":            "the hook pipeline",
}

// ProjectCheck reports which of the project's own checks command re-runs,
// and whether it re-runs one at all. It reads only the command text, like
// everything else in this package, and it deliberately passes a scoped
// command through: running one package's tests to watch a failure is work,
// while sweeping the module is a report the harness already has.
func ProjectCheck(command string) (string, bool) {
	for _, seq := range splitSequence(strings.TrimSpace(command)) {
		if name, ok := checkOf(tokenize(seq)); ok {
			return name, true
		}
	}

	return "", false
}

func checkOf(tokens []string) (string, bool) {
	if len(tokens) == 0 {
		return "", false
	}

	head := baseName(tokens[0])
	if head == cmdMise && len(tokens) > 1 && tokens[1] == "exec" {
		return checkOf(afterExec(tokens))
	}

	if name, ok := harnessTools[head]; ok {
		return name, true
	}

	if head == cmdMise && len(tokens) > 1 && tokens[1] == "run" {
		return "the mise task", true
	}

	if head == "go" {
		return goCheck(tokens)
	}

	return "", false
}

// afterExec drops `mise exec [tools] --` so the wrapped command is judged as
// itself. A wrapper that hid the command from this check would make the
// answer depend on how it was invoked.
func afterExec(tokens []string) []string {
	for i, tok := range tokens {
		if tok == "--" {
			return tokens[i+1:]
		}
	}

	return nil
}

var goSweeps = map[string]string{"build": "build", "test": "tests", "vet": "vet"}

func goCheck(tokens []string) (string, bool) {
	if len(tokens) < minGitTokens {
		return "", false
	}

	name, ok := goSweeps[tokens[1]]
	if !ok {
		return "", false
	}

	for _, tok := range tokens[2:] {
		if tok == wholeTree {
			return name, true
		}
	}

	return "", false
}

// vcsReads are the version-control subcommands that only look. A run asks
// them to find out what it has changed, which the harness has already
// recorded: 24 of 278 logged shell calls were this question.
var vcsReads = map[string]bool{
	cmdDiff: true, "log": true, subShow: true, "st": true, "status": true,
}

// VCSInspect reports whether command only asks version control what the
// working copy holds. A write (`jj commit`, `git add`) is not this: the
// guard classifies those on their own terms and this says nothing about
// them.
func VCSInspect(command string) bool {
	tokens := tokenize(strings.TrimSpace(command))
	if len(tokens) < minGitTokens {
		return false
	}

	if head := baseName(tokens[0]); head != cmdGit && head != "jj" {
		return false
	}

	return vcsReads[tokens[1]]
}

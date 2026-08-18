package guard

import (
	"path/filepath"
	"strings"
)

// interpreters run a script named on their command line. A script passed to
// one of these is executed as surely as one invoked by path, so both are
// worth resolving.
var interpreters = map[string]bool{
	"ash": true, "bash": true, "dash": true, "ksh": true, "sh": true, "zsh": true,
	"node": true, "perl": true, "python": true, "python3": true, "ruby": true,
}

// ExecutedScripts returns the project-relative paths a command would run as
// a script: a file invoked by path, one handed to an interpreter, and one
// sourced into the current shell.
//
// It reads no files. Resolving what a command executes is a question about
// the command's text, and answering it here keeps this package a pure
// function of that text. A caller that wants to know what those scripts
// *do* reads them and calls Classify on their contents, which is where the
// filesystem access belongs.
func ExecutedScripts(command string, env Env) []string {
	root := cleanRoot(env.ProjectRoot)

	var out []string

	seen := map[string]bool{}

	for _, seq := range splitSequence(stripSubstitutions(command)) {
		for _, stage := range splitPipeline(seq) {
			for _, path := range scriptsIn(stage, root) {
				if seen[path] {
					continue
				}

				seen[path] = true
				out = append(out, path)
			}
		}
	}

	return out
}

func stripSubstitutions(command string) string {
	outer, _ := extractSubstitutions(strings.TrimSpace(command))

	return outer
}

func scriptsIn(stage, root string) []string {
	tokens := tokenize(stage)
	if len(tokens) == 0 {
		return nil
	}

	name := baseName(tokens[0])

	if name == "source" || tokens[0] == "." {
		return projectPaths(tokens[1:], root, 1)
	}

	if interpreters[name] {
		if runsInlineCode(tokens[1:]) {
			return nil
		}

		return projectPaths(tokens[1:], root, 1)
	}

	// A bare command word that looks like a path is an executable in the
	// tree; a bare name is something on PATH and not ours to read.
	if looksLikePath(tokens[0]) {
		return projectPaths(tokens[:1], root, 1)
	}

	return nil
}

// projectPaths returns up to limit of args that resolve inside root,
// skipping flags. Only the first is taken because an interpreter runs one
// script and passes the rest to it.
func projectPaths(args []string, root string, limit int) []string {
	var out []string

	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			continue
		}

		rel, ok := insideRoot(arg, root)
		if !ok {
			return out
		}

		out = append(out, rel)
		if len(out) == limit {
			return out
		}
	}

	return out
}

// inlineCodeFlags make an interpreter run its next argument as source
// rather than open it as a file, so what follows is code and not a path.
var inlineCodeFlags = map[string]bool{"-c": true, "-e": true}

func runsInlineCode(args []string) bool {
	for _, arg := range args {
		if inlineCodeFlags[arg] {
			return true
		}

		if !strings.HasPrefix(arg, "-") {
			return false
		}
	}

	return false
}

func looksLikePath(tok string) bool {
	return strings.Contains(tok, "/")
}

// insideRoot reports arg's path relative to root, and whether it is inside
// it. An empty root cannot contain anything, so nothing resolves.
func insideRoot(arg, root string) (string, bool) {
	if root == "" || arg == "" {
		return "", false
	}

	abs := arg
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(root, abs)
	}

	rel, err := filepath.Rel(root, filepath.Clean(abs))
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return "", false
	}

	return rel, true
}

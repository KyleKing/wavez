package guard

import "strings"

// Declared is one check a project declares for itself, as the name it
// reports under and the command it runs.
type Declared struct {
	Name    string
	Command string
}

// DeclaredCheck reports which of a project's own declared checks command
// re-runs, and the paths that command names beyond the check's own words.
//
// Every gate wavez ships speaks Go, and so did the list this used to be:
// a project declaring `uv run ruff check {files}` had that command
// recognized as nothing, so a run re-ran it a turn at a time. The match is
// on the declaration's leading words, which is what identifies the tool
// being run whatever flags follow.
func DeclaredCheck(command string, declared []Declared) (string, []string, bool) {
	for _, seq := range splitSequence(strings.TrimSpace(command)) {
		tokens := tokenize(seq)
		for _, d := range declared {
			if paths, ok := matchDeclared(tokens, d.Command); ok {
				return d.Name, paths, true
			}
		}
	}

	return "", nil, false
}

// matchDeclared reports whether tokens run declaredCommand, and the path
// arguments tokens adds. A declaration's leading words end at its first
// flag or placeholder, since everything after those varies per invocation.
func matchDeclared(tokens []string, declaredCommand string) ([]string, bool) {
	lead := leadingWords(tokenize(declaredCommand))
	if len(lead) == 0 || len(tokens) < len(lead) {
		return nil, false
	}

	for i, word := range lead {
		if tokens[i] != word {
			return nil, false
		}
	}

	return pathArgs(tokens[len(lead):]), true
}

// leadingWords are the tokens naming the tool a declaration runs, which is
// everything before its first flag or `{files}`-style placeholder.
func leadingWords(tokens []string) []string {
	for i, tok := range tokens {
		if strings.HasPrefix(tok, "-") || strings.HasPrefix(tok, "{") {
			return tokens[:i]
		}
	}

	return tokens
}

// pathArgs are the arguments naming a file or directory, read up to the
// first shell operator so a later pipeline stage donates none of its words.
// A flag written without `=` takes the token after it, which is skipped: a
// value read as a path makes the answer narrower and never wider, since an
// unchanged path is what stops a command being answered from the gates.
func pathArgs(tokens []string) []string {
	var paths []string

	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]

		switch {
		case isOperator(tok):
			return paths
		case strings.HasPrefix(tok, "-"):
			if !strings.Contains(tok, "=") && i+1 < len(tokens) && !isOperator(tokens[i+1]) {
				i++
			}
		case looksLikeTarget(tok):
			paths = append(paths, tok)
		}
	}

	return paths
}

// isOperator names a token that ends the command's own arguments: a
// separator, a pipe, or any redirection.
func isOperator(tok string) bool {
	if isShellOperator(tok) || tok == "&" {
		return true
	}

	return strings.ContainsAny(tok, "<>")
}

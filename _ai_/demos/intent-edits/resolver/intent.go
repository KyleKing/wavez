package main

import (
	"fmt"
	"strconv"
	"strings"
)

// Intent is one of AddFn, AddField, Like, parsed from a single intent line.
type Intent struct {
	Kind string // "add fn", "add field", "like"

	// add fn
	FnName string
	FnSig  string // "(PARAMS) RESULTS" as written
	PkgDir string
	Near   string
	Test   bool
	Doc    string

	// add field
	FieldType  string // "Type.Field"
	FieldFType string
	Tag        string

	// like
	Src string
	Dst string
}

// splitTopLevel splits s on spaces that are not inside parens or quotes.
func splitTopLevel(s string) []string {
	var toks []string
	var cur strings.Builder
	depth := 0
	inQuote := false
	flush := func() {
		if cur.Len() > 0 {
			toks = append(toks, cur.String())
			cur.Reset()
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"' && (i == 0 || s[i-1] != '\\'):
			inQuote = !inQuote
			cur.WriteByte(c)
		case inQuote:
			cur.WriteByte(c)
		case c == '(':
			depth++
			cur.WriteByte(c)
		case c == ')':
			depth--
			cur.WriteByte(c)
		case c == ' ' && depth == 0:
			flush()
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	return toks
}

func unquote(s string) string {
	u, err := strconv.Unquote(s)
	if err != nil {
		return strings.Trim(s, "\"")
	}
	return u
}

// ParseIntent parses one intent line per the grammar in README.md.
func ParseIntent(line string) (*Intent, error) {
	line = strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(line, "add fn "):
		return parseAddFn(line[len("add fn "):])
	case strings.HasPrefix(line, "add field "):
		return parseAddField(line[len("add field "):])
	case strings.HasPrefix(line, "like "):
		return parseLike(line[len("like "):])
	default:
		return nil, fmt.Errorf("unrecognized intent kind in: %q", line)
	}
}

// parseAddFn: NAME(PARAMS) RESULTS in PKGDIR [near SYMBOL] [test=yes|no] [doc="..."]
func parseAddFn(rest string) (*Intent, error) {
	toks := splitTopLevel(rest)
	if len(toks) < 3 {
		return nil, fmt.Errorf("add fn: too few tokens: %q", rest)
	}
	sigTok := toks[0]
	open := strings.Index(sigTok, "(")
	if open < 0 {
		return nil, fmt.Errorf("add fn: missing '(' in signature %q", sigTok)
	}
	it := &Intent{Kind: "add fn", FnName: sigTok[:open], Test: false}

	// Reassemble the full signature: NAME(PARAMS) then RESULTS tokens up to "in".
	sig := sigTok
	i := 1
	for i < len(toks) && toks[i] != "in" {
		sig += " " + toks[i]
		i++
	}
	it.FnSig = strings.TrimPrefix(sig, it.FnName)

	if i >= len(toks) || toks[i] != "in" {
		return nil, fmt.Errorf("add fn: expected 'in PKGDIR' in %q", rest)
	}
	i++
	if i >= len(toks) {
		return nil, fmt.Errorf("add fn: missing PKGDIR in %q", rest)
	}
	it.PkgDir = toks[i]
	i++

	for i < len(toks) {
		switch {
		case toks[i] == "near" && i+1 < len(toks):
			it.Near = toks[i+1]
			i += 2
		case strings.HasPrefix(toks[i], "test="):
			it.Test = strings.TrimPrefix(toks[i], "test=") == "yes"
			i++
		case strings.HasPrefix(toks[i], "doc="):
			it.Doc = unquote(strings.TrimPrefix(toks[i], "doc="))
			i++
		default:
			return nil, fmt.Errorf("add fn: unrecognized token %q in %q", toks[i], rest)
		}
	}
	return it, nil
}

// parseAddField: TYPE.FIELD FTYPE in PKGDIR [tag="..."] [doc="..."]
func parseAddField(rest string) (*Intent, error) {
	toks := splitTopLevel(rest)
	if len(toks) < 4 {
		return nil, fmt.Errorf("add field: too few tokens: %q", rest)
	}
	it := &Intent{Kind: "add field", FieldType: toks[0], FieldFType: toks[1]}
	if toks[2] != "in" {
		return nil, fmt.Errorf("add field: expected 'in PKGDIR', got %q", toks[2])
	}
	it.PkgDir = toks[3]
	for i := 4; i < len(toks); i++ {
		switch {
		case strings.HasPrefix(toks[i], "tag="):
			it.Tag = unquote(strings.TrimPrefix(toks[i], "tag="))
		case strings.HasPrefix(toks[i], "doc="):
			it.Doc = unquote(strings.TrimPrefix(toks[i], "doc="))
		default:
			return nil, fmt.Errorf("add field: unrecognized token %q", toks[i])
		}
	}
	return it, nil
}

// parseLike: SRC: add DST in PKGDIR
func parseLike(rest string) (*Intent, error) {
	parts := strings.SplitN(rest, ":", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("like: expected 'SRC: add DST in PKGDIR', got %q", rest)
	}
	src := strings.TrimSpace(parts[0])
	toks := splitTopLevel(strings.TrimSpace(parts[1]))
	if len(toks) < 4 || toks[0] != "add" || toks[2] != "in" {
		return nil, fmt.Errorf("like: expected 'add DST in PKGDIR', got %q", parts[1])
	}
	return &Intent{Kind: "like", Src: src, Dst: toks[1], PkgDir: toks[3]}, nil
}

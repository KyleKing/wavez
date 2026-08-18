// Package link matches identifiers (PR numbers, issue keys, ticket ids)
// against a pattern table and renders them as links: OSC 8 hyperlinks in the
// terminal, markdown links in text output. DESIGN.md's Thread view section
// names the two sources a table is built from: per-repo entries in
// ".wavez.pkl" and a per-laptop file under config.UserDir().
package link

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"

	"github.com/kyleking/wavez/internal/config"
)

// FileName is the per-laptop link patterns file's name, under wavez's user
// config directory.
const FileName = "links.json"

// Source is one identifier-linking rule before it is compiled: text matching
// Pattern renders as a link to URL, which may reference Pattern's capture
// groups with Go regexp.Expand syntax ("$1").
type Source struct {
	Pattern string `json:"pattern"`
	URL     string `json:"url"`
}

// compiled is one Source with its regexp already validated.
type compiled struct {
	re  *regexp.Regexp
	url string
}

// Table is a compiled, ordered pattern table ready to match text. Its zero
// value matches nothing, so linking a Table nobody loaded is a no-op rather
// than a panic.
type Table struct {
	patterns []compiled
}

// Compile validates and compiles sources in order, the order Table uses to
// break a tie when two patterns match the same span (the earlier source
// wins). It fails on the first pattern that is not a valid Go regexp,
// naming that pattern so a typo in ".wavez.pkl" or links.json is loud
// rather than silently dropped.
func Compile(sources []Source) (Table, error) {
	out := make([]compiled, 0, len(sources))

	for _, s := range sources {
		re, err := regexp.Compile(s.Pattern)
		if err != nil {
			return Table{}, fmt.Errorf("link pattern %q: %w", s.Pattern, err)
		}

		out = append(out, compiled{re: re, url: s.URL})
	}

	return Table{patterns: out}, nil
}

// FromConfig converts a project's ".wavez.pkl" link patterns to Sources, in
// file order, ready to prepend to a per-laptop table so repo entries win
// ties.
func FromConfig(patterns []config.LinkPattern) []Source {
	out := make([]Source, 0, len(patterns))
	for _, p := range patterns {
		out = append(out, Source{Pattern: p.Pattern, URL: p.URL})
	}

	return out
}

// UserPath is the per-laptop link patterns file, under wavez's user config
// directory so personal patterns do not travel with a project.
func UserPath() (string, error) {
	dir, err := config.UserDir()
	if err != nil {
		return "", fmt.Errorf("resolving user config dir: %w", err)
	}

	return filepath.Join(dir, FileName), nil
}

// LoadUser returns the per-laptop link patterns, or nil with no error when
// the file does not exist.
func LoadUser() ([]Source, error) {
	path, err := UserPath()
	if err != nil {
		return nil, err
	}

	return loadFile(path)
}

func loadFile(path string) ([]Source, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- path is a links file this package owns
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}

		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	var sources []Source
	if err := json.Unmarshal(data, &sources); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}

	return sources, nil
}

// LoadAll returns root's compiled link table: root's ".wavez.pkl" entries
// first, then the per-laptop links.json entries, so a repo pattern wins any
// clash with a personal one. Neither source existing is not an error.
func LoadAll(ctx context.Context, root string) (Table, error) {
	loader, err := config.NewLoader(ctx)
	if err != nil {
		return Table{}, fmt.Errorf("starting config loader: %w", err)
	}
	defer loader.Close() //nolint:errcheck // best-effort; the load above already reported any real failure

	cfg, _, err := loader.Load(ctx, root)
	if err != nil {
		return Table{}, fmt.Errorf("loading %s: %w", root, err)
	}

	user, err := LoadUser()
	if err != nil {
		return Table{}, err
	}

	return Compile(append(FromConfig(cfg.Links), user...))
}

// match is one accepted, non-overlapping identifier span.
type match struct {
	submatch   []int
	start, end int
	patternIdx int
}

// matches finds every non-overlapping identifier span in s: candidates from
// every pattern are collected, sorted by start position and then by pattern
// order (an earlier pattern in t wins a tie at the same start), and a
// candidate that overlaps an already-accepted span is dropped rather than
// double-linked.
func (t Table) matches(s string) []match {
	var candidates []match

	for pi, p := range t.patterns {
		for _, idx := range p.re.FindAllStringSubmatchIndex(s, -1) {
			candidates = append(candidates, match{start: idx[0], end: idx[1], patternIdx: pi, submatch: idx})
		}
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].start != candidates[j].start {
			return candidates[i].start < candidates[j].start
		}

		return candidates[i].patternIdx < candidates[j].patternIdx
	})

	var out []match

	lastEnd := 0

	for _, c := range candidates {
		if c.start < lastEnd {
			continue
		}

		out = append(out, c)
		lastEnd = c.end
	}

	return out
}

// apply replaces every matched span in s with wrap(text, url), leaving
// unmatched text untouched.
func (t Table) apply(s string, wrap func(text, url string) string) string {
	spans := t.matches(s)
	if len(spans) == 0 {
		return s
	}

	out := make([]byte, 0, len(s))
	prev := 0

	for _, sp := range spans {
		out = append(out, s[prev:sp.start]...)

		url := string(t.patterns[sp.patternIdx].re.ExpandString(nil, t.patterns[sp.patternIdx].url, s, sp.submatch))
		out = append(out, wrap(s[sp.start:sp.end], url)...)
		prev = sp.end
	}

	out = append(out, s[prev:]...)

	return string(out)
}

// oscHyperlinkStart and oscHyperlinkEnd bracket OSC 8 hyperlink text per the
// terminal escape sequence convention; a terminal that does not support
// hyperlinks renders the bracketed text unchanged, which is the point.
const (
	oscHyperlinkStart = "\x1b]8;;"
	oscHyperlinkMid   = "\x1b\\"
	oscHyperlinkEnd   = "\x1b]8;;\x1b\\"
)

// Linkify wraps every matched identifier in s with an OSC 8 hyperlink escape
// sequence, for the transcript. It is not a color code, so it survives
// NO_COLOR: a terminal without hyperlink support renders the wrapped text
// unchanged.
func (t Table) Linkify(s string) string {
	return t.apply(s, func(text, url string) string {
		return oscHyperlinkStart + url + oscHyperlinkMid + text + oscHyperlinkEnd
	})
}

// Markdown wraps every matched identifier in s as a markdown link, for `-p`
// text output.
func (t Table) Markdown(s string) string {
	return t.apply(s, func(text, url string) string {
		return "[" + text + "](" + url + ")"
	})
}

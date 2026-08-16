package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// FileResult reports one changed file in a Result.
type FileResult struct {
	Path       string `json:"path"`
	LinesAdded int    `json:"lines_added"`
	HoleLines  int    `json:"hole_lines"`
}

// Result is the JSON summary an intent command prints after its diff.
type Result struct {
	Files        []FileResult `json:"files"`
	ImportsAdded []string     `json:"imports_added"`
	Warnings     []string     `json:"warnings"`
	origContents map[string]string
	newContents  map[string]string
}

func newResult() *Result {
	return &Result{
		origContents: map[string]string{},
		newContents:  map[string]string{},
	}
}

// record stores before/after content for a file and tallies added/hole lines.
func (r *Result) record(path, before, after string) {
	r.origContents[path] = before
	r.newContents[path] = after
	added, holes := countAddedAndHoles(before, after)
	r.Files = append(r.Files, FileResult{Path: path, LinesAdded: added, HoleLines: holes})
}

func countAddedAndHoles(before, after string) (added, holes int) {
	bSet := map[string]int{}
	for _, l := range strings.Split(before, "\n") {
		bSet[l]++
	}
	for _, l := range strings.Split(after, "\n") {
		if bSet[l] > 0 {
			bSet[l]--
			continue
		}
		added++
		if strings.Contains(l, "HOLE:") {
			holes++
		}
	}
	return added, holes
}

// PrintDiffs writes a unified diff for every changed file to stdout.
func (r *Result) PrintDiffs() {
	for _, f := range r.Files {
		before := r.origContents[f.Path]
		after := r.newContents[f.Path]
		if before == after {
			continue
		}
		diff, err := unifiedDiff(f.Path, before, after)
		if err != nil {
			fmt.Fprintf(os.Stderr, "diff error for %s: %v\n", f.Path, err)
			continue
		}
		fmt.Print(diff)
	}
}

// PrintJSON writes the machine-readable summary to stdout.
func (r *Result) PrintJSON() {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "json error: %v\n", err)
		return
	}
	fmt.Println(string(b))
}

// unifiedDiff shells out to the system `diff` for a standard unified diff;
// a prototype has no reason to hand-roll Myers diff.
func unifiedDiff(path, before, after string) (string, error) {
	oldF, err := os.CreateTemp("", "intentgo-old-*.go")
	if err != nil {
		return "", err
	}
	defer os.Remove(oldF.Name())
	newF, err := os.CreateTemp("", "intentgo-new-*.go")
	if err != nil {
		return "", err
	}
	defer os.Remove(newF.Name())

	if _, err := oldF.WriteString(before); err != nil {
		return "", err
	}
	if _, err := newF.WriteString(after); err != nil {
		return "", err
	}
	oldF.Close()
	newF.Close()

	cmd := exec.Command("diff", "-u", "--label", path+" (before)", "--label", path+" (after)", oldF.Name(), newF.Name())
	out, _ := cmd.Output() // diff exits 1 when files differ; that's expected
	return string(out), nil
}

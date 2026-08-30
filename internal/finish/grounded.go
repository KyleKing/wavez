package finish

import (
	"os"
	"path/filepath"
	"strings"
)

// Opened answers whether the run read a file, which is what tells a claim
// drawn from the tree apart from one drawn from somewhere else.
type Opened interface {
	Read(abs string) bool
}

// AnswerReadsWhatItNames reports every existing file the closing answer
// names that the run never opened.
//
// It exists for the run that edits nothing. Every other bound here reads a
// change set, and the gates do too, so a run that answers a question without
// writing anything is checked by nobody: one such run reached a confident
// wrong conclusion about a stylesheet and drafted a correction to the
// project's notes from it. Reading the file is not proof the conclusion is
// right, and not reading it is proof the conclusion came from somewhere
// else.
//
// A run that changed something abstains, because a search result is a
// legitimate source for naming a neighboring file in passing, and firing on
// every run that did so would be noise around the case this is for.
func AnswerReadsWhatItNames(root, answer string, changed []string, opened Opened) Report {
	if opened == nil || len(changed) > 0 {
		return Report{}
	}

	var report Report

	for _, path := range dedupe(pathPattern.FindAllString(answer, -1)) {
		if strings.HasPrefix(path, "/") || strings.Contains(path, "..") {
			continue
		}

		abs := filepath.Join(root, filepath.FromSlash(path))
		if _, err := os.Stat(abs); err != nil {
			continue
		}

		if opened.Read(abs) {
			continue
		}

		report.Findings = append(report.Findings, Finding{
			Check: "the answer names a file the run never opened", Detail: path,
		})
	}

	return report
}

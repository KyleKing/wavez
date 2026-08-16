package models

// CopierTemplateInfo describes a repo's copier template provenance, parsed
// from .copier-answers.yml, and how its installed version compares to the
// template's latest upstream tag.
type CopierTemplateInfo struct {
	SrcPath   string
	Commit    string
	IsTag     bool
	LatestTag string
	Behind    bool
}

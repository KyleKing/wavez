package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/kyleking/wavez/internal/tool"
)

const (
	propMilestone = "milestone"
	propShows     = "shows"
	propReading   = "reading"

	// One record per milestone lives under the project's state directory.
	demoDirName = "demos"

	demoDirPerm  = 0o700
	demoFilePerm = 0o600

	// A milestone's filename is bounded here. A longer name still identifies
	// the milestone in the record's own body.
	demoSlugMax = 60
)

// demoSlugStrip is every run of characters a milestone name may hold that a
// filename should not.
var demoSlugStrip = regexp.MustCompile(`[^a-z0-9]+`)

var demoSchema = buildSchema(map[string]schemaProperty{
	propMilestone: {
		Type: schemaTypeString,
		Description: "Short stable name for what is being shown, a few words. The same name " +
			"returns the critique it already got instead of asking again.",
	},
	propShows: {
		Type: schemaTypeString,
		Description: "What works right now, in the words the user will read: what to run or " +
			"open to see it, and what they will see when they do.",
	},
	propReading: {
		Type: schemaTypeString,
		Description: "How you expect this to be used, stated plainly enough to be wrong. " +
			"Name who reaches for it, when, and what it saves them.",
	},
}, propMilestone, propShows, propReading)

// Demo shows the user what a milestone does, says how the run expects it to
// be used, and blocks on being told where that reading is wrong.
//
// Every gate a run passes establishes that the code does what the task said
// and nothing about whether the task was worth doing. That question has one
// answer, the user's, and asking it is what this is for.
//
// The record per milestone is what keeps it from firing per run: a second
// call under a name already shown returns the earlier critique, so a run
// that reaches for it again is handed what it was told rather than asking
// the user twice.
type Demo struct {
	asker Asker
	dir   string
}

// NewDemo builds a Demo that keeps one record per milestone under
// stateDir, the project's own state directory.
func NewDemo(stateDir string, asker Asker) *Demo {
	return &Demo{asker: asker, dir: filepath.Join(stateDir, demoDirName)}
}

// Name implements tool.Tool.
func (*Demo) Name() string { return "demo" }

// Description implements tool.Tool.
func (*Demo) Description() string {
	return "Show the user what a milestone does and ask them to correct how you think it " +
		"will be used. Call it once a milestone is worth looking at, before building on it. " +
		"Passing checks says the code matches the task; only this says the task was worth " +
		"doing. A milestone already shown returns what the user said the first time."
}

// Schema implements tool.Tool.
func (*Demo) Schema() json.RawMessage { return demoSchema }

// Risk implements tool.Tool. A demo stops for a human reading.
func (*Demo) Risk() tool.RiskClass { return tool.RiskExternal }

type demoInput struct {
	Milestone string `json:"milestone"`
	Shows     string `json:"shows"`
	Reading   string `json:"reading"`
}

// Run implements tool.Tool.
func (d *Demo) Run(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	if err := ctx.Err(); err != nil {
		return tool.Result{}, fmt.Errorf("demo: %w", err)
	}

	var in demoInput
	if err := decodeInput(input, &in); err != nil {
		return tool.Fail(tool.CauseMalformed, "invalid input: %v", err), nil
	}

	in.Milestone, in.Shows, in.Reading = strings.TrimSpace(in.Milestone),
		strings.TrimSpace(in.Shows), strings.TrimSpace(in.Reading)

	if failure := demoRequires(in); failure != nil {
		return *failure, nil
	}

	slug := demoSlug(in.Milestone)

	if said, ok := d.recorded(slug); ok {
		return tool.Result{Content: "you already showed " + in.Milestone +
			" and were told:\n\n" + said}, nil
	}

	said, err := d.asker.Ask(ctx, demoPrompt(in))
	if err != nil {
		return tool.Fail(tool.CauseUpstream, "waiting for the critique: %v", err), nil
	}

	if said = strings.TrimSpace(said); said == "" {
		return tool.Fail(tool.CauseUpstream,
			"the user read the demo and said nothing, so the reading is neither "+
				"confirmed nor corrected"), nil
	}

	if err := d.record(slug, in, said); err != nil {
		return tool.Fail(tool.CauseIO, "%v", err), nil
	}

	return tool.Result{Content: "the user says:\n\n" + said +
		"\n\nthis is their reading of what it is for, so it outranks yours"}, nil
}

func demoRequires(in demoInput) *tool.Result {
	var missing string

	switch {
	case in.Milestone == "":
		missing = "milestone is required: it names what is being shown and is what a " +
			"second call is matched against"
	case in.Shows == "":
		missing = "shows is required: the user is being asked to look at something, and a " +
			"call without one does not say what"
	case in.Reading == "":
		missing = "reading is required: the critique corrects how you expect this to be " +
			"used, so state that expectation"
	default:
		return nil
	}

	failure := tool.Fail(tool.CauseBadInput, "%s", missing)

	return &failure
}

// recorded returns what the user said about slug, if this project has shown
// it before. An unreadable record reads as never shown, which costs the user
// a repeat question rather than losing the call.
func (d *Demo) recorded(slug string) (string, bool) {
	//nolint:gosec // slug is demoSlug's output under this tool's own directory
	data, err := os.ReadFile(filepath.Join(d.dir, slug+".md"))
	if err != nil {
		return "", false
	}

	_, said, ok := strings.Cut(string(data), demoSaidHeading)
	if !ok {
		return "", false
	}

	said = strings.TrimSpace(said)

	return said, said != ""
}

const demoSaidHeading = "\n## What the user said\n"

func (d *Demo) record(slug string, in demoInput, said string) error {
	if err := os.MkdirAll(d.dir, demoDirPerm); err != nil {
		return fmt.Errorf("preparing the demo record: %w", err)
	}

	body := "# " + in.Milestone + "\n\n## What was shown\n\n" + in.Shows +
		"\n\n## How the run expected it to be used\n\n" + in.Reading +
		demoSaidHeading + "\n" + said + "\n"

	path := filepath.Join(d.dir, slug+".md")
	if err := os.WriteFile(path, []byte(body), demoFilePerm); err != nil {
		return fmt.Errorf("writing the demo record: %w", err)
	}

	return nil
}

// demoSlug renders a milestone name as a filename. Two names that differ
// only in punctuation or case are the same milestone, which is what makes a
// second call match the first.
func demoSlug(milestone string) string {
	slug := strings.Trim(demoSlugStrip.ReplaceAllString(strings.ToLower(milestone), "-"), "-")
	if slug == "" {
		return "milestone"
	}

	if len(slug) > demoSlugMax {
		slug = strings.Trim(slug[:demoSlugMax], "-")
	}

	return slug
}

func demoPrompt(in demoInput) string {
	return in.Milestone + " is ready to look at.\n\n" + in.Shows +
		"\n\nI expect it to be used like this: " + in.Reading +
		"\n\nWhere is that reading wrong, and what should come next?"
}

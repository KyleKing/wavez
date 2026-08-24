package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kyleking/wavez/internal/tool"
)

const (
	vcsStatus = "status"
	vcsDiff   = "diff"
	vcsLog    = "log"
	// VcsLogLimit bounds the history one call returns. A log is context to
	// orient by, and a run that needs more of it is asking the wrong
	// question of the wrong tool.
	vcsLogLimit = 20
	// DiffHeaderAndRest splits a diff section into its header line and
	// everything under it.
	diffHeaderAndRest = 2
)

var vcsSchema = buildSchema(map[string]schemaProperty{
	"operation": {
		Type: schemaTypeString,
		Enum: []string{vcsStatus, vcsDiff, vcsLog},
		Description: "status lists what this run changed, diff shows the working copy, " +
			"log shows recent history.",
	},
	propPath: {
		Type:        schemaTypeString,
		Description: "With diff, limit it to one file.",
	},
}, "operation")

// VCSReader is the version-control history and working copy, read only.
// Every method reports; none of them writes, which is the point: a run
// reaches version control through this and never through the shell, so
// there is no verb here to force a push, rewrite history, or commit with
// git in a checkout jj owns.
type VCSReader interface {
	WorkingCopyDiff(ctx context.Context, repoRoot string) (string, error)
	Log(ctx context.Context, repoRoot string, limit int) (string, error)
}

// VCS answers what a run asked jj and git for. Across 278 logged shell
// calls 24 inspected version-control state, and each spent a turn on a
// question the harness could answer without running anything: status comes
// from the changes this run recorded as it made them, rather than from the
// tree.
type VCS struct {
	repo    VCSReader
	changes Changes
	root    string
}

// NewVCS builds a VCS tool over repo, reporting this run's own changes from
// changes. A nil changes reports the working copy instead.
func NewVCS(root string, repo VCSReader, changes Changes) *VCS {
	return &VCS{root: root, repo: repo, changes: changes}
}

// Name implements tool.Tool.
func (*VCS) Name() string { return "vcs" }

// Description implements tool.Tool.
func (*VCS) Description() string {
	return "Read version control: what this run changed, the working copy's diff, or recent " +
		"history. Nothing here writes."
}

// Schema implements tool.Tool.
func (*VCS) Schema() json.RawMessage { return vcsSchema }

type vcsInput struct {
	Operation string `json:"operation"`
	Path      string `json:"path"`
}

// Run implements tool.Tool.
func (v *VCS) Run(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	if err := ctx.Err(); err != nil {
		return tool.Result{}, fmt.Errorf("vcs: %w", err)
	}

	var in vcsInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.Fail(tool.CauseMalformed, "invalid input: %v", err), nil
	}

	switch in.Operation {
	case vcsStatus:
		return v.status(), nil
	case vcsDiff:
		return v.diff(ctx, in.Path), nil
	case vcsLog:
		return v.log(ctx), nil
	case "":
		return tool.Fail(tool.CauseBadInput, "operation is required: %s, %s, or %s",
			vcsStatus, vcsDiff, vcsLog), nil
	default:
		return tool.Fail(tool.CauseBadInput, "unknown operation %q: this tool reads %s, %s, and %s, "+
			"and writes nothing", in.Operation, vcsStatus, vcsDiff, vcsLog), nil
	}
}

func (v *VCS) status() tool.Result {
	if v.changes == nil {
		return tool.Fail(tool.CauseUpstream, "this run is not recording its changes")
	}

	changed := dedupeChanges(v.changes.Changed())
	if len(changed) == 0 {
		return tool.Result{Content: "this run has changed no files"}
	}

	var b strings.Builder

	fmt.Fprintf(&b, "this run has changed %d file(s):\n", len(changed))

	for _, c := range changed {
		fmt.Fprintf(&b, "  %s +%d -%d\n", c.Path, c.Added, c.Removed)
	}

	return tool.Result{Content: b.String()}
}

func (v *VCS) diff(ctx context.Context, path string) tool.Result {
	out, err := v.repo.WorkingCopyDiff(ctx, v.root)
	if err != nil {
		return tool.Fail(tool.CauseUpstream, "%v", err)
	}

	if path != "" {
		out = diffForPath(out, path)
	}

	if strings.TrimSpace(out) == "" {
		return tool.Result{Content: "the working copy has no uncommitted changes"}
	}

	return tool.Result{Content: out}
}

func (v *VCS) log(ctx context.Context) tool.Result {
	out, err := v.repo.Log(ctx, v.root, vcsLogLimit)
	if err != nil {
		return tool.Fail(tool.CauseUpstream, "%v", err)
	}

	if strings.TrimSpace(out) == "" {
		return tool.Result{Content: "no commits"}
	}

	return tool.Result{Content: out}
}

// diffForPath keeps the sections of a git-format diff that name path. A
// path the diff does not mention yields nothing, which reads as "that file
// is unchanged" rather than as an error.
func diffForPath(diff, path string) string {
	var kept []string

	for _, section := range strings.Split(diff, "\ndiff --git ") {
		if section == "" {
			continue
		}

		if !strings.HasPrefix(section, "diff --git ") {
			section = "diff --git " + section
		}

		if strings.Contains(strings.SplitN(section, "\n", diffHeaderAndRest)[0], path) {
			kept = append(kept, section)
		}
	}

	return strings.Join(kept, "\n")
}

package adversarial

import (
	"context"
	"fmt"

	"github.com/kyleking/what-did-ai-do/internal/gitstate"
	"github.com/kyleking/what-did-ai-do/internal/llm"
	"github.com/kyleking/what-did-ai-do/internal/session"
)

// Finding is one flagged decision, surviving both the conservative
// confidence filter and the superseded/gone file-state filter.
type Finding struct {
	Candidate  Candidate
	FileStates []gitstate.FileState
	Judgment   llm.Judgment
}

// Report is the outcome of analyzing one session.
type Report struct {
	SessionID string
	Findings  []Finding
	Analyzed  int
}

// fileStateSummary is the minimal per-decision file-state info threaded
// into the user prompt; the full gitstate.FileState is kept separately on
// Finding for the report renderer.
type fileStateSummary struct {
	Status gitstate.Status
}

// judgeClient is the narrow slice of *llm.Client this package needs,
// defined here (consumer side) so tests can substitute a fake without
// llm needing to expose its internal command-running seam.
type judgeClient interface {
	Judge(ctx context.Context, systemPrompt, userPrompt, jsonSchema string) ([]llm.Judgment, error)
}

// Analyzer judges a session's decisions for "AI slop" in one batched call.
type Analyzer struct {
	Client judgeClient
}

// New returns an Analyzer using client for judgments.
func New(client judgeClient) *Analyzer {
	return &Analyzer{Client: client}
}

// Analyze resolves each decision's file state against the working tree,
// skips decisions whose only files are superseded or gone (nothing to
// flag), and sends the rest to the LLM in a single batched call.
func (a *Analyzer) Analyze(ctx context.Context, s *session.Session) (Report, error) {
	candidates := CandidatesFrom(s)

	states := make(map[string]fileStateSummary, len(candidates))
	fileStates := make(map[string][]gitstate.FileState, len(candidates))
	live := make([]Candidate, 0, len(candidates))

	for i := range candidates {
		c := &candidates[i]

		fs, worthAnalyzing := resolveCandidateFiles(ctx, s.ProjectPath, c)
		if !worthAnalyzing {
			continue
		}

		fileStates[c.Decision.ID] = fs
		if len(fs) > 0 {
			states[c.Decision.ID] = fileStateSummary{Status: fs[0].Status}
		}

		live = append(live, *c)
	}

	if len(live) == 0 {
		return Report{SessionID: s.ID}, nil
	}

	systemPrompt, err := buildSystemPrompt()
	if err != nil {
		return Report{}, err
	}

	judgments, err := a.Client.Judge(
		ctx,
		systemPrompt,
		buildUserPrompt(live, states),
		judgmentArraySchema,
	)
	if err != nil {
		return Report{}, fmt.Errorf("judging session %s: %w", s.ID, err)
	}

	byDecisionID := make(map[string]llm.Judgment, len(judgments))
	for _, j := range judgments {
		byDecisionID[j.DecisionID] = j
	}

	findings := make([]Finding, 0, len(live))

	for i := range live {
		c := live[i]

		j, ok := byDecisionID[c.Decision.ID]
		if !ok || j.Assessment == "sound" || j.Confidence < minConfidenceToFlag {
			continue
		}

		findings = append(
			findings,
			Finding{Candidate: c, FileStates: fileStates[c.Decision.ID], Judgment: j},
		)
	}

	return Report{SessionID: s.ID, Analyzed: len(live), Findings: findings}, nil
}

// resolveCandidateFiles resolves the working-tree state of every file a
// candidate touched. It reports worthAnalyzing=false when the candidate
// named files but every one of them is superseded or gone — there's
// nothing left to judge, so it's skipped before the (paid) LLM call rather
// than filtered out after.
func resolveCandidateFiles(
	ctx context.Context,
	projectPath string,
	c *Candidate,
) ([]gitstate.FileState, bool) {
	if len(c.Decision.Files) == 0 {
		return nil, true
	}

	states := make([]gitstate.FileState, 0, len(c.Decision.Files))
	anyLive := false

	for _, file := range c.Decision.Files {
		state, err := gitstate.Resolve(ctx, projectPath, file, expectedTextForFile(c, file))
		if err != nil {
			anyLive = true // resolution failed; don't silently drop a candidate we couldn't verify

			continue
		}

		states = append(states, state)

		if state.Status == gitstate.StatusLive || state.Status == gitstate.StatusUnknown {
			anyLive = true
		}
	}

	return states, anyLive
}

func expectedTextForFile(c *Candidate, file string) string {
	for i := range c.ToolCalls {
		if containsFile(c.ToolCalls[i].Files, file) {
			return expectedText(&c.ToolCalls[i])
		}
	}

	return ""
}

func containsFile(files []string, target string) bool {
	for _, f := range files {
		if f == target {
			return true
		}
	}

	return false
}

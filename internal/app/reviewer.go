package app

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/kyleking/wavez/internal/agent"
	"github.com/kyleking/wavez/internal/llm"
	"github.com/kyleking/wavez/internal/router"
	"github.com/kyleking/wavez/internal/tool"
)

const (
	// The ceiling on a whole review request, well past what any tier reads
	// well and set so a change nobody could review in one pass is reported
	// unreviewed rather than reviewed in part: a verdict on a truncated diff
	// answers a different question than the one that was asked.
	reviewTokenBudget = 60000
	// The tail of a task is what states the requirement often enough that the
	// head is the half to drop.
	reviewTaskBudgetBytes = 1500
	// A verdict is one enum and one sentence.
	reviewMaxTokens = 200
	// A verdict should not vary between runs on the same diff. Zero is not
	// available: llm.Request omits an unset Temperature, which leaves the
	// server's own default (0.8 on llama-server) in place.
	reviewTemperature = 0.01
)

// reviewSystem is written for an 8B local model: one question, the failure
// mode a deterministic check cannot see, and no invitation to review
// anything else.
const reviewSystem = "You check one code change against the task it was asked to do.\n\n" +
	"You are given the task text and the diff a coding agent produced. " +
	"Answer one question: does the diff do what the task asked?\n\n" +
	"Read the task as a list of requirements. Check each one against the diff. " +
	`Answer "ok" when the diff meets every requirement, even if it meets one of them by leaving code alone. ` +
	`Answer "objection" when the diff meets only some of them, or does something the task did not ask for.` +
	"\n\n" +
	"Judge the task only. Style, naming, missing tests, and unrelated improvements are not your concern, " +
	"and neither is code the diff does not touch. " +
	"When you object, name the requirement the diff fails in one sentence."

// reviewSchema constrains the verdict to an enum and a sentence, so a small
// model cannot answer in prose.
var reviewSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "verdict": {"type": "string", "enum": ["ok", "objection"]},
    "reason": {"type": "string"}
  },
  "required": ["verdict", "reason"],
  "additionalProperties": false
}`)

// Differ produces the unified diff of what a run changed since its
// checkpoint. *vcs.Jj implements it.
type Differ interface {
	Diff(ctx context.Context, repoRoot, marker string, files []string) (string, error)
}

// ModelReviewer is the agent.Reviewer a real project wires in: it reads the
// run's diff out of jj, asks a model whether that diff does what the task
// asked, and returns the answer as a verdict. It never fails a run, so every
// way it can fail (an unreadable diff, a diff over the token budget, a model
// that answers off-schema) comes back as agent.ReviewSkipped naming the
// reason rather than as a pass.
//
// The tier is not fixed: the same router that routes a turn routes the
// review, from the fast tier up. A review reads one finished diff whose only
// cost is its length, so it starts on the cheapest tier and moves up only
// when the diff will not fit there, which is what keeps the ordinary
// source-plus-test change on-box.
type ModelReviewer struct {
	providers   router.Tiers[llm.Provider]
	differ      Differ
	root        string
	models      router.Tiers[string]
	tokenBudget int
}

// NewModelReviewer builds a reviewer for the project at root, reading diffs
// through differ and asking whichever tier the router picks.
func NewModelReviewer(
	root string, differ Differ, providers router.Tiers[llm.Provider], models router.Tiers[string],
) *ModelReviewer {
	return &ModelReviewer{
		root: root, differ: differ, providers: providers,
		models: models, tokenBudget: reviewTokenBudget,
	}
}

// Review implements agent.Reviewer.
func (r *ModelReviewer) Review(ctx context.Context, rv agent.Review) agent.Verdict {
	paths := changedPaths(rv.Changes)

	diff, err := r.differ.Diff(ctx, r.root, rv.Checkpoint, paths)
	if err != nil {
		return skipped("the run's diff could not be read: %v", err)
	}

	if strings.TrimSpace(diff) == "" {
		return skipped("the run's diff is empty, so there is nothing to review")
	}

	prompt := reviewPrompt(rv.Task, diff)

	estimate := estimateTokens(reviewSystem + prompt)
	if estimate > r.tokenBudget {
		return skipped("the diff is about %d tokens across %d file(s), over the %d-token review budget",
			estimate, len(paths), r.tokenBudget)
	}

	route := router.Route(router.Input{Override: router.ChoiceFast, EstimatedTokens: estimate})

	req := llm.Request{
		Model:          r.models.For(route),
		System:         reviewSystem,
		Messages:       []llm.Message{{Role: llm.RoleUser, Content: prompt}},
		MaxTokens:      reviewMaxTokens,
		Temperature:    reviewTemperature,
		ResponseFormat: &llm.ResponseFormat{Name: "review_verdict", Schema: reviewSchema},
	}

	answer, err := collectText(ctx, r.providers.For(route), req)
	if err != nil {
		return skipped("the reviewer model failed: %v", err)
	}

	return parseVerdict(answer)
}

func reviewPrompt(task, diff string) string {
	return "## Task\n\n" + trimTask(task) + "\n\n## Diff\n\n```diff\n" + diff + "\n```\n\n" +
		"Does this diff do what the task asked?"
}

// trimTask keeps the tail of an oversized task, which is where a task states
// what it wants often enough that the head is the half to drop.
func trimTask(task string) string {
	if len(task) <= reviewTaskBudgetBytes {
		return task
	}

	return "[...]\n" + task[len(task)-reviewTaskBudgetBytes:]
}

// parseVerdict reads the constrained answer. A provider that ignores the
// schema may wrap the object in prose, so the outermost braces are taken
// before decoding; anything that still will not decode is unreviewed rather
// than approved.
func parseVerdict(answer string) agent.Verdict {
	var out struct {
		Verdict string `json:"verdict"`
		Reason  string `json:"reason"`
	}

	if err := json.Unmarshal([]byte(jsonObject(answer)), &out); err != nil {
		return skipped("the reviewer answered off-schema: %s", firstLine(answer))
	}

	switch out.Verdict {
	case "ok":
		return agent.Verdict{Result: agent.ReviewOK}
	case "objection":
		return agent.Verdict{Result: agent.ReviewObjection, Note: reasonOrDefault(out.Reason)}
	default:
		return skipped("the reviewer returned no verdict: %s", firstLine(answer))
	}
}

func reasonOrDefault(reason string) string {
	if strings.TrimSpace(reason) == "" {
		return "the reviewer objected without giving a reason"
	}

	return strings.TrimSpace(reason)
}

func jsonObject(s string) string {
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start < 0 || end < start {
		return s
	}

	return s[start : end+1]
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}

	const maxLen = 120
	if len(s) > maxLen {
		s = s[:maxLen]
	}

	return s
}

func skipped(format string, args ...any) agent.Verdict {
	return agent.Verdict{Result: agent.ReviewSkipped, Note: fmt.Sprintf(format, args...)}
}

func changedPaths(changes []tool.Change) []string {
	seen := make(map[string]struct{}, len(changes))
	out := make([]string, 0, len(changes))

	for _, c := range changes {
		if _, ok := seen[c.Path]; ok {
			continue
		}

		seen[c.Path] = struct{}{}

		out = append(out, c.Path)
	}

	sort.Strings(out)

	return out
}

func collectText(ctx context.Context, provider llm.Provider, req llm.Request) (string, error) {
	var text strings.Builder

	for chunk, err := range provider.Stream(ctx, req) {
		if err != nil {
			return "", fmt.Errorf("streaming review: %w", err)
		}

		if chunk.Kind == llm.ChunkText {
			text.WriteString(chunk.Text)
		}
	}

	return text.String(), nil
}

func estimateTokens(s string) int {
	return len(s) / 4 //nolint:mnd // char/4 is the project's documented token estimate heuristic
}

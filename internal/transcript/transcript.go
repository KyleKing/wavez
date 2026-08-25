// Package transcript replays a recorded run's model turns against the real
// harness so a change to what the harness says is verifiable without a
// model.
//
// It is valid only for changes that do not alter what the model sees. The
// turns are frozen, so a fixture proves that a gate message, a tool result,
// or a reduced payload still reads the way it did, and proves nothing about
// how a model would react to a new one.
package transcript

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kyleking/wavez/internal/agent"
	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/llm"
	"github.com/kyleking/wavez/internal/llm/fake"
	"github.com/kyleking/wavez/internal/permission"
	"github.com/kyleking/wavez/internal/router"
	"github.com/kyleking/wavez/internal/thread"
	"github.com/kyleking/wavez/internal/tool"
	"github.com/kyleking/wavez/internal/tools"
)

const (
	fixtureDirMode  = 0o750
	fixtureFileMode = 0o600
)

// Fixture is one frozen run: the files it starts from, the prompt it was
// given, and every model turn it produced, in order.
type Fixture struct {
	// Files seeds the workspace, keyed by path relative to its root. A tool
	// reads and writes these for real, which is the point: a fixture that
	// stubbed the tools would assert the stub.
	Files map[string]string `json:"files"`
	Task  string            `json:"task"`
	// Checks is what the harness already knows about the tree when a run
	// reaches for the shell to re-run the project's own checks, empty when
	// it knows nothing.
	Checks string `json:"checks,omitempty"`
	Turns  []Turn `json:"turns"`
}

// Turn is one model turn. Gate is what the gates report after this turn's
// tool calls, empty for a turn that triggered none.
type Turn struct {
	Text  string     `json:"text,omitempty"`
	Gate  string     `json:"gate,omitempty"`
	Calls []CallSpec `json:"calls,omitempty"`
}

// CallSpec is one tool call the model made. Input is held as raw JSON so a
// fixture can freeze a malformed emission exactly as it was recorded.
type CallSpec struct {
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// Load reads one fixture from path.
func Load(path string) (Fixture, error) {
	body, err := os.ReadFile(path) //nolint:gosec // fixtures are named by the test that owns them
	if err != nil {
		return Fixture{}, fmt.Errorf("reading fixture: %w", err)
	}

	var f Fixture
	if err := json.Unmarshal(body, &f); err != nil {
		return Fixture{}, fmt.Errorf("parsing fixture: %w", err)
	}

	return f, nil
}

// scriptedGate hands back the fixture's own gate feedback, one turn at a
// time. It stands in for a gate.Runner because a fixture asserts the
// wording the model receives, and running the real gates would make that
// wording depend on the machine.
type scriptedGate struct {
	feedback []string
	next     int
}

func (g *scriptedGate) Begin() { g.next = 0 }

func (*scriptedGate) Enqueue(tool.Change) {}

func (*scriptedGate) FalseAlarms() []string { return nil }

// A fixture scripts the gate feedback verbatim, so nothing here is stuck:
// the routing signal is about a gate the run failed to move, and a replay
// asserts what the model saw rather than where it ran.
func (*scriptedGate) Stuck() (string, bool) { return "", false }

func (g *scriptedGate) TakeFeedback() (string, bool) {
	if g.next >= len(g.feedback) {
		return "", false
	}

	out := g.feedback[g.next]
	g.next++

	return out, strings.Contains(out, "found this")
}

// Replay runs f against the real loop and tool surface under root, and
// returns the frame a golden file holds.
func Replay(ctx context.Context, f Fixture, root, logDir string) (string, error) {
	if err := seed(root, f.Files); err != nil {
		return "", err
	}

	th, err := thread.Open(logDir, "fixture", []string{root})
	if err != nil {
		return "", fmt.Errorf("opening thread: %w", err)
	}
	defer th.Close() //nolint:errcheck // the frame is already rendered from the log

	provider := fake.New("fixture", script(f)...)
	registry := registryFor(root, logDir, f.Checks)

	loop := agent.New(
		router.Tiers[llm.Provider]{Fast: provider, Balanced: provider, Deep: provider},
		registry, permission.AllowAll(),
		agent.WithChangeGate(&scriptedGate{feedback: gateFeedback(f)}),
		agent.WithMaxTurns(len(f.Turns)+1),
	)

	prefix := agent.Prefix{System: "fixture", Tools: specs(registry)}

	outcome, err := loop.Run(ctx, th, prefix, f.Task, router.Input{Override: router.ChoiceFast})
	if err != nil {
		return "", fmt.Errorf("replaying: %w", err)
	}

	events, err := th.Log().Since(0)
	if err != nil {
		return "", fmt.Errorf("reading the log: %w", err)
	}

	return render(events, outcome), nil
}

func seed(root string, files map[string]string) error {
	for _, rel := range sortedKeys(files) {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), fixtureDirMode); err != nil {
			return fmt.Errorf("seeding %s: %w", rel, err)
		}

		if err := os.WriteFile(abs, []byte(files[rel]), fixtureFileMode); err != nil {
			return fmt.Errorf("seeding %s: %w", rel, err)
		}
	}

	return nil
}

func sortedKeys(files map[string]string) []string {
	out := make([]string, 0, len(files))
	for k := range files {
		out = append(out, k)
	}

	sort.Strings(out)

	return out
}

func script(f Fixture) []fake.Turn {
	out := make([]fake.Turn, 0, len(f.Turns))

	for i := range f.Turns {
		turn := fake.Turn{StopReason: llm.StopEndTurn}
		if f.Turns[i].Text != "" {
			turn.Text = []string{f.Turns[i].Text}
		}

		for j, call := range f.Turns[i].Calls {
			turn.ToolCalls = append(turn.ToolCalls, llm.ToolCall{
				ID: fmt.Sprintf("c%d-%d", i, j), Name: call.Name, Input: call.Input,
			})
			turn.StopReason = llm.StopToolUse
		}

		out = append(out, turn)
	}

	return out
}

func gateFeedback(f Fixture) []string {
	out := make([]string, 0, len(f.Turns))
	for i := range f.Turns {
		out = append(out, f.Turns[i].Gate)
	}

	return out
}

// registryFor is the file-level tool surface, which is every tool a fixture
// can drive without an index, a language server, or a daemon.
func registryFor(root, sessionTmp, checks string) *tool.Registry {
	scope := tools.NewScope(false)

	return tool.NewRegistry(
		tools.NewList(root),
		tools.NewRead(root, scope),
		tools.NewShell(root, sessionTmp, "fixture", permission.AllowAll(),
			tools.WithChecks(frozenChecks(checks))),
		tools.NewStrReplace(root, scope),
		tools.NewWrite(root, scope),
	)
}

// frozenChecks is the gate state a fixture declares, so a run reaching for
// the shell gets the same answer on every machine.
type frozenChecks string

func (c frozenChecks) Status() (string, bool) { return string(c), c != "" }

func specs(registry *tool.Registry) []llm.ToolSpec {
	built := registry.Specs()
	out := make([]llm.ToolSpec, 0, len(built))

	for _, s := range built {
		out = append(out, llm.ToolSpec{Name: s.Name, Description: s.Description, Schema: s.Schema})
	}

	return out
}

// render is the frame a golden file holds: every event the run logged, in
// order, with the volatile fields left out. Timestamps, sequence numbers,
// and elapsed time all vary between runs of the same fixture, and a golden
// that carried them would fail for the machine rather than for the change.
func render(events []event.Event, outcome agent.Outcome) string {
	var b strings.Builder

	for i := range events {
		line := describe(events[i])
		if line == "" {
			continue
		}

		b.WriteString(line + "\n")
	}

	fmt.Fprintf(&b, "\noutcome %s: %d turns, %d tool calls\n",
		outcome.Stop, outcome.Turns, outcome.ToolCalls)

	if outcome.Reason != "" {
		fmt.Fprintf(&b, "reason: %s\n", outcome.Reason)
	}

	return b.String()
}

func describe(ev event.Event) string {
	switch ev.Kind {
	case event.KindUser:
		return "user: " + indent(ev.Text)
	case event.KindAgent:
		if ev.Text == "" {
			return ""
		}

		return "agent: " + indent(ev.Text)
	case event.KindTool:
		return toolLine(ev)
	case event.KindError:
		return "error: " + indent(ev.Text)
	case event.KindGate, event.KindPermission, event.KindState, event.KindLedger, event.KindUsage,
		event.KindCycle, event.KindHypothesis, event.KindGoal, event.KindReview:
		return ""
	default:
		return ""
	}
}

func toolLine(ev event.Event) string {
	var head strings.Builder

	head.WriteString("tool " + ev.Tool)

	if isError(ev) {
		head.WriteString(" failed")
	}

	if cause, ok := ev.Detail["cause"].(string); ok && cause != "" {
		head.WriteString(" (" + cause + ")")
	}

	for _, c := range ev.Changes {
		fmt.Fprintf(&head, " [%s +%d/-%d]", c.Path, c.Added, c.Removed)
	}

	return head.String() + ": " + indent(ev.Text)
}

func isError(ev event.Event) bool {
	err, ok := ev.Detail["is_error"].(bool)

	return ok && err
}

// indent keeps a multi-line result readable in the frame and keeps every
// line of it, since the wording is what the fixture asserts.
func indent(text string) string {
	return strings.ReplaceAll(strings.TrimRight(text, "\n"), "\n", "\n    ")
}

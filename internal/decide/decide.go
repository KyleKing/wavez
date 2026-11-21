// Package decide asks a small model to pick one member of a set that code
// declared. The caller's rules answer first, a model answers only what no
// rule could, and anything that goes wrong returns the question's fallback,
// which is the path that ran before the question existed.
//
// A Decider is built with New and its options, or with FromConfig for the
// one a project's .wavez.toml describes:
//
//	d := decide.New(
//		decide.WithBackend(decide.NewOpenAI(endpoint, model, timeout)),
//		decide.WithCache(dir),
//		decide.WithLog(path),
//	)
//	got := d.Decide(ctx, question, state, rules...)
//
// Only a model's answer is cached, and every decision is appended to the
// log with enough of the state to label it later.
//
// # Deciding who should hold a question
//
// A labeled corpus says whether a rule, a model, or neither should answer a
// question. Each line of the corpus is one JSON object,
// {"state": "...", "want": "<choice key>"}:
//
//	cases, err := decide.ReadCorpus(file)
//	report := decide.Evaluate(ctx, d, question, rules, cases)
//	fmt.Print(report.Render())
//
// The report counts, for each backend that answered, how many cases it
// took and how many of those it got right, and lists every wrong case. To
// evaluate a hosted model, give the Decider NewClaudeCommand as its backend;
// that backend starts a process per case and is too slow for a hook.
// Evaluate runs the Decider it is handed, so one built with WithLog also
// logs every case and one built with WithCache answers a repeat run from
// the cache.
package decide

import (
	"context"
	"time"
)

// Choice is one member of a question's closed set.
type Choice struct {
	// Key is what the caller switches on. A model never writes it: the
	// answer is matched against the declared keys and dropped otherwise.
	Key string
	// Description tells the model when this member is the right one.
	Description string
}

// Question is a closed-set pick. The set, the instructions, and the
// fallback are constants in code, so nothing a model returns can widen what
// the caller does.
type Question struct {
	// Name identifies the question in the log, the cache, and the corpus.
	Name string
	// Instructions is what is being asked about the state.
	Instructions string
	// Fallback is the key returned when no rule fires and no model
	// answers. It is the behavior without the question.
	Fallback string
	// Choices is the closed set, two members or more.
	Choices []Choice
}

// Rule answers a question from the state without a model. It returns a key
// and true when it is sure, and false to pass. A rule fires only in the
// direction a corpus has shown safe.
type Rule func(state string) (key string, ok bool)

// The values Decision.Backend takes when no model answered.
const (
	BackendRule     = "rule"
	BackendCache    = "cache"
	BackendFallback = "fallback"
)

// Decision is what was picked and who picked it.
// The field order is the JSON key order of the decision log.
type Decision struct {
	Question string `json:"question"`
	Choice   string `json:"choice"`
	// Backend is BackendRule, BackendCache, BackendFallback, or the name
	// of the model backend that answered.
	Backend string `json:"backend"`
	Model   string `json:"model,omitempty"`
	// Reason says why a fallback was a fallback. It is for the log and
	// never for a prompt.
	Reason       string        `json:"reason,omitempty"`
	Latency      time.Duration `json:"latency_ns"`
	InputTokens  int           `json:"input_tokens,omitempty"`
	OutputTokens int           `json:"output_tokens,omitempty"`
}

// Answer is what a backend returned for one question.
type Answer struct {
	// Key is one of the question's choice keys.
	Key          string
	Model        string
	InputTokens  int
	OutputTokens int
}

// Backend is a model that can answer a Question. An error of any kind,
// including an answer outside the set, makes the Decider fall back.
type Backend interface {
	Name() string
	Answer(ctx context.Context, q Question, state string) (Answer, error)
}

// Decider answers questions: rules, then the cache, then the backend, then
// the fallback. Its signature is frozen, because internal/route and
// internal/hook are written against it.
type Decider interface {
	// Decide never fails. A question that cannot be answered returns its
	// fallback with the reason recorded.
	Decide(ctx context.Context, q Question, state string, rules ...Rule) Decision
}

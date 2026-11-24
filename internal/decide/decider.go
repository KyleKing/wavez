package decide

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"
)

// errPanic is what a rule or a backend that panicked is reported as.
var errPanic = errors.New("panic")

// Engine answers questions: rules, then the backend, then the fallback.
// It is safe for concurrent use when its backend is.
type Engine struct {
	backend Backend
	log     string
}

// Option configures an Engine.
type Option func(*Engine)

// WithBackend sets the model asked when no rule fires. Without one, a
// question no rule answers returns its fallback.
func WithBackend(b Backend) Option {
	return func(d *Engine) { d.backend = b }
}

// WithLog appends every Decision to the file at path as one JSON line,
// with a hash and an excerpt of the state it was made from. The file is
// rotated once it passes a megabyte. A write that fails is dropped and the
// decision stands.
func WithLog(path string) Option {
	return func(d *Engine) { d.log = path }
}

// New returns an Engine.
func New(opts ...Option) *Engine {
	d := &Engine{}
	for _, opt := range opts {
		opt(d)
	}

	return d
}

// Decide never fails. A question that cannot be answered returns its
// fallback with the reason recorded.
func (d *Engine) Decide(ctx context.Context, q Question, state string, rules ...Rule) Decision {
	start := time.Now()
	out := Decision{Question: q.Name, Choice: q.Fallback, Backend: BackendFallback}

	if key, ok := applyRules(q, state, rules); ok {
		out.Choice, out.Backend = key, BackendRule
	} else {
		d.ask(ctx, q, state, &out)
	}

	out.Latency = time.Since(start)

	if d.log != "" {
		appendLog(d.log, out, state)
	}

	return out
}

// applyRules returns the first key a rule is sure of. A rule that names a
// key outside the set, or that panics, has passed.
func applyRules(q Question, state string, rules []Rule) (string, bool) {
	for _, rule := range rules {
		var (
			key string
			ok  bool
		)

		err := safely(func() { key, ok = rule(state) })
		if err == nil && ok && q.has(key) {
			return key, true
		}
	}

	return "", false
}

// ask fills out from the backend, and leaves it as the fallback with a
// reason when the backend does not give a member of the set.
func (d *Engine) ask(ctx context.Context, q Question, state string, out *Decision) {
	if d.backend == nil {
		out.Reason = "no backend"

		return
	}

	var (
		name   string
		answer Answer
		err    error
	)

	panicked := safely(func() {
		name = d.backend.Name()
		answer, err = d.backend.Answer(ctx, q, state)
	})
	if panicked != nil {
		err = panicked
	}

	switch {
	case err != nil:
		out.Reason = err.Error()
	case !q.has(answer.Key):
		out.Reason = "answer outside the set"
	default:
		out.Choice, out.Backend, out.Model = answer.Key, name, answer.Model
		out.InputTokens, out.OutputTokens = answer.InputTokens, answer.OutputTokens
	}
}

// safely runs fn and turns a panic in it into an error, because a rule or
// a backend is someone else's code and Decide never fails.
func safely(fn func()) error {
	var err error

	func() {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("%w: %v", errPanic, r)
			}
		}()

		fn()
	}()

	return err
}

func (q Question) has(key string) bool {
	return slices.ContainsFunc(q.Choices, func(c Choice) bool { return c.Key == key })
}

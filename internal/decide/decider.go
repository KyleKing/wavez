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

type decider struct {
	backend Backend
	now     func() time.Time
	cache   string
	log     string
}

// Option configures a Decider.
type Option func(*decider)

// WithBackend sets the model asked when no rule fires. Without one, a
// question no rule answers returns its fallback.
func WithBackend(b Backend) Option {
	return func(d *decider) { d.backend = b }
}

// WithCache keeps each model answer as a file under dir, so the same
// question over the same state is asked once. A hit comes back with
// Decision.Backend set to BackendCache. Rule answers and fallbacks are never
// stored, and an entry that cannot be read is a miss.
func WithCache(dir string) Option {
	return func(d *decider) { d.cache = dir }
}

// WithLog appends every Decision to the file at path as one JSON line,
// with a hash and an excerpt of the state it was made from. A write that
// fails is dropped and the decision stands.
func WithLog(path string) Option {
	return func(d *decider) { d.log = path }
}

// WithClock replaces time.Now for the log's timestamp and the measured
// latency, so a test gets the same line twice.
func WithClock(now func() time.Time) Option {
	return func(d *decider) { d.now = now }
}

// New returns a Decider.
//
//nolint:ireturn // Decider is the frozen surface internal/route and internal/hook consume
func New(opts ...Option) Decider {
	d := &decider{now: time.Now}
	for _, opt := range opts {
		opt(d)
	}

	return d
}

func (d *decider) Decide(ctx context.Context, q Question, state string, rules ...Rule) Decision {
	start := d.now()
	out := Decision{Question: q.Name, Choice: q.Fallback, Backend: BackendFallback}

	if key, ok := applyRules(q, state, rules); ok {
		out.Choice, out.Backend = key, BackendRule
	} else {
		d.ask(ctx, q, state, &out)
	}

	out.Latency = d.now().Sub(start)
	d.record(out, state)

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

// ask fills out from the cache or the backend, and leaves it as the
// fallback with a reason when neither gives a member of the set.
func (d *decider) ask(ctx context.Context, q Question, state string, out *Decision) {
	if d.backend == nil {
		out.Reason = "no backend"

		return
	}

	var (
		name, model string
		answer      Answer
		err         error
	)

	if panicked := safely(func() { name, model = d.backend.Name(), backendModel(d.backend) }); panicked != nil {
		out.Reason = panicked.Error()

		return
	}

	key := cacheKey(q, name, model, state)

	if hit, ok := d.cached(q, key); ok {
		out.Choice, out.Backend, out.Model = hit.Choice, BackendCache, hit.Model

		return
	}

	if panicked := safely(func() { answer, err = d.backend.Answer(ctx, q, state) }); panicked != nil {
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
		d.store(key, cacheEntry{Question: q.Name, Choice: answer.Key, Backend: name, Model: answer.Model})
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

// backendModel is the model a backend will ask, when it says. It is part
// of the cache key, so changing the model asks again.
func backendModel(b Backend) string {
	if m, ok := b.(interface{ Model() string }); ok {
		return m.Model()
	}

	return ""
}

func (q Question) has(key string) bool {
	return slices.ContainsFunc(q.Choices, func(c Choice) bool { return c.Key == key })
}

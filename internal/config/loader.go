package config

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/apple/pkl-go/pkl"

	"github.com/kyleking/wavez/internal/cycle"
	"github.com/kyleking/wavez/internal/router"
)

//go:embed pkl/Wavez.pkl
var schemaSource []byte

const (
	// FileName is the project config file's name, resolved relative to the
	// project root.
	FileName = ".wavez.pkl"
	// Files Wavez itself manages inside a project live under wavezDir,
	// including the vendored schema copy an amending .wavez.pkl points at.
	wavezDir       = ".wavez"
	schemaFileName = "Wavez.pkl"

	dirPerm  = 0o755
	filePerm = 0o644
)

// pklConfig is the wire shape EvaluateModule decodes ".wavez.pkl" into.
// Field names and defaults mirror pkl/Wavez.pkl exactly.
type pklConfig struct {
	Routines map[string]pklRoutine `pkl:"routines"`
	Checks   map[string]pklCheck   `pkl:"checks"`
	Vision   *pklTier              `pkl:"vision"`
	// A pointer because zero is a meaningful threshold, meaning every turn
	// overflows, and a plain float cannot tell it from an unset field.
	OverflowLoad     *float64   `pkl:"overflowLoadPerCore"`
	Fast             pklTier    `pkl:"fast"`
	Balanced         pklTier    `pkl:"balanced"`
	Deep             pklTier    `pkl:"deep"`
	HostedKeyCommand string     `pkl:"hostedKeyCommand"`
	WebSearchURL     string     `pkl:"webSearchURL"`
	Context          []string   `pkl:"context"`
	ExtraDirs        []string   `pkl:"extraDirs"`
	ShellAllow       []string   `pkl:"shellAllow"`
	AstGrepRules     []string   `pkl:"astGrepRules"`
	DeadcodeAllow    []string   `pkl:"deadcodeAllow"`
	Cycles           []pklCycle `pkl:"cycles"`
	Links            []pklLink  `pkl:"links"`
	PreToolUseHook   []string   `pkl:"preToolUseHook"`
	PostToolUseHook  []string   `pkl:"postToolUseHook"`
	AdmissionRoom    float64    `pkl:"admissionHeadroom"`
	ContextWindow    int        `pkl:"contextWindow"`
	DebounceMs       int        `pkl:"debounceMs"`
	FullRunCadence   int        `pkl:"fullRunCadence"`
	HookTimeoutMs    int        `pkl:"hookTimeoutMs"`
	LocalPort        int        `pkl:"localPort"`
	LocalStartSecs   int        `pkl:"localStartTimeoutSeconds"`
	LeaseTTLMinutes  int        `pkl:"leaseTtlMinutes"`
	Web              bool       `pkl:"web"`
}

// pklCheck mirrors the Check class in pkl/Wavez.pkl.
type pklCheck struct {
	Command string   `pkl:"command"`
	Paths   []string `pkl:"paths"`
}

// pklTier mirrors the Tier class in pkl/Wavez.pkl.
type pklTier struct {
	Overflow   *pklTier `pkl:"overflow"`
	Thinking   *bool    `pkl:"thinking"`
	Model      string   `pkl:"model"`
	BaseURL    string   `pkl:"baseURL"`
	KeyCommand string   `pkl:"keyCommand"`
}

// pklCycle and pklPhase mirror the Cycle and Phase classes in
// pkl/Wavez.pkl.
type pklCycle struct {
	Name   string     `pkl:"name"`
	Phases []pklPhase `pkl:"phases"`
}

type pklPhase struct {
	Name        string   `pkl:"name"`
	Goal        string   `pkl:"goal"`
	Exit        string   `pkl:"exit"`
	Tools       []string `pkl:"tools"`
	MaxAttempts int      `pkl:"maxAttempts"`
	Gated       bool     `pkl:"gated"`
}

// pklLink mirrors the LinkPattern class in pkl/Wavez.pkl.
type pklLink struct {
	Pattern string `pkl:"pattern"`
	URL     string `pkl:"url"`
}

// toLinkPatterns maps the evaluated pkl shape onto the config package's own.
func toLinkPatterns(in []pklLink) []LinkPattern {
	out := make([]LinkPattern, 0, len(in))
	for _, l := range in {
		out = append(out, LinkPattern(l))
	}

	return out
}

// toSpecs maps the evaluated pkl shape onto the cycle package's own.
func toSpecs(in []pklCycle) []cycle.Spec {
	out := make([]cycle.Spec, 0, len(in))

	for _, c := range in {
		spec := cycle.Spec{Name: c.Name}
		for _, p := range c.Phases {
			spec.Phases = append(spec.Phases, cycle.PhaseSpec{
				Name:        p.Name,
				Goal:        p.Goal,
				Exit:        p.Exit,
				Tools:       p.Tools,
				MaxAttempts: p.MaxAttempts,
				Gated:       p.Gated,
			})
		}

		out = append(out, spec)
	}

	return out
}

// Loader evaluates ".wavez.pkl" files through one long-lived pkl.Evaluator,
// the shape DESIGN.md's pkl decision measured: reused across reloads, a
// warm evaluation costs ~130 µs against ~10-14 ms to spawn a fresh one. A
// Loader is not safe for concurrent use.
type Loader struct {
	evaluator pkl.Evaluator
}

// NewLoader starts the evaluator's pkl server subprocess. Call Close when
// done with it.
func NewLoader(ctx context.Context) (*Loader, error) {
	evaluator, err := pkl.NewEvaluator(ctx, pkl.PreconfiguredOptions)
	if err != nil {
		return nil, fmt.Errorf("starting pkl evaluator: %w", err)
	}

	return &Loader{evaluator: evaluator}, nil
}

// Close releases the evaluator's pkl server subprocess. Safe to call once;
// a caller must not use Load after Close.
func (l *Loader) Close() error {
	if err := l.evaluator.Close(); err != nil {
		return fmt.Errorf("closing pkl evaluator: %w", err)
	}

	return nil
}

// Option configures a Load call.
type Option func(*loadOptions)

type loadOptions struct {
	with []string
}

// WithExtra appends paths to the loaded Config's Context, the "--with
// <file>" one-off override DESIGN.md's "Project instructions" section
// describes: it covers a single run without changing the persisted config.
func WithExtra(paths ...string) Option {
	return func(o *loadOptions) { o.with = append(o.with, paths...) }
}

// Load returns root's Config: Defaults(root) when no ".wavez.pkl" exists,
// or that file evaluated against the schema at pkl/Wavez.pkl otherwise.
// When no config file exists, Load also runs Discover and returns its
// result as inference so the caller can show it to the user; Load never
// folds an inference into the returned Config on its own.
func (l *Loader) Load(ctx context.Context, root string, opts ...Option) (Config, *Inference, error) {
	var lo loadOptions
	for _, opt := range opts {
		opt(&lo)
	}

	configPath := filepath.Join(root, FileName)

	cfg := Defaults(root)

	var inference *Inference

	switch _, err := os.Stat(configPath); {
	case err == nil:
		parsed, evalErr := l.evaluate(ctx, root, configPath)
		if evalErr != nil {
			return Config{}, nil, evalErr
		}

		cfg = parsed
	case errors.Is(err, os.ErrNotExist):
		if inf, ok := Discover(root); ok {
			inference = &inf
		}
	default:
		return Config{}, nil, fmt.Errorf("checking %s: %w", configPath, err)
	}

	cfg.Context = append(append([]string{}, cfg.Context...), lo.with...)

	return cfg, inference, nil
}

// evaluate materializes the embedded schema into root's .wavez/ directory
// (so a ".wavez.pkl" that amends ".wavez/Wavez.pkl" always resolves,
// regardless of where the wavez binary itself is installed) and evaluates
// configPath against the running evaluator.
func (l *Loader) evaluate(ctx context.Context, root, configPath string) (Config, error) {
	schemaDir := filepath.Join(root, wavezDir)
	if err := os.MkdirAll(schemaDir, dirPerm); err != nil {
		return Config{}, fmt.Errorf("creating %s: %w", schemaDir, err)
	}

	schemaPath := filepath.Join(schemaDir, schemaFileName)
	if err := os.WriteFile(schemaPath, schemaSource, filePerm); err != nil {
		return Config{}, fmt.Errorf("writing %s: %w", schemaPath, err)
	}

	var parsed pklConfig
	if err := l.evaluator.EvaluateModule(ctx, pkl.FileSource(configPath), &parsed); err != nil {
		return Config{}, fmt.Errorf("evaluating %s: %w", configPath, err)
	}

	return fromPkl(root, parsed), nil
}

// tierFromPkl overlays one tier's config on its default, so a project that
// names only a model keeps the default endpoint rather than blanking it.
func tierFromPkl(def Tier, p pklTier) Tier {
	if p.Model != "" {
		def.Model = p.Model
	}
	def.BaseURL = p.BaseURL
	def.KeyCommand = p.KeyCommand
	def.Thinking = p.Thinking
	def.Overflow = overflowFromPkl(p.Overflow)

	return def
}

// visionFromPkl reads the tier a turn carrying an image goes to. Like an
// overflow endpoint it has no default to overlay, because a project naming
// one names all of it.
func visionFromPkl(p *pklTier) *Tier {
	if p == nil || p.Model == "" {
		return nil
	}

	t := tierFromPkl(Tier{}, *p)

	return &t
}

// overflowFromPkl reads a tier's overflow endpoint, which has no default to
// overlay: a project that names one names all of it.
func overflowFromPkl(p *pklTier) *Tier {
	if p == nil {
		return nil
	}

	t := tierFromPkl(Tier{}, *p)

	return &t
}

func fromPkl(root string, p pklConfig) Config {
	cfg := Defaults(root)

	cfg.Tiers = router.Tiers[Tier]{
		Fast:     tierFromPkl(cfg.Tiers.Fast, p.Fast),
		Balanced: tierFromPkl(cfg.Tiers.Balanced, p.Balanced),
		Deep:     tierFromPkl(cfg.Tiers.Deep, p.Deep),
	}

	if p.HostedKeyCommand != "" {
		cfg.HostedKeyCommand = p.HostedKeyCommand
	}

	cfg.Web, cfg.WebSearchURL = p.Web, p.WebSearchURL

	if p.ContextWindow != 0 {
		cfg.ContextWindow = p.ContextWindow
	}

	if p.OverflowLoad != nil {
		cfg.OverflowLoadPerCore = *p.OverflowLoad
	}

	if p.AdmissionRoom > 0 {
		cfg.AdmissionHeadroom = p.AdmissionRoom
	}

	if p.LeaseTTLMinutes != 0 {
		cfg.LeaseTTL = time.Duration(p.LeaseTTLMinutes) * time.Minute
	}

	if p.DebounceMs != 0 {
		cfg.GateDebounce = time.Duration(p.DebounceMs) * time.Millisecond
	}

	if p.FullRunCadence != 0 {
		cfg.FullRunCadence = p.FullRunCadence
	}

	if p.HookTimeoutMs != 0 {
		cfg.HookTimeout = time.Duration(p.HookTimeoutMs) * time.Millisecond
	}

	if p.LocalPort != 0 {
		cfg.LocalPort = p.LocalPort
	}

	if p.LocalStartSecs != 0 {
		cfg.LocalStartTimeout = time.Duration(p.LocalStartSecs) * time.Second
	}

	cfg.Context = p.Context
	cfg.ExtraDirs = p.ExtraDirs
	cfg.ShellAllow = p.ShellAllow
	cfg.AstGrepRules = p.AstGrepRules
	cfg.Checks = projectChecks(p.Checks)
	cfg.Vision = visionFromPkl(p.Vision)
	cfg.DeadcodeAllow = p.DeadcodeAllow
	cfg.Cycles = toSpecs(p.Cycles)
	cfg.Links = toLinkPatterns(p.Links)
	cfg.PreToolUseHook = p.PreToolUseHook
	cfg.PostToolUseHook = p.PostToolUseHook
	cfg.Routines = routineDefinitions(p.Routines)

	return cfg
}

// projectChecks flattens the checks mapping, in name order so the gate list
// a run assembles does not depend on map iteration.
func projectChecks(in map[string]pklCheck) []ProjectCheck {
	out := make([]ProjectCheck, 0, len(in))
	for name, c := range in {
		out = append(out, ProjectCheck{Name: name, Command: c.Command, Paths: c.Paths})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out
}

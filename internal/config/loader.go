package config

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/apple/pkl-go/pkl"
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
	LocalModel       string   `pkl:"localModel"`
	HostedModel      string   `pkl:"hostedModel"`
	HostedKeyCommand string   `pkl:"hostedKeyCommand"`
	Context          []string `pkl:"context"`
	ExtraDirs        []string `pkl:"extraDirs"`
	AstGrepRules     []string `pkl:"astGrepRules"`
	DeadcodeAllow    []string `pkl:"deadcodeAllow"`
	PreToolUseHook   []string `pkl:"preToolUseHook"`
	PostToolUseHook  []string `pkl:"postToolUseHook"`
	ContextWindow    int      `pkl:"contextWindow"`
	DebounceMs       int      `pkl:"debounceMs"`
	FullRunCadence   int      `pkl:"fullRunCadence"`
	HookTimeoutMs    int      `pkl:"hookTimeoutMs"`
	LocalPort        int      `pkl:"localPort"`
	LocalStartSecs   int      `pkl:"localStartTimeoutSeconds"`
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

func fromPkl(root string, p pklConfig) Config {
	cfg := Defaults(root)

	if p.LocalModel != "" {
		cfg.LocalModel = p.LocalModel
	}

	if p.HostedKeyCommand != "" {
		cfg.HostedKeyCommand = p.HostedKeyCommand
	}
	if p.HostedModel != "" {
		cfg.HostedModel = p.HostedModel
	}

	if p.ContextWindow != 0 {
		cfg.ContextWindow = p.ContextWindow
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
	cfg.AstGrepRules = p.AstGrepRules
	cfg.DeadcodeAllow = p.DeadcodeAllow
	cfg.PreToolUseHook = p.PreToolUseHook
	cfg.PostToolUseHook = p.PostToolUseHook

	return cfg
}

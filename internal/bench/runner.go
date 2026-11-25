package bench

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/kyleking/wavez/internal/hook"
	"github.com/kyleking/wavez/internal/transcript"
)

// ContaminatedError says a baseline workspace already carries wavez, so a
// run in it would not be a baseline.
type ContaminatedError struct {
	Path string
}

func (e *ContaminatedError) Error() string {
	return "baseline workspace already has wavez wired in: " + e.Path
}

// Runner runs tasks under one harness and turns each run into a record.
type Runner struct {
	Harness Harness
	// Now is the clock, replaceable so a test's records are stable.
	Now func() time.Time
	// HarnessName is what records call the harness.
	HarnessName string
	// WavezBin is the absolute path of the wavez binary the wavez arm wires
	// in, and WavezVersion what that binary calls itself.
	WavezBin     string
	WavezVersion string
	// TranscriptRoot is the directory holding one folder of transcripts per
	// project. Empty skips the transcript lookup.
	TranscriptRoot string
	// TempDir is where workspaces are made, the system default when empty.
	TempDir string
	// CacheDir keeps a mirror of each repository a task names by URL, so
	// a run fetches only the commit it pins and only the first time. Empty
	// fetches into each workspace afresh.
	CacheDir string
	// Build identifies the wavez binary the run used and Flags the settings
	// of the invocation that change what a run does. Both go into every
	// record, and a report never pools runs that differ in either.
	Build string
	Flags string
	// Overlay is a directory whose files a wired arm's workspace gets before
	// wavez is wired in: what a project that uses wavez would keep beside its
	// code and the pinned tree lacks, such as a test trace. Empty adds none.
	Overlay string
	// Env is the environment the harness inherits.
	Env []string
	// Timeout bounds one harness run and CheckTimeout each check that runs
	// a program; zero is no bound.
	Timeout      time.Duration
	CheckTimeout time.Duration
	// Keep leaves the workspace behind for inspection.
	Keep bool
}

// Run executes one task under one arm and returns its record. An error
// means the run never reached the harness (no workspace, no wiring) and
// there is nothing to record; everything after that point, a harness that
// fails included, is a record.
func (r *Runner) Run(ctx context.Context, task *Task, arm Arm, model string) (Record, error) {
	ws, err := NewWorkspace(ctx, task.Repo, task.Rev, r.TempDir, r.CacheDir)
	if err != nil {
		return Record{}, err
	}

	rec, err := r.runIn(ctx, ws, task, arm, model)

	if r.Keep {
		rec.Workspace = ws.Dir

		return rec, err
	}

	// The caller's context may be the reason the run ended, and the
	// workspace still has to go.
	if rmErr := ws.Remove(context.WithoutCancel(ctx)); rmErr != nil {
		err = errors.Join(err, rmErr)
	}

	return rec, err
}

func (r *Runner) runIn(ctx context.Context, ws *Workspace, task *Task, arm Arm, model string) (Record, error) {
	spec, err := specOf(arm)
	if err != nil {
		return Record{}, err
	}

	env, err := r.prepare(ctx, ws.Dir, spec, task.Gates)
	if err != nil {
		return Record{}, err
	}

	// The hooks of a wired arm trace what they did into a file beside the
	// checkout, where it is no part of the tree the agent works on.
	trace := filepath.Join(filepath.Dir(ws.Dir), "hook-trace.jsonl")
	if spec.wired {
		env = append(slices.Clone(env), hook.TraceEnv+"="+trace)
	}

	env = append(slices.Clone(env), spec.env...)

	rec := Record{
		TaskID: task.ID, TaskHash: task.Hash(), Arm: arm, Harness: r.HarnessName,
		Model: model, Rev: task.Rev, WavezVersion: r.WavezVersion, Build: r.Build, Flags: r.Flags,
		ArmEnv: spec.env, ArmArgs: spec.args, ArmConfig: spec.configLines(),
		Started: r.now().UTC().Format(time.RFC3339),
	}

	session, err := newSessionID()
	if err != nil {
		return Record{}, err
	}

	start := time.Now()
	outcome, stop, runErr := r.harness(ctx, Request{
		Dir: ws.Dir, Prompt: task.Prompt, Model: model, Env: env, SessionID: session, Args: spec.args,
	})
	rec.WallMS = time.Since(start).Milliseconds()

	if err := ctx.Err(); err != nil {
		return Record{}, fmt.Errorf("run interrupted: %w", err)
	}

	rec.Stop, rec.Served, rec.SessionID = stop, outcome.Served, outcome.SessionID
	rec.Turns, rec.CostUSD, rec.Usage = outcome.Turns, outcome.CostUSD, outcome.Usage
	rec.HarnessMS = outcome.Duration.Milliseconds()

	if runErr != nil {
		rec.Error = firstLine(runErr.Error())
	}

	rec.Checks = Verify(ctx, task.Checks, ws.Dir, outcome.Answer, r.CheckTimeout)

	// A check cut short by the caller says nothing about the tree, so the
	// run is dropped the same way one cut short in the harness is.
	if err := ctx.Err(); err != nil {
		return Record{}, fmt.Errorf("run interrupted: %w", err)
	}

	rec.Pass = rec.Stop == StopCompleted && Passed(rec.Checks)
	r.attribute(&rec)
	tally(&rec, trace)

	return rec, nil
}

// tally reads the hook trace into the record. A run whose hooks never
// fired, the baseline's included, has no trace and counts nothing.
func tally(rec *Record, trace string) {
	body, err := fs.ReadFile(os.DirFS(filepath.Dir(trace)), filepath.Base(trace))
	if err != nil {
		return
	}

	counts, err := hook.ReadTally(bytes.NewReader(body))
	if err != nil && rec.Error == "" {
		rec.Error = firstLine(err.Error())
	}

	// A trace that could not be read to its end still counts the lines
	// before that point.
	rec.RouteHints, rec.RenameDenials = counts.RouteHints, counts.RenameDenials
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}

	return time.Now()
}

// harness runs the harness under the timeout and names how it stopped. A
// deadline wins over whatever the harness said, since a run cut off has no
// trustworthy report of its own.
func (r *Runner) harness(ctx context.Context, req Request) (Outcome, string, error) {
	if r.Timeout > 0 {
		var cancel context.CancelFunc

		ctx, cancel = context.WithTimeout(ctx, r.Timeout)
		defer cancel()
	}

	outcome, err := r.Harness.Run(ctx, req)

	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return outcome, StopTimeout, nil
	case err != nil:
		return outcome, StopHarnessError, fmt.Errorf("running the harness: %w", err)
	case outcome.Stop == "":
		return outcome, StopCompleted, nil
	default:
		return outcome, outcome.Stop, nil
	}
}

// prepare wires the arm into the workspace and returns the harness's
// environment. A wired arm gets hooks and a skill from `wavez init`, and
// the binary's directory first on PATH so the skill's commands resolve. An
// arm's settings and the task's gates are written into the workspace's
// .wavez.toml first.
func (r *Runner) prepare(ctx context.Context, dir string, spec *armSpec, gates string) ([]string, error) {
	if !spec.wired {
		return r.Env, ensureBaseline(dir)
	}

	if err := r.overlay(dir); err != nil {
		return nil, err
	}

	if err := applySettings(dir, spec.settings); err != nil {
		return nil, err
	}

	if err := appendGates(dir, gates); err != nil {
		return nil, err
	}

	out, err := run(ctx, invocation{
		Dir: dir, Name: r.WavezBin, Args: []string{"init", "--bin", r.WavezBin, "--dir", dir}, Env: r.Env,
	})
	if err != nil {
		return nil, fmt.Errorf("wiring wavez into the workspace: %w: %s", err, firstLine(string(out)))
	}

	return withPath(r.Env, filepath.Dir(r.WavezBin)), nil
}

// overlay copies the runner's overlay into the workspace. A file the pinned
// tree already has is an error, since the arm would then differ from the
// baseline in the project's own code.
func (r *Runner) overlay(dir string) error {
	if r.Overlay == "" {
		return nil
	}

	if err := os.CopyFS(dir, os.DirFS(r.Overlay)); err != nil {
		return fmt.Errorf("copying the overlay into the workspace: %w", err)
	}

	return nil
}

// The bits RFC 9562 fixes in a version 4 UUID.
const (
	uuidVersionMask = 0x0f
	uuidVersion4    = 0x40
	uuidVariantMask = 0x3f
	uuidVariantRFC  = 0x80
)

// newSessionID makes a random version 4 UUID. Naming the session up front
// means a run that is cut off, and so never prints its result, still has a
// transcript that can be found.
func newSessionID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generating a session id: %w", err)
	}

	b[6] = b[6]&uuidVersionMask | uuidVersion4
	b[8] = b[8]&uuidVariantMask | uuidVariantRFC

	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}

// withPath returns env with dir first on PATH.
func withPath(env []string, dir string) []string {
	const key = "PATH="

	out := make([]string, 0, len(env)+1)
	path := dir

	for _, kv := range env {
		if rest, ok := strings.CutPrefix(kv, key); ok {
			if rest != "" {
				path = dir + string(os.PathListSeparator) + rest
			}

			continue
		}

		out = append(out, kv)
	}

	return append(out, key+path)
}

// wiring matches the hook command `wavez init` writes into a settings
// file, with the binary bare or quoted, and quoted once more by JSON.
var wiring = regexp.MustCompile(`wavez\\?["']? (hook|gate)\b`)

// ensureBaseline refuses a workspace whose Claude Code settings or skills
// already mention wavez. The pinned tree may carry its own instructions
// files; both arms get those alike, so they are not looked at.
func ensureBaseline(dir string) error {
	for _, name := range []string{"settings.json", "settings.local.json"} {
		path := filepath.Join(dir, ".claude", name)

		body, err := os.ReadFile(path) //nolint:gosec // a fixed name under the workspace
		if errors.Is(err, os.ErrNotExist) {
			continue
		}

		if err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}

		if wiring.Match(body) {
			return &ContaminatedError{Path: path}
		}
	}

	skills, err := filepath.Glob(filepath.Join(dir, ".claude", "skills", "*wavez*"))
	if err != nil {
		return fmt.Errorf("searching for skills: %w", err)
	}

	if len(skills) > 0 {
		return &ContaminatedError{Path: skills[0]}
	}

	return nil
}

// attribute reads the session's transcript into the record. A transcript
// that cannot be found is noted and not fatal: the run's cost is already
// known from the harness.
func (r *Runner) attribute(rec *Record) {
	if r.TranscriptRoot == "" || rec.SessionID == "" {
		return
	}

	path, err := transcript.Find(r.TranscriptRoot, rec.SessionID)
	if err == nil {
		var stats transcript.Stats

		if stats, err = transcript.ReadSession(path); err == nil {
			rec.Transcript = &stats

			return
		}
	}

	if rec.Error == "" {
		rec.Error = firstLine(err.Error())
	}
}

// BinaryVersion asks a wavez binary what it calls itself, so a record made
// with a binary other than the one running names the right build.
func BinaryVersion(ctx context.Context, bin string) string {
	out, err := output(ctx, invocation{Name: bin, Args: []string{"version"}})
	if err != nil || firstLine(string(out)) == "" {
		return "unknown"
	}

	return strings.TrimPrefix(firstLine(string(out)), "wavez ")
}

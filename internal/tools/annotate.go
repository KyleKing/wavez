package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/kyleking/wavez/internal/tool"
)

// annotatePerm is what the copy handed to the user is written with. It is
// theirs to edit, and nobody else's to read.
const annotatePerm = 0o600

var annotateSchema = buildSchema(map[string]schemaProperty{
	propPath: {
		Type:        schemaTypeString,
		Description: "Path to an image file in the project, relative to its root.",
	},
	propQuestion: {
		Type: schemaTypeString,
		Description: "What you want marked, in the words the user will read: name the thing " +
			"to circle, point at, or cross out, and say what you will do with it.",
	},
}, propPath, propQuestion)

// Annotate hands an image to the user to mark up, waits, and then reads what
// they drew.
//
// It is `question` for pictures. A layout that is wrong is faster to point at
// than to describe, and the two ways to get that pointing into a run are
// coordinates the user reads off by hand or a mark they draw where they mean
// it. This is the second: the image opens in whatever the platform shows
// images with, the run blocks on the same pending prompt a question uses, and
// what comes back is the vision tier's reading of the saved file beside the
// user's own words.
//
// The copy is what the user edits, so the project's image is never modified
// by answering.
type Annotate struct {
	look    *Look
	asker   Asker
	show    func(ctx context.Context, path string)
	workDir string
}

// AnnotateOption configures an Annotate.
type AnnotateOption func(*Annotate)

// WithViewer replaces what shows the copy to the user. A machine with no
// display, and a test, pass one that shows nothing: the prompt names the
// path either way, so the flow still works with no window in it.
func WithViewer(show func(ctx context.Context, path string)) AnnotateOption {
	return func(a *Annotate) { a.show = show }
}

// NewAnnotate builds an Annotate that writes the copy it hands over into
// workDir, which must sit inside the project root so the run can read the
// result back.
func NewAnnotate(look *Look, asker Asker, workDir string, opts ...AnnotateOption) *Annotate {
	a := &Annotate{look: look, asker: asker, workDir: workDir, show: showFile}
	for _, opt := range opts {
		opt(a)
	}

	return a
}

// Name implements tool.Tool.
func (*Annotate) Name() string { return "annotate" }

// Description implements tool.Tool.
func (*Annotate) Description() string {
	return "Ask the user to mark up an image and read what they drew. Use it when pointing " +
		"is clearer than describing: which element is misaligned, which of several things " +
		"on screen you mean, where a change should land. A copy opens on their machine, the " +
		"run waits while they draw on it and save, and the answer is what the marks show " +
		"plus whatever they typed. Do not use it to ask a question with no picture in it."
}

// Schema implements tool.Tool.
func (*Annotate) Schema() json.RawMessage { return annotateSchema }

// Risk implements tool.Tool. Annotating waits on the user to draw and save.
func (*Annotate) Risk() tool.RiskClass { return tool.RiskExternal }

type annotateInput struct {
	Path     string `json:"path"`
	Question string `json:"question"`
}

// Run implements tool.Tool.
func (a *Annotate) Run(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	if err := ctx.Err(); err != nil {
		return tool.Result{}, fmt.Errorf("annotate: %w", err)
	}

	var in annotateInput
	if err := decodeInput(input, &in); err != nil {
		return tool.Fail(tool.CauseMalformed, "invalid input: %v", err), nil
	}

	if strings.TrimSpace(in.Question) == "" {
		return tool.Fail(tool.CauseBadInput,
			"question is required: the user is being asked to draw something, and a call "+
				"without one does not say what"), nil
	}

	data, media, failure := a.look.image(in.Path)
	if failure != nil {
		return *failure, nil
	}

	copyPath, err := a.handOver(ctx, in.Path, data)
	if err != nil {
		return tool.Fail(tool.CauseIO, "%v", err), nil
	}

	note, err := a.asker.Ask(ctx, annotatePrompt(copyPath, in.Question))
	if err != nil {
		return tool.Fail(tool.CauseUpstream, "waiting for the annotation: %v", err), nil
	}

	marked, err := os.ReadFile(copyPath) //nolint:gosec // handOver wrote this path under workDir
	if err != nil {
		return tool.Fail(tool.CauseIO, "reading the annotated copy: %v", err), nil
	}

	return a.answer(ctx, in.Question, media, marked, note, bytes.Equal(marked, data)), nil
}

// answer reads the saved copy and reports it beside the user's own words. An
// unchanged file is said so rather than described as if it carried marks,
// because a user who answered without drawing has still answered.
func (a *Annotate) answer(
	ctx context.Context, question, media string, marked []byte, note string, unchanged bool,
) tool.Result {
	var b strings.Builder

	if note = strings.TrimSpace(note); note != "" {
		b.WriteString("the user says: " + note + "\n\n")
	}

	if unchanged {
		b.WriteString("the image came back byte for byte as it went out, so it carries no marks")

		return tool.Result{Content: b.String()}
	}

	seen, err := a.look.ask(ctx, annotateReadPrompt(question), media, marked)
	if err != nil {
		fmt.Fprintf(&b, "the marked image could not be read: %v", err)

		return tool.Result{Content: b.String()}
	}

	b.WriteString(seen)

	return tool.Result{Content: b.String()}
}

// handOver writes the copy the user edits and opens it for them. Opening is
// best effort: the prompt names the path, so a machine with no opener costs
// the user one step rather than the call.
func (a *Annotate) handOver(ctx context.Context, path string, data []byte) (string, error) {
	if err := os.MkdirAll(a.workDir, overflowDirPerm); err != nil {
		return "", fmt.Errorf("preparing a copy to annotate: %w", err)
	}

	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)) +
		"-annotate" + filepath.Ext(path)
	copyPath := filepath.Join(a.workDir, name)

	if err := os.WriteFile(copyPath, data, annotatePerm); err != nil {
		return "", fmt.Errorf("writing a copy to annotate: %w", err)
	}

	a.show(ctx, copyPath)

	return copyPath, nil
}

// showFile shows the file in whatever the platform views images with. The
// argument is a path this tool just wrote under its own directory, with an
// image extension the look tool accepts, and it is passed as an argument
// rather than through a shell.
//
// The launch is detached from the turn's context on purpose: the viewer is
// what the user is about to draw in, and it must not be killed by the wait
// that follows ending.
func showFile(ctx context.Context, path string) {
	var argv []string

	switch runtime.GOOS {
	case "darwin":
		argv = []string{"open", path}
	case "linux":
		argv = []string{"xdg-open", path}
	default:
		return
	}

	//nolint:gosec,errcheck // fixed argv, and a machine with no opener still gets the path in the prompt
	_ = exec.CommandContext(context.WithoutCancel(ctx), argv[0], argv[1:]...).Start()
}

func annotatePrompt(copyPath, question string) string {
	return fmt.Sprintf("Mark up %s (it should have opened) and save it, then answer: %s",
		copyPath, question)
}

func annotateReadPrompt(question string) string {
	return "This image has been marked up by hand. Describe the marks and what they point " +
		"at, in the terms of this question: " + question
}

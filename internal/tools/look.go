package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kyleking/wavez/internal/llm"
	"github.com/kyleking/wavez/internal/tool"
)

// maxLookBytes bounds the image one call sends. A screenshot of a laptop
// display is well under this; anything past it is a photograph or a mistake,
// and either way it is worth saying so rather than spending the turn.
const maxLookBytes = 4 << 20

// lookMedia is what this tool will look at, by extension, mapped to the media
// type the request declares.
var lookMedia = map[string]string{
	".gif":  "image/gif",
	".jpeg": "image/jpeg",
	".jpg":  "image/jpeg",
	".png":  "image/png",
	".webp": "image/webp",
}

var lookSchema = buildSchema(map[string]schemaProperty{
	propPath: {
		Type:        schemaTypeString,
		Description: "Path to an image file in the project, relative to its root.",
	},
	propQuestion: {
		Type: schemaTypeString,
		Description: "What to find out about the image. Ask for the one thing you need, " +
			"since the answer comes back as text and the image itself is not kept.",
	},
}, propPath, propQuestion)

// Look answers a question about an image by asking a model that can see it.
//
// The answer comes back as text and the image is never added to this thread's
// history. That is the whole shape: an image costs hundreds of times a line
// of text and would be re-sent on every later turn, so a visual judgment is
// asked once, answered in words, and the words are what the run keeps.
//
// It exists only where a project configured a vision tier, because the tiers
// that do the work cannot see: `glm-5.3` refuses an image outright rather
// than ignoring it.
type Look struct {
	provider llm.Provider
	scope    *Scope
	root     string
	model    string
	deps     deps
}

// NewLook builds a Look that asks provider, which must be a model that
// accepts image content.
func NewLook(root string, scope *Scope, provider llm.Provider, model string, opts ...Option) *Look {
	return &Look{root: root, scope: scope, provider: provider, model: model, deps: newDeps(opts)}
}

// Name implements tool.Tool.
func (*Look) Name() string { return "look" }

// Description implements tool.Tool.
func (*Look) Description() string {
	return "Look at an image file and answer one question about it. Use it for what only " +
		"an image can settle: whether a rendered layout is right, what a screen is showing, " +
		"where something sits relative to something else. The answer is text and the image " +
		"is not kept, so ask for everything you need in one call. Do not trust it for exact " +
		"small text, which it misreads: read the source instead when the wording matters."
}

// Schema implements tool.Tool.
func (*Look) Schema() json.RawMessage { return lookSchema }

type lookInput struct {
	Path     string `json:"path"`
	Question string `json:"question"`
}

// Run implements tool.Tool.
func (l *Look) Run(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	if err := ctx.Err(); err != nil {
		return tool.Result{}, fmt.Errorf("look: %w", err)
	}

	var in lookInput
	if err := decodeInput(input, &in); err != nil {
		return tool.Fail(tool.CauseMalformed, "invalid input: %v", err), nil
	}

	if strings.TrimSpace(in.Question) == "" {
		return tool.Fail(tool.CauseBadInput,
			"question is required: this tool answers one question about the image and keeps "+
				"nothing else, so a call without one has no answer to give"), nil
	}

	data, media, failure := l.image(in.Path)
	if failure != nil {
		return *failure, nil
	}

	answer, err := l.ask(ctx, in.Question, media, data)
	if err != nil {
		return tool.Fail(tool.CauseUpstream, "looking at %s: %v", in.Path, err), nil
	}

	if strings.TrimSpace(answer) == "" {
		return tool.Fail(tool.CauseUpstream,
			"%s answered nothing about %s", l.model, in.Path), nil
	}

	return tool.Result{Content: answer}, nil
}

// image reads the file and reports why it cannot be looked at, if it cannot.
func (l *Look) image(path string) ([]byte, string, *tool.Result) {
	abs, err := resolvePath(l.root, l.deps.extraRoots, path)
	if err != nil {
		return nil, "", failure(tool.CauseBadInput, "%v", err)
	}

	media, ok := lookMedia[strings.ToLower(filepath.Ext(abs))]
	if !ok {
		return nil, "", failure(tool.CauseBadInput,
			"%s is not an image this tool reads (%s). Use read for text",
			path, strings.Join(sortedExtensions(), ", "))
	}

	info, err := os.Stat(abs)
	if err != nil {
		return nil, "", failure(tool.CauseIO, "%v", err)
	}

	if info.Size() > maxLookBytes {
		return nil, "", failure(tool.CauseBadInput,
			"%s is %d bytes, over the %d this tool sends", path, info.Size(), maxLookBytes)
	}

	data, err := os.ReadFile(abs) //nolint:gosec // resolvePath has already confined abs to the project root
	if err != nil {
		return nil, "", failure(tool.CauseIO, "%v", err)
	}

	if l.scope != nil {
		l.scope.Observe(abs)
	}

	return data, media, nil
}

// ask sends the one turn this tool takes and returns what came back.
func (l *Look) ask(ctx context.Context, question, media string, data []byte) (string, error) {
	var b strings.Builder

	for chunk, err := range l.provider.Stream(ctx, llm.Request{
		Model: l.model,
		Messages: []llm.Message{{Role: llm.RoleUser, Parts: []llm.Part{
			{Kind: llm.PartText, Text: question},
			{Kind: llm.PartImage, Media: media, Data: data},
		}}},
	}) {
		if err != nil {
			return "", err
		}

		b.WriteString(chunk.Text)
	}

	return strings.TrimSpace(b.String()), nil
}

func failure(cause tool.Cause, format string, args ...any) *tool.Result {
	r := tool.Fail(cause, format, args...)

	return &r
}

func sortedExtensions() []string {
	out := make([]string, 0, len(lookMedia))
	for ext := range lookMedia {
		out = append(out, ext)
	}

	slices.Sort(out)

	return out
}

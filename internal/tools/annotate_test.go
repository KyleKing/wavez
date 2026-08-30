package tools_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/llm/fake"
	"github.com/kyleking/wavez/internal/tools"
)

// noViewer keeps the tests from opening a window on the machine running
// them, which is the one part of this flow that is not the harness's.
func noViewer() tools.AnnotateOption {
	return tools.WithViewer(func(context.Context, string) {})
}

// markingAsker is the person: it draws on the copy the tool handed over and
// then answers, which is the order the real flow happens in.
type markingAsker struct {
	prompt string
	draw   []byte
}

func (m *markingAsker) Ask(_ context.Context, question string) (string, error) {
	m.prompt = question

	path := strings.TrimSuffix(strings.TrimPrefix(question, "Mark up "), "")
	path, _, _ = strings.Cut(path, " (it should have opened)")

	if m.draw != nil {
		if err := os.WriteFile(path, m.draw, 0o600); err != nil {
			return "", err //nolint:wrapcheck // the test wants the raw failure
		}
	}

	return "circled the header", nil
}

// The whole point is that what the user drew reaches the run, so the copy
// they were handed has to be the file the vision tier is then asked about.
func TestAnnotate_ReadsWhatTheUserDrew(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	original := []byte{0x89, 'P', 'N', 'G', 1, 2, 3}
	if err := os.WriteFile(filepath.Join(root, "shot.png"), original, 0o600); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}

	work := filepath.Join(root, ".wavez", "session")
	provider := fake.New("vision", fake.Turn{Text: []string{"A circle around the header."}})
	marked := append(slices.Clone(original), 'm', 'a', 'r', 'k')
	asker := &markingAsker{draw: marked}

	an := tools.NewAnnotate(
		tools.NewLook(root, tools.NewScope(false), provider, "glm-4.6v"), asker, work, noViewer())

	result, err := an.Run(t.Context(), mustJSON(t, map[string]any{
		"path": "shot.png", "question": "which element is misaligned",
	}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.IsError {
		t.Fatalf("Result = %+v, want an answer", result)
	}

	for _, want := range []string{"circled the header", "A circle around the header."} {
		if !strings.Contains(result.Content, want) {
			t.Errorf("Content = %q, want it to carry %q", result.Content, want)
		}
	}

	if !strings.Contains(asker.prompt, "which element is misaligned") {
		t.Errorf("prompt = %q, want it to carry the question", asker.prompt)
	}

	// The project's own image is never edited by answering.
	kept, err := os.ReadFile(filepath.Join(root, "shot.png")) //nolint:gosec // a path this test wrote
	if err != nil || !bytes.Equal(kept, original) {
		t.Errorf("the original changed: %q (%v)", kept, err)
	}

	requests := provider.Requests()
	if len(requests) != 1 {
		t.Fatalf("sent %d requests, want one", len(requests))
	}

	sent := requests[0].Messages[0].Parts[1].Data
	if !bytes.Equal(sent, asker.draw) {
		t.Errorf("sent %q, want the marked copy", sent)
	}
}

// A user who answered without drawing has still answered, so the run is told
// what happened instead of being handed a reading of the unmarked image.
func TestAnnotate_SaysWhenNothingWasDrawn(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "shot.png"), []byte{0x89, 'P', 'N', 'G'}, 0o600); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}

	provider := fake.New("vision", fake.Turn{Text: []string{"should not be asked"}})
	an := tools.NewAnnotate(
		tools.NewLook(root, tools.NewScope(false), provider, "glm-4.6v"),
		&markingAsker{}, filepath.Join(root, ".wavez", "session"), noViewer())

	result, err := an.Run(t.Context(), mustJSON(t, map[string]any{
		"path": "shot.png", "question": "where",
	}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !strings.Contains(result.Content, "carries no marks") {
		t.Errorf("Content = %q, want it to say the image came back unmarked", result.Content)
	}

	if len(provider.Requests()) != 0 {
		t.Error("the vision tier was asked about an image nobody drew on")
	}
}

// A call with no picture in it is refused before anybody is interrupted.
func TestAnnotate_RefusesWhatItCannotHandOver(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "notes.md"), []byte("# hi"), 0o600); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}

	asker := &markingAsker{}
	an := tools.NewAnnotate(
		tools.NewLook(root, tools.NewScope(false), fake.New("vision"), "glm-4.6v"),
		asker, filepath.Join(root, ".wavez", "session"), noViewer())

	for _, input := range []map[string]any{
		{"path": "notes.md", "question": "where"},
		{"path": "shot.png", "question": ""},
	} {
		result, err := an.Run(t.Context(), mustJSON(t, input))
		if err != nil {
			t.Fatalf("Run(%v): %v", input, err)
		}
		if !result.IsError {
			t.Errorf("Run(%v) = %q, want a refusal", input, result.Content)
		}
	}

	if asker.prompt != "" {
		t.Errorf("the user was interrupted for a call that could not work: %q", asker.prompt)
	}
}

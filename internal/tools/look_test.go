package tools_test

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/llm"
	"github.com/kyleking/wavez/internal/llm/fake"
	"github.com/kyleking/wavez/internal/tool"
	"github.com/kyleking/wavez/internal/tools"
)

// A visual judgment is asked once and answered in words. The image must not
// reach the thread's history, because it costs hundreds of times a line of
// text and every later turn would carry it again.
func TestLook_AnswersInTextAndSendsTheImageOnce(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	png := []byte{0x89, 'P', 'N', 'G', 1, 2, 3}
	if err := os.WriteFile(filepath.Join(root, "shot.png"), png, 0o600); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}

	provider := fake.New("vision", fake.Turn{Text: []string{"A red ", "square."}})
	look := tools.NewLook(root, tools.NewScope(false), provider, "glm-4.6v")

	result, err := look.Run(t.Context(), mustJSON(t, map[string]any{
		"path": "shot.png", "question": "what is this",
	}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.IsError || result.Content != "A red square." {
		t.Fatalf("Result = %+v, want the model's text", result)
	}

	requests := provider.Requests()
	if len(requests) != 1 {
		t.Fatalf("sent %d requests, want one", len(requests))
	}

	parts := requests[0].Messages[0].Parts
	if len(parts) != 2 || parts[0].Kind != llm.PartText || parts[1].Kind != llm.PartImage {
		t.Fatalf("Parts = %+v, want the question then the image", parts)
	}

	if base64.StdEncoding.EncodeToString(parts[1].Data) != base64.StdEncoding.EncodeToString(png) {
		t.Errorf("the image sent was not the file's bytes")
	}

	if parts[1].Media != "image/png" {
		t.Errorf("Media = %q, want image/png", parts[1].Media)
	}
}

// What it refuses, it refuses before spending a request.
func TestLook_RefusesWhatItCannotLookAt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	for name, body := range map[string]string{"notes.md": "hello", "shot.png": "x"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}

	tests := map[string]struct {
		input map[string]any
		want  string
		cause tool.Cause
	}{
		"a text file": {
			input: map[string]any{"path": "notes.md", "question": "what is this"},
			want:  "not an image",
			cause: tool.CauseBadInput,
		},
		"no question": {
			input: map[string]any{"path": "shot.png"},
			want:  "question is required",
			cause: tool.CauseBadInput,
		},
		"outside the project": {
			input: map[string]any{"path": "../secret.png", "question": "what is this"},
			want:  "outside",
			cause: tool.CauseBadInput,
		},
		"a file that is not there": {
			input: map[string]any{"path": "missing.png", "question": "what is this"},
			want:  "no such file",
			cause: tool.CauseIO,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			provider := fake.New("vision")
			look := tools.NewLook(root, tools.NewScope(false), provider, "glm-4.6v")

			result, err := look.Run(t.Context(), mustJSON(t, tt.input))
			if err != nil {
				t.Fatalf("Run: %v", err)
			}

			if !result.IsError || result.Cause != tt.cause {
				t.Fatalf("Result = %+v, want an error with cause %s", result, tt.cause)
			}

			if !strings.Contains(strings.ToLower(result.Content), tt.want) {
				t.Errorf("Content = %q, want it to mention %q", result.Content, tt.want)
			}

			if len(provider.Requests()) != 0 {
				t.Errorf("spent a request on a call it could refuse from the file alone")
			}
		})
	}
}

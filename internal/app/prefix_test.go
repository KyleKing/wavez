package app_test

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/kyleking/wavez/internal/app"
	"github.com/kyleking/wavez/internal/config"
	"github.com/kyleking/wavez/internal/llm"
	"github.com/kyleking/wavez/internal/llm/fake"
	"github.com/kyleking/wavez/internal/permission"
)

const agentsMD = `# AGENTS

Top-level instructions.

## Architecture

The store owns SQLite. Gates trigger on change events.

## Testing

Table-driven tests everywhere.
`

func TestBuildPrefix(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "AGENTS.md"), agentsMD)
	writeFile(t, filepath.Join(root, "NOTES.md"), "just notes")

	tests := []struct {
		name    string
		want    string
		entries []string
		wantErr bool
	}{
		{
			name:    "whole file",
			entries: []string{"NOTES.md"},
			want:    "just notes",
		},
		{
			name:    "headed section stops before the next heading",
			entries: []string{"AGENTS.md#Architecture"},
			want:    "The store owns SQLite. Gates trigger on change events.",
		},
		{
			name:    "multiple entries join with a blank line",
			entries: []string{"NOTES.md", "AGENTS.md#Testing"},
			want:    "just notes\n\nTable-driven tests everywhere.",
		},
		{
			name:    "missing heading errors",
			entries: []string{"AGENTS.md#NoSuchHeading"},
			wantErr: true,
		},
		{
			name:    "missing file errors",
			entries: []string{"MISSING.md"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := app.BuildPrefix(root, tt.entries)
			if (err != nil) != tt.wantErr {
				t.Fatalf("BuildPrefix() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if got != tt.want {
				t.Errorf("BuildPrefix() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestBuildPrefix_UnlistedFileNeverEntersPrefix is the anti-auto-load
// property DESIGN.md's "Project instructions" section requires: an
// AGENTS.md present on disk but absent from the context list must not
// enter the prefix, even though the tool set could otherwise read it freely.
func TestBuildPrefix_UnlistedFileNeverEntersPrefix(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "AGENTS.md"), agentsMD)
	writeFile(t, filepath.Join(root, "CLAUDE.md"), "claude-only instructions")

	got, err := app.BuildPrefix(root, nil)
	if err != nil {
		t.Fatalf("BuildPrefix() error = %v", err)
	}
	if got != "" {
		t.Fatalf("BuildPrefix() = %q, want empty for an empty context list", got)
	}

	writeFile(t, filepath.Join(root, "OTHER.md"), "explicitly listed")

	got, err = app.BuildPrefix(root, []string{"OTHER.md"})
	if err != nil {
		t.Fatalf("BuildPrefix() error = %v", err)
	}
	if got != "explicitly listed" {
		t.Fatalf("BuildPrefix() = %q, want only the listed file's content", got)
	}
	if strings.Contains(got, "AGENTS") || strings.Contains(got, "CLAUDE") {
		t.Fatalf("BuildPrefix() = %q, must not contain unlisted AGENTS.md or CLAUDE.md content", got)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

func TestSystemPrefixAlwaysCarriesTheBaseInstructions(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("# Arch\n\nproject text\n"), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	a, err := app.New(t.Context(), root, config.Defaults(root), permission.DenyAll(),
		app.WithProviders(fake.New("local"), fake.New("hosted")))
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	t.Cleanup(func() {
		if cerr := a.Close(); cerr != nil {
			t.Errorf("close: %v", cerr)
		}
	})

	if !strings.Contains(a.SystemPrefix, "Formatting and imports are fixed automatically") {
		t.Fatal("base system instructions missing from the prefix")
	}
	if strings.Contains(a.SystemPrefix, "project text") {
		t.Fatal("an unlisted file entered the prefix")
	}
}

func TestHostedKeyComesFromTheConfiguredCommand(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg := config.Defaults(root)
	cfg.HostedKeyCommand = "printf sk-from-command"

	a, err := app.New(t.Context(), root, cfg, permission.DenyAll())
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	if cerr := a.Close(); cerr != nil {
		t.Errorf("close: %v", cerr)
	}
}

// A local-only run must start without a hosted credential it never uses, so
// the key resolves on the first hosted request rather than at construction.
func TestHostedKeyFailureDoesNotBlockAppConstruction(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg := config.Defaults(root)
	cfg.HostedKeyCommand = "false"

	a, err := app.New(t.Context(), root, cfg, permission.DenyAll())
	if err != nil {
		t.Fatalf("a failing key command blocked a local-only run: %v", err)
	}
	if cerr := a.Close(); cerr != nil {
		t.Errorf("close: %v", cerr)
	}
}

func TestHostedKeyErrorsOnFirstHostedRequest(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg := config.Defaults(root)
	cfg.HostedKeyCommand = "true" // succeeds, prints nothing

	a, err := app.New(t.Context(), root, cfg, permission.DenyAll())
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	t.Cleanup(func() {
		if cerr := a.Close(); cerr != nil {
			t.Errorf("close: %v", cerr)
		}
	})

	var streamErr error
	for _, err := range a.Hosted.Stream(t.Context(), llm.Request{Model: "m"}) {
		if err != nil {
			streamErr = err

			break
		}
	}
	if !errors.Is(streamErr, app.ErrEmptyKeyCommand) {
		t.Fatalf("stream error = %v, want ErrEmptyKeyCommand", streamErr)
	}
}

// A remote local tier dials the configured endpoint with its key and never
// starts a llama-server here, even when the App was asked to manage one.
func TestRemoteLocalTierDialsTheEndpointWithItsKey(t *testing.T) {
	t.Parallel()

	var (
		mu   sync.Mutex
		auth string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		auth = r.Header.Get("Authorization")
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if _, err := io.WriteString(w, "data: [DONE]\n\n"); err != nil {
			t.Logf("writing SSE body: %v", err)
		}
	}))
	t.Cleanup(srv.Close)

	root := t.TempDir()
	cfg := config.Defaults(root)
	cfg.LocalBaseURL = srv.URL
	cfg.LocalKeyCommand = "printf tok-from-command"

	a, err := app.New(t.Context(), root, cfg, permission.DenyAll(), app.WithManagedLocalServer())
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	t.Cleanup(func() {
		if cerr := a.Close(); cerr != nil {
			t.Errorf("close: %v", cerr)
		}
	})

	for _, err := range a.Local.Stream(t.Context(), llm.Request{Model: "m"}) {
		if err != nil {
			t.Fatalf("stream: %v", err)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if auth != "Bearer tok-from-command" {
		t.Fatalf("Authorization = %q, want the key command's output as a bearer token", auth)
	}
}

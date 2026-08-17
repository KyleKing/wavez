package lsp_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kyleking/wavez/internal/lsp"
	"github.com/kyleking/wavez/internal/lsp/lsptest"
)

func TestMain(m *testing.M) {
	lsptest.ServeIfChild()
	os.Exit(m.Run())
}

const waitBudget = 10 * time.Second

// writeModule lays down a one-file project the scripted server can be pointed
// at. Nothing compiles it: the server is scripted, not real.
func writeModule(t *testing.T, files map[string]string) string {
	t.Helper()

	root := t.TempDir()

	for name, body := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}

	return root
}

func newPool(t *testing.T, root string, script lsptest.Script) *lsp.Pool {
	t.Helper()

	pool := lsp.NewPool(root, lsptest.Server(t, script))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), waitBudget)
		defer cancel()

		if err := pool.Close(ctx); err != nil {
			t.Errorf("closing pool: %v", err)
		}
	})

	return pool
}

func TestClientDiagnostics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		mode       lsptest.Mode
		diagnostic []lsptest.Diagnostic
		want       []lsp.Diagnostic
	}{
		{
			name: "error is reported with a one-based position",
			diagnostic: []lsptest.Diagnostic{
				{Message: "undefined: Foo", Severity: 1, Line: 7, Source: "compiler"},
			},
			want: []lsp.Diagnostic{{
				Path: "main.go", Message: "undefined: Foo", Source: "compiler",
				Severity: lsp.SeverityError, Line: 7, Character: 1,
			}},
		},
		{
			name:       "clean file publishes an empty list",
			diagnostic: nil,
			want:       []lsp.Diagnostic{},
		},
		{
			name: "a publication without a version still satisfies the wait",
			mode: lsptest.ModeOmitVersion,
			diagnostic: []lsptest.Diagnostic{
				{Message: "unused variable", Severity: 2, Line: 1, Source: "staticcheck"},
			},
			want: []lsp.Diagnostic{{
				Path: "main.go", Message: "unused variable", Source: "staticcheck",
				Severity: lsp.SeverityWarning, Line: 1, Character: 1,
			}},
		},
		{
			name: "a diagnostic with no severity counts as an error",
			diagnostic: []lsptest.Diagnostic{
				{Message: "something is wrong", Line: 2},
			},
			want: []lsp.Diagnostic{
				{Path: "main.go", Message: "something is wrong", Severity: lsp.SeverityError, Line: 2, Character: 1},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root := writeModule(t, map[string]string{"main.go": "package main\n"})
			pool := newPool(t, root, lsptest.Script{
				Mode:        tc.mode,
				Diagnostics: map[string][]lsptest.Diagnostic{"main.go": tc.diagnostic},
			})

			assertDiagnostics(t, pool, tc.want)
		})
	}
}

func assertDiagnostics(t *testing.T, pool *lsp.Pool, want []lsp.Diagnostic) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), waitBudget)
	defer cancel()

	client, err := pool.Client(ctx, "main.go")
	if err != nil {
		t.Fatalf("starting server: %v", err)
	}

	version, err := client.Sync(ctx, "main.go")
	if err != nil {
		t.Fatalf("syncing: %v", err)
	}

	if version != 1 {
		t.Errorf("first sync version = %d, want 1", version)
	}

	got, err := client.Diagnostics(ctx, "main.go", version)
	if err != nil {
		t.Fatalf("waiting for diagnostics: %v", err)
	}

	if len(got) != len(want) {
		t.Fatalf("got %d diagnostics, want %d: %v", len(got), len(want), got)
	}

	for i, w := range want {
		if got[i] != w {
			t.Errorf("diagnostic %d = %+v, want %+v", i, got[i], w)
		}
	}
}

func TestClientSyncSendsAChangeOnTheSecondCall(t *testing.T) {
	t.Parallel()

	root := writeModule(t, map[string]string{"main.go": "package main\n"})
	pool := newPool(t, root, lsptest.Script{
		Diagnostics: map[string][]lsptest.Diagnostic{
			"main.go": {{Message: "boom", Severity: 1, Line: 1}},
		},
	})

	ctx, cancel := context.WithTimeout(t.Context(), waitBudget)
	defer cancel()

	client, err := pool.Client(ctx, "main.go")
	if err != nil {
		t.Fatalf("starting server: %v", err)
	}

	for want := 1; want <= 3; want++ {
		version, err := client.Sync(ctx, "main.go")
		if err != nil {
			t.Fatalf("sync %d: %v", want, err)
		}

		if version != want {
			t.Fatalf("sync %d returned version %d", want, version)
		}

		// The server echoes the version it was sent, so a wait that resolves
		// here proves the change reached it rather than the didOpen.
		if _, err := client.Diagnostics(ctx, "main.go", version); err != nil {
			t.Fatalf("diagnostics at version %d: %v", version, err)
		}
	}
}

func TestClientDiagnosticsHonorsTheCallersDeadline(t *testing.T) {
	t.Parallel()

	root := writeModule(t, map[string]string{"main.go": "package main\n"})
	pool := newPool(t, root, lsptest.Script{Mode: lsptest.ModeSilent})

	ctx, cancel := context.WithTimeout(t.Context(), waitBudget)
	defer cancel()

	client, err := pool.Client(ctx, "main.go")
	if err != nil {
		t.Fatalf("starting server: %v", err)
	}

	version, err := client.Sync(ctx, "main.go")
	if err != nil {
		t.Fatalf("syncing: %v", err)
	}

	waitCtx, waitCancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer waitCancel()

	if _, err := client.Diagnostics(waitCtx, "main.go", version); err == nil {
		t.Fatal("a server that publishes nothing must not report a clean file")
	}
}

func TestPoolStartsOneProcessPerLanguageAndReusesIt(t *testing.T) {
	t.Parallel()

	root := writeModule(t, map[string]string{"a.go": "package main\n", "b.go": "package main\n"})
	startLog := filepath.Join(t.TempDir(), "starts")
	pool := newPool(t, root, lsptest.Script{StartLog: startLog})

	ctx, cancel := context.WithTimeout(t.Context(), waitBudget)
	defer cancel()

	first, err := pool.Client(ctx, "a.go")
	if err != nil {
		t.Fatalf("starting server: %v", err)
	}

	second, err := pool.Client(ctx, "b.go")
	if err != nil {
		t.Fatalf("second client: %v", err)
	}

	if first != second {
		t.Error("two files of one language got two clients")
	}

	for _, f := range []string{"a.go", "b.go"} {
		version, err := first.Sync(ctx, f)
		if err != nil {
			t.Fatalf("syncing %s: %v", f, err)
		}

		if _, err := first.Diagnostics(ctx, f, version); err != nil {
			t.Fatalf("diagnostics for %s: %v", f, err)
		}
	}

	starts, err := os.ReadFile(startLog) //nolint:gosec // path is this test's own temp file
	if err != nil {
		t.Fatalf("reading start log: %v", err)
	}

	if got := len(strings.Fields(string(starts))); got != 1 {
		t.Errorf("%d server processes started, want 1: %q", got, starts)
	}
}

func TestPoolClientRefusals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantErr error
		server  func(t *testing.T) lsp.Server
		name    string
		file    string
	}{
		{
			name:    "a file no server claims",
			server:  func(t *testing.T) lsp.Server { t.Helper(); return lsptest.Server(t, lsptest.Script{}) },
			file:    "README.md",
			wantErr: lsp.ErrNoServer,
		},
		{
			name: "a server that is not installed",
			server: func(t *testing.T) lsp.Server {
				t.Helper()

				return lsp.Server{Language: "go", Command: "wavez-no-such-language-server", Extensions: []string{".go"}}
			},
			file:    "main.go",
			wantErr: lsp.ErrServerUnavailable,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root := writeModule(t, map[string]string{"main.go": "package main\n"})
			pool := lsp.NewPool(root, tc.server(t))

			ctx, cancel := context.WithTimeout(t.Context(), waitBudget)
			defer cancel()

			_, err := pool.Client(ctx, tc.file)
			if err == nil {
				t.Fatal("expected a refusal")
			}

			if !strings.Contains(err.Error(), tc.wantErr.Error()) {
				t.Errorf("error = %v, want one wrapping %v", err, tc.wantErr)
			}
		})
	}
}

func TestPoolCloseStopsTheServer(t *testing.T) {
	t.Parallel()

	root := writeModule(t, map[string]string{"main.go": "package main\n"})
	pool := lsp.NewPool(root, lsptest.Server(t, lsptest.Script{}))

	ctx, cancel := context.WithTimeout(t.Context(), waitBudget)
	defer cancel()

	client, err := pool.Client(ctx, "main.go")
	if err != nil {
		t.Fatalf("starting server: %v", err)
	}

	if err := pool.Close(ctx); err != nil {
		t.Fatalf("closing pool: %v", err)
	}

	if _, err := client.Sync(ctx, "main.go"); err == nil {
		t.Error("a closed client must not accept a document")
	}
}

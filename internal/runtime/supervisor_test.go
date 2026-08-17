package runtime_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kyleking/wavez/internal/runtime"
)

var errResolve = errors.New("model not found")

// fakeResolver is runtime.GGUFResolver with a fixed answer and a call
// count, so a test can assert that reuse never consults Ollama.
type fakeResolver struct {
	err   error
	path  string
	calls int
}

func (r *fakeResolver) resolve(context.Context, string) (string, error) {
	r.calls++

	return r.path, r.err
}

// harness wires a Supervisor whose process, readiness check, and resolver
// are all fakes, and records what it started.
type harness struct {
	supervisor *runtime.Supervisor
	proc       *fakeProcess
	resolver   *fakeResolver
	args       []string
	started    int
}

func newHarness(probe runtime.HealthChecker, path string, resolveErr, startErr error) *harness {
	h := &harness{proc: newFakeProcess(), resolver: &fakeResolver{path: path, err: resolveErr}}
	h.proc.startErr = startErr

	manager := runtime.NewManager(
		runtime.WithProcessFactory(func(_ string, args []string) runtime.Process {
			h.started++
			h.args = args

			return h.proc
		}),
		runtime.WithHealthChecker(alwaysReady),
	)

	h.supervisor = runtime.NewSupervisor("qwen3:8b", runtime.Config{Port: 8097},
		runtime.WithManager(manager),
		runtime.WithProbe(probe),
		runtime.WithGGUFResolver(h.resolver.resolve),
	)

	return h
}

// assertGGUFArg checks the -m argument llama-server was started with, or,
// for an empty want, that nothing was started or resolved at all.
func (h *harness) assertGGUFArg(t *testing.T, want string) {
	t.Helper()

	if want == "" {
		if h.started != 0 || h.resolver.calls != 0 {
			t.Errorf("starts = %d, resolver calls = %d, want 0 and 0 when reusing",
				h.started, h.resolver.calls)
		}

		return
	}

	if len(h.args) < 2 || h.args[1] != want {
		t.Errorf("llama-server args = %v, want -m %s", h.args, want)
	}
}

func TestSupervisor_Ensure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		probe       runtime.HealthChecker
		wantGGUFArg string
		wantManaged bool
	}{
		{
			name:        "reuses a server already answering the port",
			probe:       alwaysReady,
			wantManaged: false,
		},
		{
			name:        "starts one from the resolved GGUF when nothing answers",
			probe:       neverReady,
			wantGGUFArg: "/blobs/sha256-abc",
			wantManaged: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := newHarness(tt.probe, "/blobs/sha256-abc", nil, nil)

			endpoint, err := h.supervisor.Ensure(context.Background())
			if err != nil {
				t.Fatalf("Ensure: %v", err)
			}

			if got, want := endpoint.BaseURL, "http://127.0.0.1:8097/v1"; got != want {
				t.Errorf("BaseURL = %q, want %q", got, want)
			}

			if endpoint.Managed != tt.wantManaged {
				t.Errorf("Managed = %v, want %v", endpoint.Managed, tt.wantManaged)
			}

			h.assertGGUFArg(t, tt.wantGGUFArg)
		})
	}
}

func TestSupervisor_Ensure_FailuresNameTheirCause(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		path        string
		resolveErr  error
		startErr    error
		wantErr     string
		wantStarted int
	}{
		{
			name:       "unresolvable model",
			resolveErr: errResolve,
			wantErr:    "resolving GGUF for qwen3:8b",
		},
		{
			name:        "binary that will not start",
			path:        "/blobs/sha256-abc",
			startErr:    os.ErrNotExist,
			wantErr:     "starting llama-server for qwen3:8b",
			wantStarted: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := newHarness(neverReady, tt.path, tt.resolveErr, tt.startErr)

			_, err := h.supervisor.Ensure(context.Background())
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Ensure() error = %v, want one containing %q", err, tt.wantErr)
			}

			if h.started != tt.wantStarted {
				t.Errorf("process starts = %d, want %d", h.started, tt.wantStarted)
			}
		})
	}
}

func TestSupervisor_Stop(t *testing.T) {
	t.Parallel()

	tests := []struct {
		probe       runtime.HealthChecker
		name        string
		wantSignals int
	}{
		{name: "stops the server it started", probe: neverReady, wantSignals: 1},
		{name: "leaves an externally started server alone", probe: alwaysReady, wantSignals: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			proc := newFakeProcess()
			proc.stopOnSignal = true

			manager := runtime.NewManager(
				runtime.WithProcessFactory(func(string, []string) runtime.Process { return proc }),
				runtime.WithHealthChecker(alwaysReady),
			)

			resolver := &fakeResolver{path: "/blobs/sha256-abc"}

			s := runtime.NewSupervisor("qwen3:8b", runtime.Config{Port: 8098},
				runtime.WithManager(manager),
				runtime.WithProbe(tt.probe),
				runtime.WithGGUFResolver(resolver.resolve),
			)

			if _, err := s.Ensure(context.Background()); err != nil {
				t.Fatalf("Ensure: %v", err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()

			if err := s.Stop(ctx); err != nil {
				t.Fatalf("Stop: %v", err)
			}

			if got := proc.signalCount(); got != tt.wantSignals {
				t.Errorf("signals sent = %d, want %d", got, tt.wantSignals)
			}

			if tt.wantSignals > 0 && proc.signals[0] != syscall.SIGTERM {
				t.Errorf("first signal = %v, want SIGTERM", proc.signals[0])
			}
		})
	}
}

func TestSupervisor_Ensure_ReusesTheServerItStarted(t *testing.T) {
	t.Parallel()

	proc := newFakeProcess()
	started := 0

	manager := runtime.NewManager(
		runtime.WithProcessFactory(func(string, []string) runtime.Process {
			started++

			return proc
		}),
		runtime.WithHealthChecker(alwaysReady),
	)

	resolver := &fakeResolver{path: "/blobs/sha256-abc"}

	s := runtime.NewSupervisor("qwen3:8b", runtime.Config{},
		runtime.WithManager(manager),
		runtime.WithProbe(neverReady),
		runtime.WithGGUFResolver(resolver.resolve),
	)

	first, err := s.Ensure(context.Background())
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	second, err := s.Ensure(context.Background())
	if err != nil {
		t.Fatalf("second Ensure: %v", err)
	}

	if first != second {
		t.Errorf("second Ensure() = %+v, want the first endpoint %+v", second, first)
	}

	if first.BaseURL != runtime.LocalBaseURL(runtime.DefaultPort) {
		t.Errorf("BaseURL = %q, want the default port", first.BaseURL)
	}

	if started != 1 {
		t.Errorf("process starts = %d, want 1", started)
	}
}

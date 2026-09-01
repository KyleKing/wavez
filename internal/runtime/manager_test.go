package runtime_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kyleking/wavez/internal/runtime"
)

func alwaysReady(context.Context, string) error { return nil }

func neverReady(context.Context, string) error { return runtime.ErrNotReady }

func TestManager_Start_ReadyImmediately(t *testing.T) {
	t.Parallel()

	proc := newFakeProcess()

	var gotName string

	var gotArgs []string

	m := runtime.NewManager(
		runtime.WithProcessFactory(func(name string, args []string) runtime.Process {
			gotName, gotArgs = name, args

			return proc
		}),
		runtime.WithHealthChecker(alwaysReady),
	)

	srv, err := m.Start(context.Background(), runtime.Config{GGUFPath: "model.gguf", Port: 8096})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	if got, want := srv.BaseURL(), "http://127.0.0.1:8096/v1"; got != want {
		t.Errorf("BaseURL() = %q, want %q", got, want)
	}

	if gotName != runtime.DefaultBinary {
		t.Errorf("process factory name = %q, want %q", gotName, runtime.DefaultBinary)
	}

	wantArgs := []string{
		"-m", "model.gguf",
		"--host", "127.0.0.1",
		"--port", "8096",
		"-c", "8192",
		"-np", "1",
		"--spec-type", "ngram-simple",
		"--cache-reuse", "256",
		"--cache-ram", "512",
		"--jinja",
		"--chat-template-kwargs", `{"enable_thinking":false}`,
	}
	if len(gotArgs) != len(wantArgs) {
		t.Fatalf("process factory args = %v, want %v", gotArgs, wantArgs)
	}

	for i, a := range wantArgs {
		if gotArgs[i] != a {
			t.Errorf("process factory args[%d] = %q, want %q", i, gotArgs[i], a)
		}
	}
}

func TestManager_Start_CacheRAMOverride(t *testing.T) {
	t.Parallel()

	proc := newFakeProcess()

	var gotArgs []string

	m := runtime.NewManager(
		runtime.WithProcessFactory(func(_ string, args []string) runtime.Process {
			gotArgs = args

			return proc
		}),
		runtime.WithHealthChecker(alwaysReady),
	)

	cfg := runtime.Config{GGUFPath: "model.gguf", Port: 8097, CacheRAMMiB: 128}
	if _, err := m.Start(context.Background(), cfg); err != nil {
		t.Fatalf("Start: %v", err)
	}

	want := "--cache-ram"
	i := slices.Index(gotArgs, want)
	if i < 0 || i+1 >= len(gotArgs) {
		t.Fatalf("args %v missing %s value", gotArgs, want)
	}

	if got, w := gotArgs[i+1], "128"; got != w {
		t.Errorf("args %s = %q, want %q", want, got, w)
	}
}

func TestHTTPHealthCheck(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		status  int
		wantErr bool
	}{
		{name: "ready", status: http.StatusOK, wantErr: false},
		{name: "not ready", status: http.StatusServiceUnavailable, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var gotPath string

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				w.WriteHeader(tt.status)
			}))
			t.Cleanup(srv.Close)

			err := runtime.HTTPHealthCheck(context.Background(), srv.URL)
			if (err != nil) != tt.wantErr {
				t.Fatalf("HTTPHealthCheck() error = %v, wantErr %v", err, tt.wantErr)
			}

			if gotPath != "/health" {
				t.Errorf("health check requested %q, want /health", gotPath)
			}
		})
	}
}

// TestManager_Start_WaitsForReadinessViaHTTP drives an httptest.Server that
// reports unavailable twice before succeeding, with the real
// runtime.HTTPHealthCheck polling it, so the readiness loop is exercised
// end to end rather than through a stubbed HealthChecker.
func TestManager_Start_WaitsForReadinessViaHTTP(t *testing.T) {
	t.Parallel()

	var served int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&served, 1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)

			return
		}

		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parsing httptest URL: %v", err)
	}

	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatalf("parsing httptest port: %v", err)
	}

	proc := newFakeProcess()
	tickCh := make(chan time.Time)

	m := runtime.NewManager(
		runtime.WithProcessFactory(func(string, []string) runtime.Process { return proc }),
		runtime.WithHealthChecker(runtime.HTTPHealthCheck),
		runtime.WithTickerFactory(fakeTickerFactory(tickCh)),
	)

	result := make(chan error, 1)

	go func() {
		_, startErr := m.Start(context.Background(), runtime.Config{GGUFPath: "model.gguf", Port: port})
		result <- startErr
	}()

	tickCh <- time.Now()
	tickCh <- time.Now()

	if err := <-result; err != nil {
		t.Fatalf("Start: %v", err)
	}

	if got := atomic.LoadInt32(&served); got != 3 {
		t.Errorf("health endpoint served %d requests, want 3", got)
	}
}

func TestManager_Start_ReadinessTimeout(t *testing.T) {
	t.Parallel()

	proc := newFakeProcess()
	m := runtime.NewManager(
		runtime.WithProcessFactory(func(string, []string) runtime.Process { return proc }),
		runtime.WithHealthChecker(neverReady),
	)

	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Millisecond))
	defer cancel()

	if _, err := m.Start(ctx, runtime.Config{GGUFPath: "model.gguf", Port: 8097}); err == nil {
		t.Fatal("Start with a server that never becomes ready: want error, got nil")
	}

	if !proc.wasKilled() {
		t.Error("Start on readiness timeout did not kill the process")
	}
}

func TestManager_Stop_CleanExit(t *testing.T) {
	t.Parallel()

	proc := newFakeProcess()
	proc.stopOnSignal = true

	m := runtime.NewManager(
		runtime.WithProcessFactory(func(string, []string) runtime.Process { return proc }),
		runtime.WithHealthChecker(alwaysReady),
	)

	if _, err := m.Start(context.Background(), runtime.Config{GGUFPath: "model.gguf", Port: 8098}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := m.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	if proc.wasKilled() {
		t.Error("Stop killed a process that exited cleanly on signal")
	}

	if proc.signalCount() != 1 {
		t.Errorf("signalCount = %d, want 1", proc.signalCount())
	}

	if m.Active() != nil {
		t.Error("Active() is non-nil after Stop")
	}
}

func TestManager_Stop_KillsAfterTimeout(t *testing.T) {
	t.Parallel()

	proc := newFakeProcess()
	m := runtime.NewManager(
		runtime.WithProcessFactory(func(string, []string) runtime.Process { return proc }),
		runtime.WithHealthChecker(alwaysReady),
	)

	if _, err := m.Start(context.Background(), runtime.Config{GGUFPath: "model.gguf", Port: 8099}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Millisecond))
	defer cancel()

	if err := m.Stop(ctx); err == nil {
		t.Fatal("Stop on an unresponsive process: want error, got nil")
	}

	if !proc.wasKilled() {
		t.Error("Stop did not kill an unresponsive process")
	}

	if proc.signalCount() != 1 {
		t.Errorf("signalCount = %d, want 1 (SIGTERM sent before kill)", proc.signalCount())
	}
}

func TestManager_Start_RefusesSecondServer(t *testing.T) {
	t.Parallel()

	proc := newFakeProcess()
	m := runtime.NewManager(
		runtime.WithProcessFactory(func(string, []string) runtime.Process { return proc }),
		runtime.WithHealthChecker(alwaysReady),
	)

	if _, err := m.Start(context.Background(), runtime.Config{GGUFPath: "model.gguf", Port: 8100}); err != nil {
		t.Fatalf("first Start: %v", err)
	}

	_, err := m.Start(context.Background(), runtime.Config{GGUFPath: "other.gguf", Port: 8101})
	if !errors.Is(err, runtime.ErrAlreadyRunning) {
		t.Errorf("second Start error = %v, want ErrAlreadyRunning", err)
	}
}

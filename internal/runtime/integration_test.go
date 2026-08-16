package runtime_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/kyleking/wavez/internal/runtime"
)

// TestManager_Start_RealLlamaServer starts a real llama-server against a
// real GGUF file. It is skipped unless WAVEZ_LLAMA_SERVER_BIN names the
// llama-server binary and WAVEZ_TEST_GGUF_PATH names a GGUF model file, so
// it never runs in CI.
func TestManager_Start_RealLlamaServer(t *testing.T) {
	t.Parallel()

	bin := os.Getenv("WAVEZ_LLAMA_SERVER_BIN")
	gguf := os.Getenv("WAVEZ_TEST_GGUF_PATH")

	if bin == "" || gguf == "" {
		t.Skip("WAVEZ_LLAMA_SERVER_BIN and WAVEZ_TEST_GGUF_PATH not both set; " +
			"skipping live llama-server integration test")
	}

	m := runtime.NewManager()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	srv, err := m.Start(ctx, runtime.Config{Binary: bin, GGUFPath: gguf, Port: 8099})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	if srv.BaseURL() == "" {
		t.Error("BaseURL() is empty for a started server")
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer stopCancel()

	if err := m.Stop(stopCtx); err != nil {
		t.Errorf("Stop: %v", err)
	}
}

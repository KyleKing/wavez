package ollama_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kyleking/wavez/internal/ollama"
)

// tagsBody and manifestBody are trimmed captures of the real endpoints, kept
// byte-exact where it matters: the manifest's digest is the sha256 of these
// bytes, which is the whole of the update check.
const (
	tagsBody = `{"models":[{"name":"qwen3:8b","digest":"500a1f06","size":5225388164,` +
		`"details":{"family":"qwen3","parameter_size":"8.2B","quantization_level":"Q4_K_M",` +
		`"context_length":40960}}]}`
	manifestBody = `{"schemaVersion":2,"config":{"size":487},` +
		`"layers":[{"size":5225387677}]}`
)

func newTestClient(t *testing.T, handler http.HandlerFunc) *ollama.Client {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	return ollama.New(ollama.WithBaseURL(srv.URL), ollama.WithRegistryURL(srv.URL))
}

func TestClient_ListAndRemote(t *testing.T) {
	t.Parallel()

	var manifestPath string

	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		body := tagsBody
		if r.URL.Path != "/api/tags" {
			manifestPath, body = r.URL.Path, manifestBody
		}
		if _, err := io.WriteString(w, body); err != nil {
			t.Errorf("writing body: %v", err)
		}
	})

	models, err := c.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(models) != 1 {
		t.Fatalf("List returned %d models, want 1", len(models))
	}

	m := models[0]
	if m.Repo() != "qwen3" || m.Tag() != "8b" || m.Quant != "Q4_K_M" || m.SizeBytes != 5225388164 {
		t.Errorf("model = %+v, want qwen3:8b at Q4_K_M and its byte size", m)
	}

	remote, err := c.Remote(context.Background(), "qwen3:8b")
	if err != nil {
		t.Fatalf("Remote: %v", err)
	}

	sum := sha256.Sum256([]byte(manifestBody))
	if remote.Digest != hex.EncodeToString(sum[:]) {
		t.Errorf("Digest = %q, want the sha256 of the manifest bytes", remote.Digest)
	}

	// The registry's own size is config plus layers, which is exactly what
	// Ollama reports on disk, so an install's delta is exact before it runs.
	if remote.SizeBytes != m.SizeBytes {
		t.Errorf("remote size = %d, want the installed size %d", remote.SizeBytes, m.SizeBytes)
	}
	if manifestPath != "/v2/library/qwen3/manifests/8b" {
		t.Errorf("manifest path = %q, want an unqualified name resolved into the library namespace", manifestPath)
	}
}

func TestClient_PullReportsAnErrorInTheProgressStream(t *testing.T) {
	t.Parallel()

	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		if _, err := io.WriteString(w, `{"status":"pulling"}`+"\n"+`{"error":"file does not exist"}`+"\n"); err != nil {
			t.Errorf("writing body: %v", err)
		}
	})

	if err := c.Pull(context.Background(), "nope:latest"); err == nil {
		t.Fatal("Pull returned nil for a stream that reported an error")
	}
}

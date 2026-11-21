package decide_test

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/config"
	"github.com/kyleking/wavez/internal/decide"
)

func TestFromConfig_WithoutAnEndpointThereIsOnlyTheLog(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	d := decide.FromConfig(root, config.Decide{})

	got := d.Decide(t.Context(), question(), "state")

	assert.Equal(t, decide.BackendFallback, got.Backend)
	assert.Equal(t, "no backend", got.Reason)

	records := readLog(t, filepath.Join(root, ".wavez", "decisions.jsonl"))
	require.Len(t, records, 1)
	assert.Equal(t, got.Choice, records[0].Choice)
	assert.Equal(t, filepath.Join(root, ".wavez", "decisions.jsonl"), decide.LogPath(root))

	assert.NoDirExists(t, decide.CacheDir(root))
}

func TestFromConfig_WiresTheEndpointTheCacheAndTheLog(t *testing.T) {
	t.Setenv(decide.APIKeyEnv, "from-env")

	var (
		calls int
		auth  string
	)

	server := serve(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		auth = r.Header.Get("Authorization")

		send(t, w, reply("C"))
	})

	root := t.TempDir()
	// A zero timeout, as a Config built by hand has, takes the default.
	d := decide.FromConfig(root, config.Decide{Endpoint: server.URL, Model: "tiny"})

	first := d.Decide(t.Context(), question(), "state")
	second := d.Decide(t.Context(), question(), "state")

	assert.Equal(t, "split", first.Choice)
	assert.Equal(t, "openai", first.Backend)
	assert.Equal(t, "tiny", first.Model)
	assert.Equal(t, decide.BackendCache, second.Backend)
	assert.Equal(t, 1, calls)
	assert.Equal(t, "Bearer from-env", auth)

	entries, err := os.ReadDir(filepath.Join(root, ".wavez", "cache", "decide"))
	require.NoError(t, err)
	assert.Len(t, entries, 1)
	assert.Len(t, readLog(t, decide.LogPath(root)), 2)
}

func TestFromConfig_ADeadEndpointIsTheFallback(t *testing.T) {
	t.Parallel()

	d := decide.FromConfig(t.TempDir(), config.Decide{
		Endpoint: "http://127.0.0.1:0", Model: "tiny", Timeout: 200 * time.Millisecond,
	})

	got := d.Decide(t.Context(), question(), "state")

	assert.Equal(t, "direct", got.Choice)
	assert.Equal(t, decide.BackendFallback, got.Backend)
	assert.NotEmpty(t, got.Reason)
}

package decide_test

import (
	"net/http"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/config"
	"github.com/kyleking/wavez/internal/decide"
)

// recorder is a model server that counts its requests and keeps the last
// Authorization header.
type recorder struct {
	auth  atomic.Value
	url   string
	calls atomic.Int32
}

func record(t *testing.T) *recorder {
	t.Helper()

	r := &recorder{}
	r.url = serve(t, func(w http.ResponseWriter, req *http.Request) {
		r.calls.Add(1)
		r.auth.Store(req.Header.Get("Authorization"))

		send(t, w, reply("C"))
	}).URL

	return r
}

func TestFromConfig_WithoutAnEndpointThereIsOnlyTheLog(t *testing.T) {
	t.Setenv(decide.EndpointEnv, "")

	root := t.TempDir()

	got := decide.FromConfig(root, config.Decide{}).Decide(t.Context(), question(), "state")

	assert.Equal(t, decide.BackendFallback, got.Backend)
	assert.Equal(t, "no backend", got.Reason)

	lines := readLog(t, filepath.Join(root, ".wavez", "decisions.jsonl"))
	require.Len(t, lines, 1)
	assert.Equal(t, got.Choice, lines[0].Choice)
}

func TestConfigured_KeepsNoLog(t *testing.T) {
	t.Setenv(decide.EndpointEnv, "")

	server := record(t)

	// The working directory is the only place a log could land by accident.
	t.Chdir(t.TempDir())

	got := decide.Configured(config.Decide{Endpoint: server.url, Model: "tiny"}).
		Decide(t.Context(), question(), "state")

	assert.Equal(t, "split", got.Choice)
	assert.Equal(t, "openai", got.Backend)
	assert.NoDirExists(t, ".wavez")
}

func TestFromConfig_AProjectEndpointOnThisMachineIsAskedWithoutTheKey(t *testing.T) {
	t.Setenv(decide.EndpointEnv, "")
	t.Setenv(decide.APIKeyEnv, "the-users-key")

	server := record(t)
	root := t.TempDir()

	// A zero timeout, as a Config built by hand has, takes the default.
	got := decide.FromConfig(root, config.Decide{Endpoint: server.url, Model: "tiny"}).
		Decide(t.Context(), question(), "state")

	assert.Equal(t, "split", got.Choice)
	assert.Equal(t, "openai", got.Backend)
	assert.Equal(t, "tiny", got.Model)
	assert.EqualValues(t, 1, server.calls.Load())
	assert.Empty(t, server.auth.Load(), "a checked-in endpoint never receives the user's key")
	assert.Len(t, readLog(t, filepath.Join(root, ".wavez", "decisions.jsonl")), 1)
}

func TestFromConfig_TheKeyGoesOnlyToTheEndpointTheEnvironmentNames(t *testing.T) {
	projects, users := record(t), record(t)

	t.Setenv(decide.EndpointEnv, users.url)
	t.Setenv(decide.APIKeyEnv, "the-users-key")

	got := decide.FromConfig(t.TempDir(), config.Decide{Endpoint: projects.url, Model: "tiny"}).
		Decide(t.Context(), question(), "state")

	assert.Equal(t, "openai", got.Backend)
	assert.EqualValues(t, 1, users.calls.Load())
	assert.Equal(t, "Bearer the-users-key", users.auth.Load())
	assert.Zero(t, projects.calls.Load(), "the environment's endpoint replaces the project's")
}

func TestFromConfig_TheEnvironmentNamingTheProjectsEndpointSendsTheKey(t *testing.T) {
	server := record(t)

	t.Setenv(decide.EndpointEnv, server.url)
	t.Setenv(decide.APIKeyEnv, "the-users-key")

	decide.FromConfig(t.TempDir(), config.Decide{Endpoint: server.url, Model: "tiny"}).
		Decide(t.Context(), question(), "state")

	assert.Equal(t, "Bearer the-users-key", server.auth.Load())
}

func TestFromConfig_AProjectEndpointOffThisMachineIsNotAsked(t *testing.T) {
	t.Setenv(decide.EndpointEnv, "")
	t.Setenv(decide.APIKeyEnv, "the-users-key")

	// None of these is dialed: a request to any of them would outlast the
	// bound below or need a network the tests do not have.
	for _, endpoint := range []string{
		"https://models.example.com/v1",
		"http://203.0.113.7:8080/v1",
		"http://localhost@models.example.com/v1",
		"http://127.0.0.1.models.example.com/v1",
		"http://localhost.models.example.com/v1",
		"http://[2001:db8::1]:8080/v1",
	} {
		start := time.Now()

		got := decide.FromConfig(t.TempDir(), config.Decide{Endpoint: endpoint, Model: "tiny", Timeout: time.Minute}).
			Decide(t.Context(), question(), "a prompt the repository must not receive")

		assert.Equal(t, "direct", got.Choice, endpoint)
		assert.Equal(t, decide.BackendFallback, got.Backend, endpoint)
		assert.Equal(t, decide.ErrEndpointNotTrusted.Error(), got.Reason, endpoint)
		assert.Less(t, time.Since(start), time.Second, endpoint)
	}
}

func TestFromConfig_TheNamesOfThisMachineAreLoopback(t *testing.T) {
	t.Setenv(decide.EndpointEnv, "")

	// Nothing listens on port 0, so a loopback endpoint is dialed and
	// refused where another host would not be dialed at all.
	for _, endpoint := range []string{"http://localhost:0/v1", "http://127.0.0.1:0/v1", "http://[::1]:0/v1"} {
		got := decide.FromConfig(t.TempDir(), config.Decide{Endpoint: endpoint, Model: "tiny", Timeout: time.Second}).
			Decide(t.Context(), question(), "state")

		assert.Equal(t, decide.BackendFallback, got.Backend, endpoint)
		assert.Contains(t, got.Reason, "asking tiny", endpoint)
	}
}

func TestFromConfig_AnEnvironmentEndpointWithNoModelFallsBack(t *testing.T) {
	server := record(t)

	t.Setenv(decide.EndpointEnv, server.url)

	got := decide.FromConfig(t.TempDir(), config.Decide{}).Decide(t.Context(), question(), "state")

	assert.Equal(t, decide.BackendFallback, got.Backend)
	assert.Equal(t, decide.ErrNoModel.Error(), got.Reason)
	assert.Zero(t, server.calls.Load())
}

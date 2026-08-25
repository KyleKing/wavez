package app

import (
	"testing"

	"github.com/kyleking/wavez/internal/config"
	"github.com/kyleking/wavez/internal/llm/openaic"
)

// A fast tier pointed at a provider is a hosted tier whatever its name, so
// it resolves a key the way every other tier does. Without the fallback it
// sent no credential at all and every request came back unauthorized.
func TestFastTierTakesTheHostedKeyWhenPointedAtAProvider(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		tier    config.Tier
		hosted  string
		wantKey bool
	}{
		{
			name:    "the loopback server needs none",
			tier:    config.Tier{Model: "qwen3:8b"},
			hosted:  "printf sk-test",
			wantKey: false,
		},
		{
			name:    "a remote endpoint falls back to the hosted command",
			tier:    config.Tier{Model: "qwen/qwen3-8b", BaseURL: "https://example.invalid/v1"},
			hosted:  "printf sk-test",
			wantKey: true,
		},
		{
			name:    "the tier's own command still wins",
			tier:    config.Tier{Model: "m", BaseURL: "https://example.invalid/v1", KeyCommand: "printf sk-own"},
			hosted:  "printf sk-test",
			wantKey: true,
		},
		{
			name:    "a remote endpoint with no command anywhere sends none",
			tier:    config.Tier{Model: "m", BaseURL: "https://example.invalid/v1"},
			hosted:  "",
			wantKey: false,
		},
	}

	// The baseline is what a tier with no credential anywhere is built with,
	// so adding an unrelated option to every fast tier does not read as a
	// key having appeared.
	base := len(fastProviderOptions(t.Context(), config.Config{},
		config.Tier{Model: "m"}, "http://127.0.0.1:8080/v1"))

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := config.Config{HostedKeyCommand: tt.hosted}

			opts := fastProviderOptions(t.Context(), cfg, tt.tier, "http://127.0.0.1:8080/v1")
			if got := len(opts) > base; got != tt.wantKey {
				t.Errorf("a key option was added = %v, want %v", got, tt.wantKey)
			}
		})
	}
}

// The data-collection denial rides on the dialect, so misreading an
// endpoint as a llama-server is what would send a private repository to a
// provider that stores prompts. A URL that does not parse is the router for
// the same reason.
func TestDialectForNamesTheBackend(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		baseURL string
		want    openaic.Dialect
	}{
		{baseURL: "http://127.0.0.1:8080/v1", want: openaic.DialectLlamaCpp},
		{baseURL: "https://m4.tailnet.ts.net/v1", want: openaic.DialectLlamaCpp},
		{baseURL: DefaultHostedBaseURL, want: openaic.DialectOpenRouter},
		{baseURL: "https://openrouter.ai/api/v1", want: openaic.DialectOpenRouter},
		{baseURL: "://not a url", want: openaic.DialectOpenRouter},
	} {
		t.Run(tt.baseURL, func(t *testing.T) {
			t.Parallel()

			if got := dialectFor(tt.baseURL); got != tt.want {
				t.Errorf("dialectFor(%q) = %q, want %q", tt.baseURL, got, tt.want)
			}
		})
	}
}

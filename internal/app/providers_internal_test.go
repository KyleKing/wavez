package app

import (
	"testing"

	"github.com/kyleking/wavez/internal/config"
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := config.Config{HostedKeyCommand: tt.hosted}

			opts := fastProviderOptions(t.Context(), cfg, tt.tier, "http://127.0.0.1:8080/v1")
			if got := len(opts) > 2; got != tt.wantKey {
				t.Errorf("a key option was added = %v, want %v", got, tt.wantKey)
			}
		})
	}
}

package app

import (
	"testing"

	"github.com/kyleking/wavez/internal/config"
	"github.com/kyleking/wavez/internal/runtime"
)

// The served window follows the project config unless the models screen
// saved a size for this model, and either way it is what llama-server
// starts with and what the loop routes against.
func TestLocalRuntimePrefersSavedSettingsOverConfig(t *testing.T) {
	t.Parallel()

	cfg := config.Config{LocalPort: 8080, ContextWindow: 16384}

	fromConfig := localRuntime(cfg, Options{})
	if fromConfig.ContextSize != 16384 || fromConfig.Port != 8080 {
		t.Errorf("from config: %+v, want the config's window on its port", fromConfig)
	}

	saved := localRuntime(cfg, Options{LocalRuntime: runtime.Config{ContextSize: 32768, SpecType: "none"}})
	if saved.ContextSize != 32768 || saved.SpecType != "none" || saved.Port != 8080 {
		t.Errorf("saved: %+v, want the saved size and spec on the config's port", saved)
	}
}

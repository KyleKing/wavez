package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kyleking/wavez/internal/api"
	"github.com/kyleking/wavez/internal/ollama"
	"github.com/kyleking/wavez/internal/runtime"
)

// Permissions for the daemon's own model settings file.
const (
	settingsDirMode  = 0o750
	settingsFileMode = 0o600
)

// modelListTimeout bounds a listing, which is a local HTTP call.
const modelListTimeout = 5 * time.Second

// modelRegistryTimeout bounds an update check, which crosses the network.
const modelRegistryTimeout = 20 * time.Second

// modelPullTimeout bounds an install. A multi-gigabyte pull over a slow link
// is the case it has to leave room for.
const modelPullTimeout = 2 * time.Hour

// Sentinel errors the model commands return.
var (
	// ErrNoModelStore reports a daemon built without a model store, which is
	// a daemon that cannot answer any model command rather than one with an
	// empty list.
	ErrNoModelStore = errors.New("daemon: no model store configured")
	ErrModelUnnamed = errors.New("daemon: no model named")
	// ErrModelNotInstalled refuses a removal of something that is not there,
	// which is a mistyped name rather than a no-op.
	ErrModelNotInstalled   = errors.New("daemon: model is not installed")
	ErrNoSettings          = errors.New("daemon: no settings in the command")
	ErrUnknownModelCommand = errors.New("daemon: not a model command")
)

// ModelStore is what the daemon drives model management through: the models
// on disk, what the registry holds for a reference, and the two deliberate
// actions. *ollama.Client satisfies it.
type ModelStore interface {
	List(ctx context.Context) ([]ollama.Model, error)
	Remote(ctx context.Context, ref string) (ollama.Remote, error)
	Pull(ctx context.Context, ref string) error
	Remove(ctx context.Context, ref string) error
}

// modelDefaults is what wavez ships for every model, from the llama-server
// flags DESIGN.md tuned on this laptop. A zero thread or batch count is
// llama-server's own choice, which is not a number wavez has measured a
// better value for.
func modelDefaults() api.ModelSettings {
	return api.ModelSettings{
		ContextSize: runtime.DefaultContextSize,
		CacheReuse:  runtime.DefaultCacheReuse,
		SpecType:    runtime.DefaultSpecType,
	}
}

// modelRegistry holds per-model runtime settings and the last update check,
// persisted beside the thread logs so an edited setting survives a restart.
// A check is never persisted: it is a claim about the network a minute ago.
type modelRegistry struct {
	settings map[string]api.ModelSettings
	checked  map[string]bool
	updates  map[string]bool
	path     string
	mu       sync.Mutex
}

// SavedLocalRuntime reads the runtime flags the models screen has saved
// for model in the settings file at path (WithModelSettingsPath), as the
// config a supervisor starts llama-server with. A model with no saved
// settings yields the zero Config, which the supervisor fills from its
// defaults.
func SavedLocalRuntime(path, model string) runtime.Config {
	settings, _, _ := newModelRegistry(path).view(model)

	return runtime.Config{
		SpecType:    settings.SpecType,
		ContextSize: settings.ContextSize,
		CacheReuse:  settings.CacheReuse,
		Threads:     settings.Threads,
		BatchSize:   settings.BatchSize,
	}
}

func newModelRegistry(path string) *modelRegistry {
	r := &modelRegistry{
		settings: map[string]api.ModelSettings{},
		checked:  map[string]bool{},
		updates:  map[string]bool{},
		path:     path,
	}
	r.load()

	return r
}

func (r *modelRegistry) load() {
	if r.path == "" {
		return
	}

	b, err := os.ReadFile(r.path)
	if err != nil {
		return
	}

	var stored map[string]api.ModelSettings
	if err := json.Unmarshal(b, &stored); err != nil {
		return
	}

	r.settings = stored
}

func (r *modelRegistry) save() error {
	if r.path == "" {
		return nil
	}

	b, err := json.Marshal(r.settings)
	if err != nil {
		return fmt.Errorf("encoding model settings: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(r.path), settingsDirMode); err != nil {
		return fmt.Errorf("creating model settings directory: %w", err)
	}
	if err := os.WriteFile(r.path, b, settingsFileMode); err != nil {
		return fmt.Errorf("writing model settings: %w", err)
	}

	return nil
}

func (r *modelRegistry) setSettings(name string, s api.ModelSettings) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.settings[name] = s

	return r.save()
}

func (r *modelRegistry) noteCheck(name string, update bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.checked[name], r.updates[name] = true, update
}

func (r *modelRegistry) view(name string) (api.ModelSettings, bool, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.settings[name], r.checked[name], r.updates[name]
}

// models renders the whole list every model command answers with.
func (s *Server) modelInfos(ctx context.Context) ([]api.ModelInfo, error) {
	if s.modelStore == nil {
		return nil, ErrNoModelStore
	}

	installed, err := s.modelStore.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing models: %w", err)
	}

	total := s.memTotal()
	loaded := s.localModel()

	out := make([]api.ModelInfo, 0, len(installed))
	for _, m := range installed {
		settings, checked, update := s.modelReg.view(m.Name)
		out = append(out, api.ModelInfo{
			Name:            m.Name,
			Tag:             m.Tag(),
			Quant:           m.Quant,
			ParamSize:       m.ParamSize,
			SizeBytes:       m.SizeBytes,
			FreeBytes:       freeAfter(total, m.SizeBytes),
			Settings:        settings,
			Defaults:        modelDefaults(),
			Checked:         checked,
			UpdateAvailable: update,
			Loaded:          m.Name == loaded,
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out, nil
}

// freeAfter is what stays free once a model of size bytes is resident. It is
// the ceiling minus the model, not minus what is in use now, because the
// question the screen answers is what loading this one costs.
func freeAfter(total, size uint64) uint64 {
	if total <= size {
		return 0
	}

	return total - size
}

func (s *Server) memTotal() uint64 {
	if s.stats == nil {
		return 0
	}

	return s.stats.Stats().TotalBytes
}

// checkModels asks the registry whether a newer manifest exists, for one
// model or for all of them. It never installs anything, per DESIGN.md's
// local model management section.
func (s *Server) checkModels(ctx context.Context, name string) error {
	if s.modelStore == nil {
		return ErrNoModelStore
	}

	installed, err := s.modelStore.List(ctx)
	if err != nil {
		return fmt.Errorf("listing models: %w", err)
	}

	var errs []error

	for _, m := range installed {
		if name != "" && m.Name != name {
			continue
		}

		remote, err := s.modelStore.Remote(ctx, m.Name)
		if err != nil {
			errs = append(errs, err)

			continue
		}

		s.modelReg.noteCheck(m.Name, !strings.EqualFold(remote.Digest, m.Digest))
	}

	return errors.Join(errs...)
}

// installModel pulls name. Without confirm it reports what the pull would
// take on disk and installs nothing, which is the confirmation the screen
// shows before it acts.
func (s *Server) installModel(ctx context.Context, name string, confirm bool) (string, error) {
	if s.modelStore == nil {
		return "", ErrNoModelStore
	}
	if name == "" {
		return "", ErrModelUnnamed
	}

	remote, err := s.modelStore.Remote(ctx, name)
	if err != nil {
		return "", fmt.Errorf("reading %s from the registry: %w", name, err)
	}
	if !confirm {
		return fmt.Sprintf("installing %s adds %s to disk", name, humanBytes(remote.SizeBytes)), nil
	}

	if err := s.modelStore.Pull(ctx, name); err != nil {
		return "", fmt.Errorf("installing %s: %w", name, err)
	}

	return "installed " + name, nil
}

// removeModel uninstalls name. Wavez never removes a model it thinks is
// unused: Ollama serves other tools on this machine and wavez cannot see
// their working set, so only a named model with a confirmation goes.
func (s *Server) removeModel(ctx context.Context, name string, confirm bool) (string, error) {
	if s.modelStore == nil {
		return "", ErrNoModelStore
	}
	if name == "" {
		return "", ErrModelUnnamed
	}

	installed, err := s.modelStore.List(ctx)
	if err != nil {
		return "", fmt.Errorf("listing models: %w", err)
	}

	var size uint64

	found := false
	for _, m := range installed {
		if m.Name == name {
			size, found = m.SizeBytes, true
		}
	}
	if !found {
		return "", fmt.Errorf("%w: %s", ErrModelNotInstalled, name)
	}

	if !confirm {
		return fmt.Sprintf("removing %s frees %s", name, humanBytes(size)), nil
	}

	if err := s.modelStore.Remove(ctx, name); err != nil {
		return "", fmt.Errorf("removing %s: %w", name, err)
	}

	return fmt.Sprintf("removed %s, freeing %s", name, humanBytes(size)), nil
}

func humanBytes(b uint64) string {
	const gb = 1 << 30

	return fmt.Sprintf("%.1f GB", float64(b)/gb)
}

// runModelCommand performs one model command's action and reports what it
// did, leaving the caller to render the list every model command answers
// with.
func (s *Server) runModelCommand(ctx context.Context, cmd api.Command) (string, error) {
	switch cmd.Kind {
	case api.CmdModels:
		return "", nil
	case api.CmdModelCheck:
		checkCtx, cancel := context.WithTimeout(ctx, modelRegistryTimeout)
		defer cancel()

		return "", s.checkModels(checkCtx, cmd.Model)
	case api.CmdModelInstall:
		installCtx, cancel := context.WithTimeout(ctx, modelPullTimeout)
		defer cancel()

		return s.installModel(installCtx, cmd.Model, cmd.Confirm)
	case api.CmdModelRemove:
		removeCtx, cancel := context.WithTimeout(ctx, modelListTimeout)
		defer cancel()

		return s.removeModel(removeCtx, cmd.Model, cmd.Confirm)
	case api.CmdModelSettings:
		return s.applyModelSettings(cmd)
	default:
		return "", fmt.Errorf("%w: %s", ErrUnknownModelCommand, cmd.Kind)
	}
}

func (s *Server) applyModelSettings(cmd api.Command) (string, error) {
	if cmd.Model == "" {
		return "", ErrModelUnnamed
	}
	if cmd.Settings == nil {
		return "", ErrNoSettings
	}
	if err := s.modelReg.setSettings(cmd.Model, *cmd.Settings); err != nil {
		return "", err
	}

	return "saved settings for " + cmd.Model, nil
}

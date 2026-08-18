package daemon_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/kyleking/wavez/internal/api"
	"github.com/kyleking/wavez/internal/daemon"
	"github.com/kyleking/wavez/internal/ollama"
)

// fakeModelStore stands in for Ollama and its registry, recording what the
// daemon asked it to do so a test can assert that a preview installed and
// removed nothing.
type fakeModelStore struct {
	remote  map[string]ollama.Remote
	models  []ollama.Model
	pulled  []string
	removed []string
	mu      sync.Mutex
}

var errNoSuchRemote = errors.New("no such model in the registry")

func (f *fakeModelStore) List(context.Context) ([]ollama.Model, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]ollama.Model(nil), f.models...), nil
}

func (f *fakeModelStore) Remote(_ context.Context, ref string) (ollama.Remote, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	r, ok := f.remote[ref]
	if !ok {
		return ollama.Remote{}, errNoSuchRemote
	}

	return r, nil
}

func (f *fakeModelStore) Pull(_ context.Context, ref string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.pulled = append(f.pulled, ref)

	return nil
}

func (f *fakeModelStore) Remove(_ context.Context, ref string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.removed = append(f.removed, ref)

	return nil
}

// acted reports what the store was told to pull and to remove.
//
//nolint:nonamedreturns // two string lists are otherwise indistinguishable
func (f *fakeModelStore) acted() (pulled, removed []string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]string(nil), f.pulled...), append([]string(nil), f.removed...)
}

const (
	gib     = 1 << 30
	staleID = "0000000000000000000000000000000000000000000000000000000000000000"
)

func newModelStore() *fakeModelStore {
	return &fakeModelStore{
		models: []ollama.Model{
			{Name: "qwen3:8b", Digest: "aaaa", Quant: "Q4_K_M", ParamSize: "8.2B", SizeBytes: 5 * gib},
			{Name: "qwen2.5-coder:7b", Digest: staleID, Quant: "Q4_K_M", ParamSize: "7.6B", SizeBytes: 4 * gib},
		},
		remote: map[string]ollama.Remote{
			"qwen3:8b":         {Digest: "AAAA", SizeBytes: 5 * gib},
			"qwen2.5-coder:7b": {Digest: "bbbb", SizeBytes: 4*gib + 1},
			"gemma4:12b":       {Digest: "cccc", SizeBytes: 8 * gib},
		},
	}
}

// modelClient starts a daemon backed by store and returns a connected client.
func modelClient(t *testing.T, store daemon.ModelStore) *client {
	t.Helper()

	h := newHarness(t, nil, withServerOptions(
		daemon.WithModelStore(store),
		daemon.WithStatsSource(fakeStats{mem: daemon.MachineStats{TotalBytes: 16 * gib}}),
	))

	cl := dial(t, h)
	cl.hello()

	return cl
}

func modelByName(t *testing.T, models []api.ModelInfo, name string) api.ModelInfo {
	t.Helper()

	for i := range models {
		if models[i].Name == name {
			return models[i]
		}
	}
	t.Fatalf("no model %q in %+v", name, models)

	return api.ModelInfo{}
}

// modelCase is one command and what the reply must show for it.
type modelCase struct {
	check   func(t *testing.T, rep api.Reply, store *fakeModelStore)
	name    string
	command api.Command
}

func checkList(t *testing.T, rep api.Reply, _ *fakeModelStore) {
	t.Helper()

	m := modelByName(t, rep.Models, "qwen3:8b")
	if m.Quant != "Q4_K_M" || m.SizeBytes != 5*gib || m.FreeBytes != 11*gib {
		t.Errorf("qwen3:8b = %+v, want 5 GiB at Q4_K_M leaving 11 GiB free", m)
	}
	if m.Checked {
		t.Errorf("a listing must not claim an update check ran")
	}
	if m.Defaults.ContextSize == 0 || m.Defaults.SpecType == "" {
		t.Errorf("defaults = %+v, want the shipped llama-server flags", m.Defaults)
	}
}

func checkUpdateCheck(t *testing.T, rep api.Reply, store *fakeModelStore) {
	t.Helper()

	if same := modelByName(t, rep.Models, "qwen3:8b"); !same.Checked || same.UpdateAvailable {
		t.Errorf("matching digests reported an update: %+v", same)
	}
	if stale := modelByName(t, rep.Models, "qwen2.5-coder:7b"); !stale.Checked || !stale.UpdateAvailable {
		t.Errorf("differing digests reported no update: %+v", stale)
	}

	assertNothingHappened(t, store)
}

func checkNote(want string) func(t *testing.T, rep api.Reply, store *fakeModelStore) {
	return func(t *testing.T, rep api.Reply, store *fakeModelStore) {
		t.Helper()

		if rep.Note != want {
			t.Errorf("Note = %q, want %q", rep.Note, want)
		}

		assertNothingHappened(t, store)
	}
}

func checkRemoved(t *testing.T, _ api.Reply, store *fakeModelStore) {
	t.Helper()

	pulled, removed := store.acted()
	if len(pulled) != 0 || len(removed) != 1 || removed[0] != "qwen3:8b" {
		t.Errorf("pulled %v, removed %v, want only qwen3:8b removed", pulled, removed)
	}
}

func checkSettings(t *testing.T, rep api.Reply, _ *fakeModelStore) {
	t.Helper()

	m := modelByName(t, rep.Models, "qwen3:8b")
	if m.Settings.ContextSize != 16384 || m.Settings.Threads != 6 {
		t.Errorf("settings = %+v, want the edited values", m.Settings)
	}
	if m.Defaults.ContextSize == m.Settings.ContextSize {
		t.Errorf("defaults must stay beside an edited value, got %+v", m.Defaults)
	}
}

func modelCases() []modelCase {
	return []modelCase{
		{
			name:    "list reports size, quant, and headroom against the ceiling",
			command: api.Command{ID: "m", Kind: api.CmdModels},
			check:   checkList,
		},
		{
			name:    "check compares the registry digest without installing",
			command: api.Command{ID: "m", Kind: api.CmdModelCheck},
			check:   checkUpdateCheck,
		},
		{
			name:    "install without confirm reports the disk it would take",
			command: api.Command{ID: "m", Kind: api.CmdModelInstall, Model: "gemma4:12b"},
			check:   checkNote("installing gemma4:12b adds 8.0 GB to disk"),
		},
		{
			name:    "remove without confirm reports the disk it would free",
			command: api.Command{ID: "m", Kind: api.CmdModelRemove, Model: "qwen3:8b"},
			check:   checkNote("removing qwen3:8b frees 5.0 GB"),
		},
		{
			name:    "remove with confirm removes only the named model",
			command: api.Command{ID: "m", Kind: api.CmdModelRemove, Model: "qwen3:8b", Confirm: true},
			check:   checkRemoved,
		},
		{
			name: "settings replace what a model is served with",
			command: api.Command{
				ID: "m", Kind: api.CmdModelSettings, Model: "qwen3:8b",
				Settings: &api.ModelSettings{ContextSize: 16384, Threads: 6},
			},
			check: checkSettings,
		},
	}
}

func TestModelCommands(t *testing.T) {
	t.Parallel()

	for _, tt := range modelCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := newModelStore()
			cl := modelClient(t, store)

			cl.send(tt.command)

			rep := cl.recvFor("m")
			if rep.Kind != api.RepModels {
				t.Fatalf("reply = %+v, want a models reply", rep)
			}

			tt.check(t, rep, store)
		})
	}
}

func assertNothingHappened(t *testing.T, store *fakeModelStore) {
	t.Helper()

	if pulled, removed := store.acted(); len(pulled) != 0 || len(removed) != 0 {
		t.Errorf("pulled %v and removed %v, want a preview to act on nothing", pulled, removed)
	}
}

func TestModelCommands_RefusedWithoutAStore(t *testing.T) {
	t.Parallel()

	cl := dial(t, newHarness(t, nil))
	cl.hello()
	cl.send(api.Command{ID: "m", Kind: api.CmdModels})

	if rep := cl.recvFor("m"); rep.Kind != api.RepError {
		t.Fatalf("reply = %+v, want a refusal rather than an empty list", rep)
	}
}

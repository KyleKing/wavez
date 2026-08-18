package tui_test

import (
	"strconv"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"

	"github.com/kyleking/wavez/internal/api"
	"github.com/kyleking/wavez/internal/tui"
)

const gib = 1 << 30

// sampleDiag is a fleet mid-run: every gauge the daemon can measure carries a
// reading, and the ones no subsystem observes yet are named as unmeasured so
// the panel has to render them as dashes.
func sampleDiag() api.Diagnostics {
	return api.Diagnostics{
		LocalModel:     "qwen3:8b",
		MemUsedBytes:   9*gib + gib/2,
		MemTotalBytes:  16 * gib,
		ModelBytes:     6 * gib,
		ModelDiskBytes: 10 * gib,
		CPUPercent:     141,
		CPUDaemon:      3,
		CPUModel:       98,
		ContextUsed:    3100,
		ContextWindow:  8192,
		TokensPerSec:   29.4,
		PrefixHit:      0.944,
		CacheRead:      0.71,
		SpendToday:     0.43,
		GateQueue:      2,
		GateRuns:       38,
		GateFailures:   1,
		Threads:        4,
		NeedsInput:     1,
		ToolCalls:      142,
		Malformed:      4,
		TranscriptRows: 41000,
		CompactionRuns: 3,
		TokensSaved:    12400,
		EventsPerSec:   97,
		Sparks: map[api.Gauge][]float64{
			api.GaugeMemory:       {0.2, 0.3, 0.5, 0.6, 0.6, 0.5, 0.5, 0.6},
			api.GaugeCPU:          {1, 2, 4, 6, 8, 6, 4, 2},
			api.GaugeTokensPerSec: {18, 22, 29, 31, 29, 28, 30, 29},
		},
		PerThread: []api.ThreadDiag{
			{ID: "t1", Name: "fix-lock-timeout", Dir: "calcipy", Context: 3100, Window: 8192, Tokens: 9200},
			{ID: "t3", Name: "add-jj-backend", Dir: "wavez", Context: 1200, Window: 8192, Tokens: 4100, Spend: 0.12},
		},
		Unmeasured: []api.Gauge{
			api.GaugeCPUGates, api.GaugeCPUTUI, api.GaugeEscalations, api.GaugeGateLatency,
			api.GaugeGateRunning, api.GaugeHostedCalls, api.GaugeHostedLatency, api.GaugeLeases,
		},
	}
}

func sampleModels() []api.ModelInfo {
	defaults := api.ModelSettings{ContextSize: 8192, CacheReuse: 256, SpecType: "ngram-simple"}

	return []api.ModelInfo{
		{
			Name: "qwen2.5-coder:7b", Tag: "7b", Quant: "Q4_K_M", ParamSize: "7.6B",
			SizeBytes: 4*gib + gib/2, FreeBytes: 11*gib + gib/2,
			Settings: api.ModelSettings{}, Defaults: defaults,
			Checked: true, UpdateAvailable: true,
		},
		{
			Name: "qwen3:8b", Tag: "8b", Quant: "Q4_K_M", ParamSize: "8.2B",
			SizeBytes: 5 * gib, FreeBytes: 11 * gib,
			Settings: api.ModelSettings{ContextSize: 16384}, Defaults: defaults,
			Checked: true, Loaded: true,
		},
	}
}

// panelSizes spans DESIGN's 80x24 floor and a wide terminal, which is where
// a row that overflows its frame shows up.
var panelSizes = []struct{ w, h int }{{80, 24}, {120, 40}}

func diagnosticsScreen(t *testing.T, opts tui.Options, w, h int) tui.Model {
	t.Helper()

	m := newSized(t, opts, w, h)
	m = apply(t, m, api.Reply{Kind: api.RepDiag, Diag: ptrDiag(sampleDiag())})

	return press(t, m, 'D')
}

func modelsScreen(t *testing.T, opts tui.Options, w, h int) tui.Model {
	t.Helper()

	m := newSized(t, opts, w, h)
	m = press(t, m, 'M')

	return apply(t, m, api.Reply{Kind: api.RepModels, Models: sampleModels()})
}

func ptrDiag(d api.Diagnostics) *api.Diagnostics { return &d }

func monoOptions() tui.Options { return tui.Options{Dir: "~/dev", NoColor: true} }

func TestDiagnosticsPanel_GoldenFrames(t *testing.T) {
	t.Parallel()

	for _, size := range panelSizes {
		out := diagnosticsScreen(t, monoOptions(), size.w, size.h).View().Content
		goldenCompare(t, "diagnostics_"+strconv.Itoa(size.w), out)
	}
}

func TestModelsScreen_GoldenFrames(t *testing.T) {
	t.Parallel()

	for _, size := range panelSizes {
		out := modelsScreen(t, monoOptions(), size.w, size.h).View().Content
		goldenCompare(t, "models_"+strconv.Itoa(size.w), out)
	}
}

func TestDiagnosticsPanel_RendersDashesForUnmeasuredGauges(t *testing.T) {
	t.Parallel()

	out := diagnosticsScreen(t, monoOptions(), 120, 40).View().Content

	assert.Contains(t, out, "29.4", "a measured decode speed must render as a number")
	assert.Contains(t, out, "94%", "prefix hit comes from the runtime's timings")
	assert.Contains(t, out, "leases -", "an unmeasured lease count must render as a dash, never as zero")
	assert.Contains(t, out, "- escalations", "an unmeasured escalation count must render as a dash")
	assert.NotContains(t, out, "leases 0", "a gauge with no source must never read as a measured zero")
}

func TestDiagnosticsPanel_TabWalksSectionsAndEnterDrills(t *testing.T) {
	t.Parallel()

	m := diagnosticsScreen(t, monoOptions(), 120, 40)
	assert.NotContains(t, m.View().Content, "per thread")

	m = apply(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	out := m.View().Content
	assert.Contains(t, out, "per thread")
	assert.Contains(t, out, "fix-lock-timeout")
	assert.Contains(t, out, "3.1k/8.2k", "the drill shows each thread's own occupied window")
}

func TestModelsScreen_ReportsDiskAndUpdateStateWithoutClaimingAnUncheckedOne(t *testing.T) {
	t.Parallel()

	out := modelsScreen(t, monoOptions(), 120, 40).View().Content

	assert.Contains(t, out, "9.5G on disk", "the header totals what every model takes")
	assert.Contains(t, out, "a newer tag exists")
	assert.Contains(t, out, "11.0G", "each row shows what it leaves free against the ceiling")
}

func TestModelsScreen_SettingsShowTheShippedDefaultBesideEachValue(t *testing.T) {
	t.Parallel()

	m := press(t, modelsScreen(t, monoOptions(), 120, 40), 'j')
	m = apply(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	out := m.View().Content
	assert.Contains(t, out, "settings · qwen3:8b")
	assert.Contains(t, out, "served context   16384        default 8192")
	assert.Contains(t, out, "threads", "an unset setting still lists its field")
	assert.Contains(t, out, "llama-server's own", "a field wavez ships no number for says so")
}

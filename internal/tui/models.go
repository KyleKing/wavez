package tui

import (
	"fmt"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/kyleking/wavez/internal/api"
)

// modelField names one editable runtime setting, in the order the settings
// pane lists them.
type modelField int

// Settings a model is served with. Each is a llama-server flag.
const (
	fieldContext modelField = iota
	fieldSpecType
	fieldCacheReuse
	fieldThreads
	fieldBatch
	fieldCount
)

func (f modelField) label() string {
	switch f {
	case fieldContext:
		return "served context"
	case fieldSpecType:
		return "--spec-type"
	case fieldCacheReuse:
		return "--cache-reuse"
	case fieldThreads:
		return "threads"
	case fieldBatch:
		return "batch"
	default:
		return ""
	}
}

// modelAction names what a confirmation is about, empty when none is open.
type modelAction string

// Actions the screen asks a confirmation for. Neither happens without one,
// because both change what is on disk.
const (
	actionInstall modelAction = "install"
	actionRemove  modelAction = "remove"
)

func (a modelAction) kind() api.CommandKind {
	if a == actionInstall {
		return api.CmdModelInstall
	}

	return api.CmdModelRemove
}

// modelsState is the model management screen: the list the daemon sent, the
// cursor, an optional settings pane, and an optional confirmation.
type modelsState struct {
	list []api.ModelInfo
	// confirm is the note the daemon returned for a preview, which is what
	// the user confirms against.
	confirm string
	// pending names the model an open confirmation is about.
	pending string
	action  modelAction
	install textinput.Model
	edit    textinput.Model
	cursor  int
	field   modelField
	// settings reports whether the per-model settings pane is open.
	settings bool
	editing  bool
	naming   bool
}

func newModelsState(th theme) modelsState {
	return modelsState{
		install: th.newInput("model to install, for example qwen2.5-coder:7b"),
		edit:    th.newInput("value, or empty to restore the default"),
	}
}

func (m Model) selectedModel() (api.ModelInfo, bool) {
	rows := m.models.list
	if len(rows) == 0 {
		return api.ModelInfo{}, false
	}

	return rows[min(max(m.models.cursor, 0), len(rows)-1)], true
}

func (m Model) updateModelsKey(msg tea.KeyPressMsg, s string) (Model, tea.Cmd) {
	switch {
	case m.models.naming:
		return m.updateModelInstallName(msg, s)
	case m.models.action != "":
		return m.updateModelConfirm(s)
	case m.models.editing:
		return m.updateModelEdit(msg, s)
	case m.models.settings:
		return m.updateModelSettings(s)
	default:
		return m.updateModelList(s)
	}
}

func (m Model) updateModelList(s string) (Model, tea.Cmd) {
	switch s {
	case keyJ, keyDown:
		m.models.cursor = min(m.models.cursor+1, max(len(m.models.list)-1, 0))
	case keyK, keyUp:
		m.models.cursor = max(m.models.cursor-1, 0)
	case "u":
		m.status = "checking the registry"

		if m.client != nil {
			return m, m.client.modelCommand(api.Command{Kind: api.CmdModelCheck})
		}
	case "a":
		m.models.naming = true

		return m, m.models.install.Focus()
	case "x":
		return m.previewModel(actionRemove)
	case keyEnter, "e":
		if _, ok := m.selectedModel(); ok {
			m.models.settings, m.models.field = true, fieldContext
		}
	}

	return m, nil
}

// previewModel asks the daemon what the action would cost on disk. Nothing
// is installed or removed until the answer comes back and is confirmed.
func (m Model) previewModel(action modelAction) (Model, tea.Cmd) {
	info, ok := m.selectedModel()
	if !ok {
		return m, nil
	}

	return m.requestPreview(action, info.Name)
}

func (m Model) requestPreview(action modelAction, name string) (Model, tea.Cmd) {
	m.models.action, m.models.pending, m.models.confirm = action, name, ""

	if m.client == nil {
		return m, nil
	}

	return m, m.client.modelCommand(api.Command{Kind: action.kind(), Model: name})
}

func (m Model) updateModelInstallName(msg tea.KeyPressMsg, s string) (Model, tea.Cmd) {
	if s == keyEnter {
		name := strings.TrimSpace(m.models.install.Value())
		m.models.install.Reset()
		m.models.naming = false

		if name == "" {
			return m, nil
		}

		return m.requestPreview(actionInstall, name)
	}

	var cmd tea.Cmd
	m.models.install, cmd = m.models.install.Update(msg)

	return m, cmd
}

func (m Model) updateModelConfirm(s string) (Model, tea.Cmd) {
	action, name := m.models.action, m.models.pending
	// y is the answer to the disk delta, so it means nothing until the delta
	// is here. Taking it early installs or removes a model against a
	// question the user was never shown.
	if s == "y" && m.models.confirm == "" {
		return m, nil
	}

	if s != "y" {
		m.models.action, m.models.pending, m.models.confirm = "", "", ""

		return m, nil
	}

	m.models.action, m.models.pending, m.models.confirm = "", "", ""
	m.status = string(action) + "ing " + name

	if m.client == nil {
		return m, nil
	}

	return m, m.client.modelCommand(api.Command{Kind: action.kind(), Model: name, Confirm: true})
}

func (m Model) updateModelSettings(s string) (Model, tea.Cmd) {
	switch s {
	case keyJ, keyDown:
		m.models.field = (m.models.field + 1) % fieldCount
	case keyK, keyUp:
		m.models.field = (m.models.field + fieldCount - 1) % fieldCount
	case keyEnter, "e":
		m.models.editing = true

		return m, m.models.edit.Focus()
	case "0":
		return m.saveModelField("")
	}

	return m, nil
}

// closeModelsOverlay is Esc's ladder on the model screen: an open text field
// closes first, then a confirmation, then the settings pane, and only with
// none of those open does Esc leave the screen. It reports whether it closed
// anything.
func (m *Model) closeModelsOverlay() bool {
	switch {
	case m.models.naming:
		m.models.naming = false
		m.models.install.Reset()
	case m.models.editing:
		m.models.editing = false
		m.models.edit.Reset()
	case m.models.action != "":
		m.models.action, m.models.pending, m.models.confirm = "", "", ""
	case m.models.settings:
		m.models.settings = false
	default:
		return false
	}

	return true
}

func (m Model) updateModelEdit(msg tea.KeyPressMsg, s string) (Model, tea.Cmd) {
	if s == keyEnter {
		value := strings.TrimSpace(m.models.edit.Value())
		m.models.edit.Reset()
		m.models.editing = false

		return m.saveModelField(value)
	}

	var cmd tea.Cmd
	m.models.edit, cmd = m.models.edit.Update(msg)

	return m, cmd
}

// saveModelField writes one field and sends the whole settings block, since
// the daemon stores it whole. An empty value clears the field back to the
// shipped default rather than to zero.
func (m Model) saveModelField(value string) (Model, tea.Cmd) {
	info, ok := m.selectedModel()
	if !ok {
		return m, nil
	}

	settings := info.Settings
	applyModelField(&settings, m.models.field, value)

	if m.client == nil {
		return m, nil
	}

	return m, m.client.modelCommand(api.Command{
		Kind: api.CmdModelSettings, Model: info.Name, Settings: &settings,
	})
}

func applyModelField(s *api.ModelSettings, f modelField, value string) {
	n, err := strconv.Atoi(value)
	if err != nil {
		n = 0
	}

	switch f {
	case fieldContext:
		s.ContextSize = n
	case fieldSpecType:
		s.SpecType = value
	case fieldCacheReuse:
		s.CacheReuse = n
	case fieldThreads:
		s.Threads = n
	case fieldBatch:
		s.BatchSize = n
	case fieldCount:
	}
}

func modelFieldValues(s api.ModelSettings) [fieldCount]string {
	return [fieldCount]string{
		optInt(s.ContextSize), s.SpecType, optInt(s.CacheReuse), optInt(s.Threads), optInt(s.BatchSize),
	}
}

// optInt renders a setting that is unset, which means the shipped default
// applies rather than a zero.
func optInt(n int) string {
	if n == 0 {
		return ""
	}

	return strconv.Itoa(n)
}

// defaultLabel names a shipped default, calling out the one case where the
// default is llama-server's own choice rather than a number wavez measured.
func defaultLabel(value string) string {
	if value == "" {
		return "llama-server's own"
	}

	return value
}

func (m Model) renderModels() string {
	total := uint64(0)
	for i := range m.models.list {
		total += m.models.list[i].SizeBytes
	}

	title := fmt.Sprintf("models · %d installed · %s on disk", len(m.models.list), bytesGB(total))
	pane := m.modelPaneRows()
	body := append(m.modelRows(m.modelListBudget(len(pane))), pane...)

	return frame(m.width, title, body, footerHints(m.modelHints(), m.width-boxPad), m.th)
}

// modelPaneRows is whatever is open below the list: the install field, a
// confirmation, or the settings pane.
func (m Model) modelPaneRows() []string {
	switch {
	case m.models.naming:
		return []string{"", "install > " + m.models.install.View()}
	case m.models.action != "":
		return []string{"", m.th.fgEmphasis.Render(m.modelConfirmLine())}
	case m.models.settings:
		return append([]string{""}, m.modelSettingsRows()...)
	default:
		return nil
	}
}

// frameRows is the two border lines a frame spends, which carry the title
// and the key hints. A body longer than what is left pushes the hints off
// the terminal, so the list is cut to fit rather than the frame.
const frameRows = 2

// modelListBudget is how many models fit once the border, the column header,
// and any open pane have taken their lines.
func (m Model) modelListBudget(pane int) int {
	return max(m.height-frameRows-1-pane, 1)
}

func (m Model) modelConfirmLine() string {
	if m.models.confirm == "" {
		return "asking what " + string(m.models.action) + " " + m.models.pending + " would cost"
	}

	return m.models.confirm
}

const (
	modelNameWidth  = 22
	modelQuantWidth = 9
)

// modelValueWidth is the settings value column, which the edit field is
// sized to so an open editor leaves the shipped default beside it.
const modelValueWidth = 12

func (m Model) modelRows(budget int) []string {
	if len(m.models.list) == 0 {
		return []string{m.th.fgMuted.Render("no models installed · press a to install one")}
	}

	out := []string{m.th.fgMuted.Render(fmt.Sprintf("  %-22s %-9s %-7s %-8s %-8s %s",
		"model", "quant", "size", "headroom", "loaded", "update"))}

	cursor := min(max(m.models.cursor, 0), len(m.models.list)-1)
	start, end := centeredWindow(cursor, len(m.models.list), budget)

	for i := start; i < end; i++ {
		info := &m.models.list[i]

		line := fmt.Sprintf("%-22s %-9s %-7s %-8s %-8s %s",
			truncate(info.Name, modelNameWidth), truncate(orDash(info.Quant), modelQuantWidth),
			bytesGB(info.SizeBytes), bytesGB(info.FreeBytes),
			yesNo(info.Loaded), updateLabel(*info))

		if i == cursor {
			out = append(out, m.th.accent.Render("> "+line))

			continue
		}

		out = append(out, m.th.fgDefault.Render("  "+line))
	}

	if end-start < len(m.models.list) {
		out = append(out, m.th.fgMuted.Render(fmt.Sprintf("  showing %d-%d of %d",
			start+1, end, len(m.models.list))))
	}

	return out
}

// centeredWindow is the slice of a list to draw: everything when it fits, and
// otherwise a run of budget rows centered on the cursor, one row shorter to
// leave the line that says how much is hidden. It keeps no scroll position,
// unlike scrolledWindow, so the cursor sits mid-list rather than where the
// reader last left it.
func centeredWindow(cursor, n, budget int) (int, int) {
	if budget >= n {
		return 0, n
	}

	size := max(budget-1, 1)

	start := max(cursor-size/2, 0)
	if start+size > n {
		start = n - size
	}

	return start, start + size
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}

	return "no"
}

// updateLabel says what the last registry check found. Never checked is a
// dash rather than "up to date", which would be a claim nobody made.
func updateLabel(info api.ModelInfo) string {
	switch {
	case !info.Checked:
		return "-"
	case info.UpdateAvailable:
		return "a newer tag exists"
	default:
		return "current"
	}
}

func (m Model) modelSettingsRows() []string {
	info, ok := m.selectedModel()
	if !ok {
		return nil
	}

	values := modelFieldValues(info.Settings)
	defaults := modelFieldValues(info.Defaults)

	out := []string{m.th.fgEmphasis.Render("settings · " + info.Name)}

	for f := fieldContext; f < fieldCount; f++ {
		value := values[f]
		if value == "" {
			value = defaults[f]
		}

		line := fmt.Sprintf("%-16s %-12s default %s", f.label(), orDash(value), defaultLabel(defaults[f]))
		if f == m.models.field && m.models.editing {
			line = fmt.Sprintf("%-16s %s default %s", f.label(), m.models.edit.View(), defaultLabel(defaults[f]))
		}

		if f == m.models.field {
			out = append(out, m.th.accent.Render("> "+line))

			continue
		}

		out = append(out, m.th.fgDefault.Render("  "+line))
	}

	return out
}

func (m Model) modelHints() []hint {
	switch {
	case m.models.naming, m.models.editing:
		return []hint{{keyEnter, labelApply, ""}, {keyEsc, labelCancel, ""}}
	case m.models.action != "" && m.models.confirm == "":
		return []hint{{keyEsc, labelCancel, ""}}
	case m.models.action != "":
		return []hint{{"y", "confirm", ""}, {"n", labelCancel, ""}}
	case m.models.settings:
		return []hint{{keyEnter, "edit", ""}, {"0", "default", ""}, {keyEsc, labelBack, ""}}
	default:
		return []hint{
			{keyEnter, "settings", ""},
			{"u", "check", ""},
			{"a", "install", ""},
			{"x", "uninstall", ""},
			{keyEsc, labelBack, ""},
		}
	}
}

// openModels pushes the model screen and asks the daemon for the list, which
// is cheap: it reads what Ollama has on disk and contacts no registry.
func (m Model) openModels() (Model, tea.Cmd) {
	m.push(screenModels)

	if m.client == nil {
		return m, nil
	}

	return m, m.client.modelCommand(api.Command{Kind: api.CmdModels})
}

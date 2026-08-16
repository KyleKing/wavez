package main

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"daemontui/internal/proto"
)

var threadNames = []string{"a", "b", "c"}

type screen int

const (
	screenHome screen = iota
	screenThread
)

type clientReadyMsg struct{ c *client }

// Model is a flat Bubble Tea v2 model: one struct, one Update switch, no
// sub-models beyond the textinput widget.
type Model struct {
	width, height int
	screen        screen
	cursor        int
	activeIdx     int
	scrollOffset  int

	seq      map[string]int
	lastText map[string]string
	pending  map[string]bool
	events   map[string][]proto.Event

	input  textinput.Model
	status string
	client *client
}

func initialModel() Model {
	ti := textinput.New()
	ti.Placeholder = "type and press enter to send..."

	m := Model{
		seq:      map[string]int{},
		lastText: map[string]string{},
		pending:  map[string]bool{},
		events:   map[string][]proto.Event{},
		input:    ti,
	}
	for _, n := range threadNames {
		m.events[n] = nil
	}
	return m
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) activeThread() string {
	return threadNames[m.activeIdx]
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.input.SetWidth(msg.Width - 2)
		return m, nil

	case clientReadyMsg:
		m.client = msg.c
		m.status = "connected"
		return m, nil

	case connErrMsg:
		if msg.err != nil {
			m.status = fmt.Sprintf("connection error: %v", msg.err)
		} else {
			m.status = "daemon closed the connection"
		}
		return m, nil

	case batchMsg:
		m.applyBatch(msg)
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m *Model) applyBatch(events []proto.Event) {
	for _, e := range events {
		m.events[e.Thread] = append(m.events[e.Thread], e)
		m.seq[e.Thread] = e.Seq
		m.lastText[e.Thread] = e.Text
		switch {
		case e.Kind == proto.KindPermission:
			m.pending[e.Thread] = true
		case strings.HasPrefix(e.Text, "permission answered") || strings.HasPrefix(e.Text, "permission timed out"):
			m.pending[e.Thread] = false
		}
	}
}

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	s := msg.String()
	if s == "ctrl+c" {
		return m, tea.Quit
	}

	if m.screen == screenHome {
		return m.handleHomeKey(s)
	}
	return m.handleThreadKey(msg, s)
}

func (m Model) handleHomeKey(s string) (tea.Model, tea.Cmd) {
	switch s {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(threadNames)-1 {
			m.cursor++
		}
	case "[":
		m.cursor = (m.cursor - 1 + len(threadNames)) % len(threadNames)
	case "]":
		m.cursor = (m.cursor + 1) % len(threadNames)
	case "enter":
		m.screen = screenThread
		m.activeIdx = m.cursor
		m.scrollOffset = 0
		return m, m.input.Focus()
	case "y", "n":
		m.answerThread(threadNames[m.cursor], s)
	}
	return m, nil
}

func (m *Model) answerThread(name, v string) {
	if m.pending[name] && m.client != nil {
		m.client.sendCmd(proto.Command{Cmd: "answer", Thread: name, Value: v})
	}
}

func (m Model) handleThreadKey(msg tea.KeyPressMsg, s string) (tea.Model, tea.Cmd) {
	switch s {
	case "esc":
		m.screen = screenHome
		m.input.Blur()
		m.input.Reset()
		return m, nil
	case "[":
		m.activeIdx = (m.activeIdx - 1 + len(threadNames)) % len(threadNames)
		m.scrollOffset = 0
		return m, nil
	case "]":
		m.activeIdx = (m.activeIdx + 1) % len(threadNames)
		m.scrollOffset = 0
		return m, nil
	case "up":
		total := len(m.events[m.activeThread()])
		if m.scrollOffset < total {
			m.scrollOffset++
		}
		return m, nil
	case "down":
		if m.scrollOffset > 0 {
			m.scrollOffset--
		}
		return m, nil
	case "y", "n":
		if m.input.Value() == "" {
			m.answerThread(m.activeThread(), s)
			return m, nil
		}
	case "enter":
		if text := strings.TrimSpace(m.input.Value()); text != "" {
			if m.client != nil {
				m.client.sendCmd(proto.Command{Cmd: "send", Thread: m.activeThread(), Text: text})
			}
			m.input.Reset()
		}
		return m, nil
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

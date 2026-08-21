package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/kyleking/wavez/internal/router"
)

// Palette verbs that pin the active thread's routing tier, or clear the pin.
// The unpinned state, shared by the routing tier and the reasoning trace.
const labelAuto = "auto"

const (
	verbRouteAuto     = "route auto"
	verbRouteBalanced = "route balanced"
	verbRouteDeep     = "route deep"
	verbRouteFast     = "route fast"
)

// routeCycle is the order `m` walks: automatic, then each tier, then back to
// automatic, so a pin is never more than three presses from being cleared.
var routeCycle = []router.Choice{"", router.ChoiceFast, router.ChoiceBalanced, router.ChoiceDeep}

func nextRoute(current router.Choice) router.Choice {
	for i, c := range routeCycle {
		if c == current {
			return routeCycle[(i+1)%len(routeCycle)]
		}
	}

	return ""
}

// routeLabel names a tier for the header and the status line.
func routeLabel(c router.Choice) string {
	switch c {
	case router.ChoiceFast:
		return "fast"
	case router.ChoiceBalanced:
		return "balanced"
	case router.ChoiceDeep:
		return "deep"
	default:
		return "auto"
	}
}

// Palette verbs that turn the active thread's reasoning trace on or off.
const (
	verbThinkOn   = "think on"
	verbThinkOff  = "think off"
	verbThinkAuto = "think auto"
)

// thinkCycle is the order `t` walks: the served model's default, then off,
// then on, then back. Off comes first because it is the setting that pays:
// measured on qwen3:8b, replying "OK" costs 79 completion tokens with the
// trace on and 2 with it off, and decode is the local bottleneck.
func nextThinking(current *bool) *bool {
	off, on := false, true

	switch {
	case current == nil:
		return &off
	case !*current:
		return &on
	default:
		return nil
	}
}

func thinkingLabel(t *bool) string {
	switch {
	case t == nil:
		return labelAuto
	case *t:
		return "on"
	default:
		return "off"
	}
}

func (m Model) cycleThinking() (Model, tea.Cmd) {
	info, ok := m.activeThread()
	if !ok {
		m.status = msgNoThread

		return m, nil
	}

	return m.setThinking(nextThinking(info.Thinking))
}

// setThinking pins the active thread's reasoning trace. The daemon applies
// it from the next turn, not to one already running, so the status says so.
func (m Model) setThinking(thinking *bool) (Model, tea.Cmd) {
	info, ok := m.activeThread()
	if !ok {
		m.status = msgNoThread

		return m, nil
	}

	m.status = info.Name + " thinking " + thinkingLabel(thinking) + " from the next turn"

	if m.client == nil {
		return m, nil
	}

	return m, m.client.think(info.ID, thinking)
}

func (m Model) cycleRoute() (Model, tea.Cmd) {
	info, ok := m.activeThread()
	if !ok {
		m.status = msgNoThread

		return m, nil
	}

	return m.setRoute(nextRoute(info.Override))
}

// setRoute pins the active thread to override, or clears the pin when it is
// empty. The status says so immediately because the daemon applies the pin
// from the next turn, not to one already running.
func (m Model) setRoute(override router.Choice) (Model, tea.Cmd) {
	info, ok := m.activeThread()
	if !ok {
		m.status = msgNoThread

		return m, nil
	}

	if override == "" {
		m.status = info.Name + " routes automatically from the next turn"
	} else {
		m.status = info.Name + " is pinned to " + routeLabel(override) + " from the next turn"
	}

	if m.client == nil {
		return m, nil
	}

	return m, m.client.route(info.ID, override)
}

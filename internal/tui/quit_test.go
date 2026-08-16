package tui_test

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/kyleking/wavez/internal/api"
	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/tui"
)

func quitTestThreads() []api.ThreadInfo {
	return []api.ThreadInfo{{ID: "t1", Name: "fix-lock-timeout", Dir: "calcipy", State: event.StateWorking}}
}

const (
	quitWait  = 2 * time.Second
	shortPoll = 300 * time.Millisecond
)

func TestQuit_QAtHomeQuits(t *testing.T) {
	t.Parallel()

	tm := teatest.NewTestModel(t, tui.New(tui.Options{}), teatest.WithInitialTermSize(80, 24))
	tm.Send(tea.KeyPressMsg{Code: 'q', Text: "q"})
	tm.WaitFinished(t, teatest.WithFinalTimeout(quitWait))
}

func TestQuit_CtrlCAtHomeQuits(t *testing.T) {
	t.Parallel()

	tm := teatest.NewTestModel(t, tui.New(tui.Options{}), teatest.WithInitialTermSize(80, 24))
	tm.Send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	tm.WaitFinished(t, teatest.WithFinalTimeout(quitWait))
}

func TestQuit_CtrlCInThreadViewQuits(t *testing.T) {
	t.Parallel()

	tm := teatest.NewTestModel(t, tui.New(tui.Options{}), teatest.WithInitialTermSize(80, 24))
	tm.Send(api.Reply{Kind: api.RepThreads, Threads: quitTestThreads()})
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	tm.Send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	tm.WaitFinished(t, teatest.WithFinalTimeout(quitWait))
}

func TestQuit_EscNeverQuits(t *testing.T) {
	t.Parallel()

	tm := teatest.NewTestModel(t, tui.New(tui.Options{}), teatest.WithInitialTermSize(80, 24))
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEscape})

	timedOut := false
	tm.WaitFinished(t,
		teatest.WithFinalTimeout(shortPoll),
		teatest.WithTimeoutFn(func(testing.TB) { timedOut = true }),
	)

	if !timedOut {
		t.Fatal("esc quit the program; it must only ever go back one level")
	}
}

func TestQuit_QOutsideHomeDoesNotQuit(t *testing.T) {
	t.Parallel()

	tm := teatest.NewTestModel(t, tui.New(tui.Options{}), teatest.WithInitialTermSize(80, 24))
	tm.Send(api.Reply{Kind: api.RepThreads, Threads: quitTestThreads()})
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	tm.Send(tea.KeyPressMsg{Code: 'q', Text: "q"})

	timedOut := false
	tm.WaitFinished(t,
		teatest.WithFinalTimeout(shortPoll),
		teatest.WithTimeoutFn(func(testing.TB) { timedOut = true }),
	)

	if !timedOut {
		t.Fatal("q quit the program outside Home; it must be a no-op there")
	}
}

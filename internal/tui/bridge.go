package tui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/kyleking/wavez/internal/api"
	"github.com/kyleking/wavez/internal/permission"
	"github.com/kyleking/wavez/internal/router"
)

// daemonClient is the subset of api.Client the model drives commands
// through. It is satisfied by *bridge in production and left nil in tests,
// which feed the model api.Reply values directly instead of talking to a
// daemon.
type daemonClient interface {
	subscribe(threadID string) tea.Cmd
	send(threadID, text string) tea.Cmd
	answer(promptID, text string, decision permission.Decision) tea.Cmd
	diff(threadID string) tea.Cmd
	cancel(threadID string) tea.Cmd
	schedule() tea.Cmd
	restore(threadID string, confirm bool) tea.Cmd
	route(threadID string, override router.Choice) tea.Cmd
	think(threadID string, thinking *bool) tea.Cmd
	newThread(prompt, model, parent, cycle string, dirs []string) tea.Cmd
	routines() tea.Cmd
	runRoutine(name string) tea.Cmd
}

const flushInterval = 16 * time.Millisecond

// bridge adapts an api.Client to daemonClient and forwards its pushed
// replies into a running tea.Program, coalescing bursts on a fixed tick so a
// stream of token events costs one redraw instead of one per event.
type bridge struct {
	client *api.Client
	prog   *tea.Program
}

// newBridge starts forwarding c's pushed replies into prog and returns a
// daemonClient the model can issue commands through. The caller keeps
// ownership of c and closes it.
func newBridge(ctx context.Context, c *api.Client, prog *tea.Program) *bridge {
	b := &bridge{client: c, prog: prog}
	go b.forward(ctx)

	return b
}

func (b *bridge) forward(ctx context.Context) {
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()

	var buf []api.Reply

	for {
		select {
		case <-ctx.Done():
			return
		case r, ok := <-b.client.Events():
			if !ok {
				b.prog.Send(connErrMsg{})

				return
			}

			buf = append(buf, r)
		case <-ticker.C:
			if len(buf) == 0 {
				continue
			}

			b.prog.Send(batchMsg(buf))
			buf = nil
		}
	}
}

func (b *bridge) subscribe(threadID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
		defer cancel()

		reply, err := b.client.Do(ctx, api.Command{Kind: api.CmdSubscribe, ThreadID: threadID})
		if err != nil {
			return connErrMsg{err: err}
		}

		return reply
	}
}

func (b *bridge) send(threadID, text string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
		defer cancel()

		reply, err := b.client.Do(ctx, api.Command{Kind: api.CmdSend, ThreadID: threadID, Prompt: text})
		if err != nil {
			return connErrMsg{err: err}
		}

		return reply
	}
}

func (b *bridge) answer(promptID, text string, decision permission.Decision) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
		defer cancel()

		cmd := api.Command{Kind: api.CmdAnswer, PromptID: promptID, Answer: text, Decision: decision}

		reply, err := b.client.Do(ctx, cmd)
		if err != nil {
			return connErrMsg{err: err}
		}

		return reply
	}
}

func (b *bridge) cancel(threadID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
		defer cancel()

		reply, err := b.client.Do(ctx, api.Command{Kind: api.CmdCancel, ThreadID: threadID})
		if err != nil {
			return connErrMsg{err: err}
		}

		return reply
	}
}

func (b *bridge) schedule() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
		defer cancel()

		reply, err := b.client.Do(ctx, api.Command{Kind: api.CmdSchedule})
		if err != nil {
			return connErrMsg{err: err}
		}

		return reply
	}
}

func (b *bridge) diff(threadID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
		defer cancel()

		reply, err := b.client.Do(ctx, api.Command{Kind: api.CmdDiff, ThreadID: threadID})
		if err != nil {
			return connErrMsg{err: err}
		}

		return reply
	}
}

// restore previews an undo of threadID's checkpoint, or performs it when
// confirm is set. A daemon refusal comes back as restoreErrMsg rather than
// connErrMsg: the connection is fine, the undo is what failed.
func (b *bridge) restore(threadID string, confirm bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
		defer cancel()

		reply, err := b.client.Do(ctx, api.Command{Kind: api.CmdRestore, ThreadID: threadID, Confirm: confirm})
		if err != nil {
			return restoreErrMsg{err: err}
		}

		return reply
	}
}

func (b *bridge) think(threadID string, thinking *bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
		defer cancel()

		reply, err := b.client.Do(ctx, api.Command{Kind: api.CmdThink, ThreadID: threadID, Thinking: thinking})
		if err != nil {
			return connErrMsg{err: err}
		}

		return reply
	}
}

func (b *bridge) route(threadID string, override router.Choice) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
		defer cancel()

		reply, err := b.client.Do(ctx, api.Command{Kind: api.CmdRoute, ThreadID: threadID, Override: override})
		if err != nil {
			return connErrMsg{err: err}
		}

		return reply
	}
}

func (b *bridge) newThread(prompt, model, parent, cycle string, dirs []string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
		defer cancel()

		cmd := api.Command{
			Kind: api.CmdNew, Prompt: prompt, Model: model, Parent: parent, Cycle: cycle, Dirs: dirs,
		}

		reply, err := b.client.Do(ctx, cmd)
		if err != nil {
			return connErrMsg{err: err}
		}

		return reply
	}
}

func (b *bridge) routines() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
		defer cancel()

		reply, err := b.client.Do(ctx, api.Command{Kind: api.CmdRoutines})
		if err != nil {
			return connErrMsg{err: err}
		}

		return reply
	}
}

// runRoutine has no command timeout: a routine is a build or a test suite,
// and cutting the reply off after five seconds would leave the panel
// reporting a failure the routine never had.
func (b *bridge) runRoutine(name string) tea.Cmd {
	return func() tea.Msg {
		reply, err := b.client.Do(context.Background(), api.Command{Kind: api.CmdRunRoutine, Routine: name})
		if err != nil {
			return connErrMsg{err: err}
		}

		return reply
	}
}

const commandTimeout = 5 * time.Second

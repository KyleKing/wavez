package tui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/kyleking/wavez/internal/api"
	"github.com/kyleking/wavez/internal/permission"
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
	newThread(prompt, model, parent string, dirs []string) tea.Cmd
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

func (b *bridge) newThread(prompt, model, parent string, dirs []string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
		defer cancel()

		cmd := api.Command{Kind: api.CmdNew, Prompt: prompt, Model: model, Parent: parent, Dirs: dirs}

		reply, err := b.client.Do(ctx, cmd)
		if err != nil {
			return connErrMsg{err: err}
		}

		return reply
	}
}

const commandTimeout = 5 * time.Second

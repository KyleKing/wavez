package tui

import (
	"context"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/kyleking/wavez/internal/api"
)

// pollInterval refreshes Home's thread list and the diagnostics strip. The
// daemon pushes per-thread events to subscribers and pending prompts to
// every connection on its own, but a fleet-wide ThreadInfo refresh (step,
// age, spend for threads the user is not currently subscribed to) has no
// push equivalent in the protocol, so Home polls for it.
const pollInterval = 2 * time.Second

// clientReadyMsg installs the daemon-backed client once dialing and the
// handshake in Dial have already completed, since the bridge needs a
// running tea.Program to forward into and the program needs a model before
// it can run.
type clientReadyMsg struct{ c daemonClient }

// Run drives the TUI against an already-dialed client until the program
// exits (a quit key) or ctx is canceled. It never calls the agent loop
// directly; every state change comes from client.
func Run(ctx context.Context, client *api.Client, opts Options) error {
	prog := tea.NewProgram(New(opts))

	go connect(ctx, client, prog)

	if _, err := prog.Run(); err != nil {
		return fmt.Errorf("running tui: %w", err)
	}

	return nil
}

func connect(ctx context.Context, client *api.Client, prog *tea.Program) {
	b := newBridge(ctx, client, prog)
	prog.Send(clientReadyMsg{c: b})

	refresh(ctx, client, b, prog)

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			refresh(ctx, client, b, prog)
		}
	}
}

// refresh polls the fleet-wide readings the daemon has no push equivalent
// for. The list request honors b's current scope, since a `w` toggle that
// won a race against this poll would otherwise be undone by it within
// pollInterval.
func refresh(ctx context.Context, client *api.Client, b *bridge, prog *tea.Program) {
	reqCtx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()

	if reply, err := client.Do(reqCtx, api.Command{Kind: api.CmdList, AllRoots: b.fleet.Load()}); err == nil {
		prog.Send(reply)
	}
	if reply, err := client.Do(reqCtx, api.Command{Kind: api.CmdDiag}); err == nil {
		prog.Send(reply)
	}
}

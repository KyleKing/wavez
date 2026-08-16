// Command wavez is a spike TUI client: it renders threads streamed from
// wavezd over a unix socket using a flat Bubble Tea v2 model.
package main

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
)

func main() {
	p := tea.NewProgram(initialModel())

	go func() {
		cl, err := newClient(p)
		if err != nil {
			p.Send(connErrMsg{err: err})
			return
		}
		p.Send(clientReadyMsg{c: cl})
	}()

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error running program: %v\n", err)
		os.Exit(1)
	}
}

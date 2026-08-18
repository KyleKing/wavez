package daemon

import (
	"context"

	"github.com/kyleking/wavez/internal/api"
)

// RoutineSource is the project's routines as the daemon needs them: a list
// for the panel and a way to run one by name. A Server without one answers
// both commands with an error rather than an empty list, since a client
// cannot tell a project with no routines from a daemon that cannot reach
// them.
type RoutineSource interface {
	List() ([]api.RoutineInfo, error)
	Run(ctx context.Context, name string) (api.RoutineInfo, error)
}

// WithRoutines sets the source the routines panel reads and runs through.
func WithRoutines(r RoutineSource) Option {
	return func(c *config) { c.routines = r }
}

func (c *conn) handleRoutines(cmd api.Command) {
	p, err := c.srv.resolveProject(c.ctx, cmd.Root)
	if err != nil {
		c.reply(cmd.ID, errorReply(err.Error()))

		return
	}
	if p.routines == nil {
		c.reply(cmd.ID, errorReply("this project has no routines configured"))

		return
	}

	infos, err := p.routines.List()
	if err != nil {
		c.reply(cmd.ID, errorReply(err.Error()))

		return
	}

	c.reply(cmd.ID, api.Reply{Kind: api.RepRoutines, Routines: infos})
}

// handleRunRoutine runs one routine and answers with its refreshed row. A
// routine run is bounded by the connection's context rather than the
// command's, so a client that disconnects mid-run stops it instead of
// leaving a build running with nobody to read it.
func (c *conn) handleRunRoutine(cmd api.Command) {
	p, err := c.srv.resolveProject(c.ctx, cmd.Root)
	if err != nil {
		c.reply(cmd.ID, errorReply(err.Error()))

		return
	}
	if p.routines == nil {
		c.reply(cmd.ID, errorReply("this project has no routines configured"))

		return
	}

	info, err := p.routines.Run(c.ctx, cmd.Routine)
	if err != nil {
		c.reply(cmd.ID, errorReply(err.Error()))

		return
	}

	c.reply(cmd.ID, api.Reply{Kind: api.RepRoutines, Routines: []api.RoutineInfo{info}})
}

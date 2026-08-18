package config

import (
	"time"

	"github.com/kyleking/wavez/internal/routine"
)

// pklStep and pklRoutine are the wire shapes EvaluateModule decodes the
// routine half of ".wavez.pkl" into. Field names mirror pkl/Wavez.pkl
// exactly.
type pklStep struct {
	Params  map[string]any `pkl:"params"`
	Name    string         `pkl:"name"`
	Action  string         `pkl:"action"`
	Parents []string       `pkl:"parents"`
}

type pklRoutine struct {
	ConcurrencyKey  string    `pkl:"concurrencyKey"`
	Concurrency     string    `pkl:"concurrency"`
	Triggers        []string  `pkl:"triggers"`
	Paths           []string  `pkl:"paths"`
	Steps           []pklStep `pkl:"steps"`
	IntervalSeconds int       `pkl:"intervalSeconds"`
	Enabled         bool      `pkl:"enabled"`
}

func routineDefinitions(raw map[string]pklRoutine) map[string]routine.Definition {
	if len(raw) == 0 {
		return nil
	}

	out := make(map[string]routine.Definition, len(raw))

	for name, r := range raw {
		triggers := make([]routine.Trigger, 0, len(r.Triggers))
		for _, t := range r.Triggers {
			triggers = append(triggers, routine.Trigger(t))
		}

		steps := make([]routine.StepDef, 0, len(r.Steps))
		for _, s := range r.Steps {
			steps = append(steps, routine.StepDef{
				Name: s.Name, Action: s.Action, Params: s.Params, Parents: s.Parents,
			})
		}

		out[name] = routine.Definition{
			Name:           name,
			Triggers:       triggers,
			Paths:          r.Paths,
			Steps:          steps,
			ConcurrencyKey: r.ConcurrencyKey,
			Concurrency:    routine.Concurrency(r.Concurrency),
			Interval:       time.Duration(r.IntervalSeconds) * time.Second,
			Enabled:        r.Enabled,
		}
	}

	return out
}

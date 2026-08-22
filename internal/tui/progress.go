package tui

import (
	"fmt"
	"time"

	"github.com/kyleking/wavez/internal/api"
	"github.com/kyleking/wavez/internal/event"
)

// progressLine says how long the turn in flight has been going, against
// what this run's turns have cost so far.
//
// It deliberately does not estimate what the run has left. Scored over 108
// runs of this project's own thread logs, the best remaining-run estimator
// landed within a factor of two 23% of the time, and reading the project's
// history rather than the run's own did not improve it, so a countdown
// would be wrong twice for every time it was right. The same corpus put the
// next turn at 54% within a factor of two from the run's mean gap alone
// (`_ai_/demos/progress-estimate`), which is what this renders.
//
// It is empty for a thread that is not working, because a turn that has
// ended has no duration left to report.
func progressLine(info api.ThreadInfo, now time.Time) string {
	if info.State != event.StateWorking || info.Turn <= 0 || info.TurnStart.IsZero() {
		return ""
	}

	elapsed := now.Sub(info.TurnStart)
	if elapsed < 0 {
		elapsed = 0
	}

	line := fmt.Sprintf("turn %d · %s", info.Turn, shortDuration(elapsed))
	if info.TurnMean > 0 {
		line += " of ~" + shortDuration(info.TurnMean)
	}

	return line
}

// shortDuration renders a duration for a status line: seconds under a
// minute, then minutes and seconds.
func shortDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}

	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%secondsPerMinute)
}

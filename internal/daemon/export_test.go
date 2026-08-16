package daemon

import "github.com/kyleking/wavez/internal/event"

// StepTextForTest exposes stepText to the black-box test package.
func StepTextForTest(ev event.Event) string { return stepText(ev) }

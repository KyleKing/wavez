package weak

import "testing"

func TestPassingRuns(t *testing.T) {
	// Executes the line without checking the boundary it decides.
	_ = Passing(100)
}

// Package calc is a fixture tree recorded through codegraph.
package calc

// Adder accumulates a running total.
type Adder struct {
	Total int
}

// Add folds n into the running total.
func (a *Adder) Add(n int) int {
	a.Total = scale(n) + a.Total

	return a.Total
}

func scale(n int) int {
	return n * 2
}

package calc

// Factor scales every Add.
const Factor = 2

// Run adds every value in ns and reports the total.
func Run(ns []int) int {
	a := &Adder{}
	for _, n := range ns {
		a.Add(n * Factor)
	}

	return a.Total
}

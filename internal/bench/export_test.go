package bench

// PValueRankSum exposes the rank-sum test to the black-box tests.
func PValueRankSum(a, b []float64) float64 { return rankSumP(a, b) }

// PValueSignFlip exposes the paired sign-flip test to the black-box tests.
func PValueSignFlip(values []float64) float64 { return signFlipP(values) }

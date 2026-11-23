package bench_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kyleking/wavez/internal/bench"
)

// seq is the whole numbers from first through last.
func seq(first, last int) []float64 {
	out := make([]float64, 0, last-first+1)
	for v := first; v <= last; v++ {
		out = append(out, float64(v))
	}

	return out
}

// repeated is value n times.
func repeated(value float64, n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = value
	}

	return out
}

func TestRankSumP(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		a    []float64
		b    []float64
		want float64
	}{
		{name: "five against five with no overlap is as small as p gets", a: seq(1, 5), b: seq(6, 10), want: 2.0 / 252},
		{name: "the order of the arms does not matter", a: seq(6, 10), b: seq(1, 5), want: 2.0 / 252},
		{
			name: "one value out of place", a: []float64{1, 2, 3, 4, 6}, b: []float64{5, 7, 8, 9, 10},
			want: 4.0 / 252,
		},
		{
			name: "a quarter fewer turns on tight runs is detectable",
			a:    []float64{20, 22, 19, 21, 23}, b: []float64{15, 16, 14, 17, 16}, want: 2.0 / 252,
		},
		{
			name: "samples that alternate", a: []float64{1, 3, 5, 7, 9}, b: []float64{2, 4, 6, 8, 10},
			want: 0.6904761904761905,
		},
		{name: "ties share a rank", a: []float64{1, 1, 2}, b: []float64{2, 3, 3}, want: 0.2},
		{
			name: "unequal sizes", a: []float64{10, 12, 9, 14, 11, 13}, b: []float64{9, 11, 10, 12, 8},
			want: 0.23809523809523808,
		},
		{name: "every value tied", a: repeated(3, 5), b: repeated(3, 5), want: 1},
		{name: "an empty sample has no test", a: nil, b: seq(1, 5), want: 1},
		{
			name: "more than twenty values use the normal approximation",
			a:    seq(1, 11), b: seq(12, 22), want: 8.151536127743262e-05,
		},
		{
			name: "the approximation corrects its variance for ties",
			a:    append(repeated(1, 6), repeated(2, 6)...), b: append(repeated(2, 6), repeated(3, 6)...),
			want: 0.0007800870786341557,
		},
		{name: "every value tied above the exact limit", a: repeated(3, 12), b: repeated(3, 12), want: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.InDelta(t, tt.want, bench.PValueRankSum(tt.a, tt.b), 1e-12)
		})
	}
}

func TestSignFlipP(t *testing.T) {
	t.Parallel()

	many := make([]float64, 0, 30)
	for i := range 30 {
		many = append(many, -0.1-float64(i)/100)
	}

	mixed := make([]float64, 0, 30)
	for i := range 30 {
		mixed = append(mixed, float64(i%2*2-1)*(0.1+float64(i)/100))
	}

	tests := []struct {
		name   string
		values []float64
		want   float64
		delta  float64
	}{
		{name: "six tasks all moving one way is the smallest p there is", values: repeated(-0.2, 6), want: 2.0 / 64},
		{
			name: "one task moving the other way", values: []float64{-0.2, -0.3, -0.1, -0.25, -0.15, 0.05},
			want: 0.0625,
		},
		{
			name: "tasks moving both ways", values: []float64{-0.2, 0.3, -0.1, 0.25, -0.15, 0.05, 0.1},
			want: 0.703125,
		},
		{name: "no difference on any task", values: repeated(0, 6), want: 1},
		{name: "no tasks", values: nil, want: 1},
		{
			name: "more than twenty tasks all moving one way are sampled", values: many,
			want: 1.0 / 100_001,
		},
		{name: "more than twenty tasks moving both ways are sampled", values: mixed, want: 0.85, delta: 0.1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := bench.PValueSignFlip(tt.values)
			assert.InDelta(t, tt.want, got, max(tt.delta, 1e-12))
			assert.InDelta(t, got, bench.PValueSignFlip(tt.values), 0, "the same values always give the same p")
		})
	}
}

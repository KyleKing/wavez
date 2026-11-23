package bench_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/bench"
)

func TestSchedule(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		arms []bench.Arm
		reps int
	}{
		{name: "one arm", arms: []bench.Arm{bench.ArmBaseline}, reps: 3},
		{name: "two arms", arms: []bench.Arm{bench.ArmBaseline, bench.ArmWavez}, reps: 40},
		{name: "every arm", arms: bench.Arms(), reps: 40},
		{name: "no repetitions", arms: bench.Arms(), reps: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := bench.Schedule(tt.arms, tt.reps, 7)
			require.Len(t, got, tt.reps)
			assert.Equal(t, got, bench.Schedule(tt.arms, tt.reps, 7), "a seed gives its schedule again")

			first := map[bench.Arm]int{}

			for _, order := range got {
				assert.ElementsMatch(t, tt.arms, order, "a repetition runs every arm once")

				first[order[0]]++
			}

			if tt.reps >= 40 {
				assert.Len(t, first, len(tt.arms), "no arm always goes first")
				assert.NotEqual(t, got, bench.Schedule(tt.arms, tt.reps, 8), "another seed gives another schedule")
			}
		})
	}
}

func TestSchedule_LeavesItsArgumentAlone(t *testing.T) {
	t.Parallel()

	arms := bench.Arms()
	bench.Schedule(arms, 20, 3)
	assert.Equal(t, bench.Arms(), arms)
}

func TestNewSeed(t *testing.T) {
	t.Parallel()

	seed, err := bench.NewSeed()
	require.NoError(t, err)
	assert.NotZero(t, seed)
	assert.Less(t, seed, uint64(1)<<32, "a seed survives a reader that keeps numbers as floats")
}

func TestBinaryDigest(t *testing.T) {
	t.Parallel()

	first := script(t, "wavez", "echo one\n")
	same := script(t, "wavez", "echo one\n")
	other := script(t, "wavez", "echo two\n")

	tests := []struct {
		name     string
		path     string
		wantSame bool
		wantErr  bool
	}{
		{name: "the same content elsewhere is the same build", path: same, wantSame: true},
		{name: "other content is another build", path: other},
		{name: "a binary that is not there", path: filepath.Join(t.TempDir(), "absent"), wantErr: true},
		{name: "a directory", path: t.TempDir(), wantErr: true},
	}

	want, err := bench.BinaryDigest(first)
	require.NoError(t, err)
	assert.Regexp(t, `^[0-9a-f]{12}$`, want)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := bench.BinaryDigest(tt.path)
			if tt.wantErr {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantSame, got == want)
		})
	}
}

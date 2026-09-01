package runtime_test

import (
	"testing"

	"github.com/kyleking/wavez/internal/runtime"
	"github.com/kyleking/wavez/internal/sysinfo"
)

func gib(n uint64) uint64 { return n << 30 }

func TestDeriveCacheRAMMiB(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		in     runtime.CacheRAMInput
		want   int
	}{
		{
			name: "plenty of headroom",
			in: runtime.CacheRAMInput{
				Mem:        sysinfo.Memory{TotalBytes: gib(16), UsedBytes: gib(6)},
				MemRead:    true,
				ModelBytes: gib(6),
				Slots:      1,
			},
			// 16-6=10 free, -6 model = 4 GiB headroom, /2 = 2 GiB, 1 slot.
			want:   2048,
			source: "derived from admission headroom",
		},
		{
			name: "no headroom floors at the minimum",
			in: runtime.CacheRAMInput{
				Mem:        sysinfo.Memory{TotalBytes: gib(16), UsedBytes: gib(13)},
				MemRead:    true,
				ModelBytes: gib(6),
				Slots:      1,
			},
			want:   64,
			source: "derived from admission headroom",
		},
		{
			// Splitting across slots is the whole of "admission headroom":
			// four slots each get a quarter of what one slot would.
			name: "the headroom is split across the admitted slots",
			in: runtime.CacheRAMInput{
				Mem:        sysinfo.Memory{TotalBytes: gib(16), UsedBytes: gib(6)},
				MemRead:    true,
				ModelBytes: gib(6),
				Slots:      4,
			},
			want:   512,
			source: "derived from admission headroom",
		},
		{
			name:   "memory unreadable falls back to the default",
			in:     runtime.CacheRAMInput{MemRead: false, Slots: 1},
			want:   runtime.DefaultCacheRAMMiB,
			source: "memory unreadable, using default",
		},
		{
			name: "explicit override wins",
			in: runtime.CacheRAMInput{
				OverrideMiB: 128,
				Mem:         sysinfo.Memory{TotalBytes: gib(16), UsedBytes: gib(6)},
				MemRead:     true,
				ModelBytes:  gib(6),
				Slots:       1,
			},
			want:   128,
			source: "config override",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, source := runtime.DeriveCacheRAMMiB(tt.in)
			if got != tt.want {
				t.Errorf("DeriveCacheRAMMiB() = %d MiB, want %d", got, tt.want)
			}

			if source != tt.source {
				t.Errorf("source = %q, want %q", source, tt.source)
			}
		})
	}
}

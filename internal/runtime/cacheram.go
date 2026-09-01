package runtime

import (
	"context"
	"os"

	"github.com/kyleking/wavez/internal/sysinfo"
)

// cacheRAMShare is the fraction of admission headroom the prompt cache may
// take. The other half stays for decode activations and the gate runs the
// admission scheduler is protecting; a cache that eats all headroom just
// moves the OOM from llama-server to the next `go test`.
const cacheRAMShare = 2

// bytesPerMiB converts a byte count to MiB.
const bytesPerMiB = 1 << 20

// minCacheRAMMiB is the smallest cache worth llama-server's bookkeeping for.
// Below it the process still runs, but every prefix is recomputed.
const minCacheRAMMiB = 64

// CacheRAMInput is one start's admission headroom: what the host has free,
// what the model and its KV slots already claim, and how many slots the
// scheduler admits against them.
type CacheRAMInput struct {
	// OverrideMiB is Config.CacheRAMMiB; zero means derive.
	OverrideMiB int
	// Mem is the memory snapshot; valid only when MemRead is true.
	Mem sysinfo.Memory
	// MemRead reports whether Mem came from a successful read.
	MemRead bool
	// ModelBytes is the model's share of RAM (the GGUF's size on disk).
	ModelBytes uint64
	// Slots is how many slots the scheduler admits.
	Slots int
}

// DeriveCacheRAMMiB sizes --cache-ram from the admission headroom. An
// explicit override wins; a machine whose memory could not be read falls
// back to DefaultCacheRAMMiB; otherwise the cache gets half of the RAM left
// after the model, split evenly across the admitted slots, never below
// minCacheRAMMiB. The returned source names which branch decided, for the
// start-up log line.
func DeriveCacheRAMMiB(in CacheRAMInput) (int, string) {
	if in.OverrideMiB > 0 {
		return in.OverrideMiB, "config override"
	}

	if !in.MemRead {
		return DefaultCacheRAMMiB, "memory unreadable, using default"
	}

	slots := max(in.Slots, 1)

	headroom := in.Mem.Free()
	if headroom > in.ModelBytes {
		headroom -= in.ModelBytes
	} else {
		headroom = 0
	}

	mib := minCacheRAMMiB
	if perSlot := headroom / cacheRAMShare / uint64(slots) / bytesPerMiB; perSlot >= maxInt32 {
		// perSlot this large is already absurd for a cache; clamp rather
		// than overflow on a host whose total RAM exceeds int32 MiB.
		mib = maxInt32
	} else if perSlot >= minCacheRAMMiB {
		mib = int(perSlot)
	}

	return mib, "derived from admission headroom"
}

// maxInt32 caps a derived cache size so the uint64-to-int conversion cannot
// overflow; no real cache decision needs more than 2 million GiB.
const maxInt32 = 1<<31 - 1

// modelRAMBytes reports the model's share of host RAM as its GGUF's size on
// disk: the blob is what gets mapped into Metal, and its size is the number
// admission arithmetic already reasons about. A path that cannot be read
// contributes nothing, leaving the cache sized from free RAM alone.
func modelRAMBytes(ggufPath string) uint64 {
	st, err := os.Stat(ggufPath)
	if err != nil {
		return 0
	}

	return uint64(max(st.Size(), 0))
}

// readMemoryOnce wraps a memory reader for Supervisor, which reads memory
// once per start rather than polling. A read that fails or reports no total
// counts as unreadable so deriveCacheRAMMiB falls back to its default.
func readMemoryOnce(ctx context.Context, read func(context.Context) (sysinfo.Memory, error)) (sysinfo.Memory, bool) {
	mem, err := read(ctx)
	if err != nil || mem.TotalBytes == 0 {
		return sysinfo.Memory{}, false
	}

	return mem, true
}

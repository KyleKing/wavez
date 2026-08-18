package sysinfo

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

const (
	kib = 1 << 10
	mib = 1 << 20
	gib = 1 << 30
)

// ReadFootprint reports one process's physical footprint, which is what a
// GGUF mapped into Metal actually occupies. RSS misses it: a llama-server
// serving a 5 GB model shows 16 MB resident, since the mapped pages are
// charged to the footprint and not to the resident set.
func ReadFootprint(ctx context.Context, pid int) (uint64, error) {
	//nolint:gosec // pid is an integer the caller read from ps, never text from outside the process
	out, err := exec.CommandContext(ctx, "top", "-l", "1", "-pid", strconv.Itoa(pid), "-stats", "mem").Output()
	if err != nil {
		return 0, fmt.Errorf("running top for %d: %w", pid, err)
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")

	return parseTopMem(lines[len(lines)-1])
}

// parseTopMem reads top's MEM column, which is a number with a K, M, or G
// suffix and sometimes a trailing "+" or "-" marking the change since the
// last sample.
func parseTopMem(s string) (uint64, error) {
	s = strings.TrimRight(strings.TrimSpace(s), "+-")
	if s == "" {
		return 0, fmt.Errorf("parsing top memory: %w", errEmptyField)
	}

	units := map[byte]uint64{'B': 1, 'K': kib, 'M': mib, 'G': gib}

	unit, ok := units[s[len(s)-1]]
	if !ok {
		return 0, fmt.Errorf("parsing top memory %q: %w", s, errUnknownUnit)
	}

	n, err := strconv.ParseFloat(s[:len(s)-1], 64)
	if err != nil {
		return 0, fmt.Errorf("parsing top memory %q: %w", s, err)
	}

	return uint64(n * float64(unit)), nil
}

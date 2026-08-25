// Package sysinfo reads the machine numbers the scheduler and the diagnostics
// panel both need. It is macOS-specific, like the rest of wavez.
package sysinfo

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// Memory is one snapshot of physical memory.
type Memory struct {
	UsedBytes  uint64
	TotalBytes uint64
}

// Free reports memory not currently backing a resident page.
func (m Memory) Free() uint64 {
	if m.TotalBytes < m.UsedBytes {
		return 0
	}

	return m.TotalBytes - m.UsedBytes
}

// ReadMemory reports physical memory use. Used counts the pages macOS cannot
// hand out without evicting something: active, wired, and compressed. Inactive
// and cached pages are excluded because they are reclaimable, and counting
// them would make a healthy machine look full.
func ReadMemory(ctx context.Context) (Memory, error) {
	total, err := sysctlUint(ctx, "hw.memsize")
	if err != nil {
		return Memory{}, err
	}

	pageSize, err := sysctlUint(ctx, "hw.pagesize")
	if err != nil {
		return Memory{}, err
	}

	out, err := exec.CommandContext(ctx, "vm_stat").Output()
	if err != nil {
		return Memory{}, fmt.Errorf("running vm_stat: %w", err)
	}

	var pages uint64
	for _, line := range strings.Split(string(out), "\n") {
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(name) {
		case "Pages active", "Pages wired down", "Pages occupied by compressor":
			pages += parsePages(value)
		}
	}

	return Memory{UsedBytes: pages * pageSize, TotalBytes: total}, nil
}

func parsePages(value string) uint64 {
	n, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimSpace(value), "."), 10, 64)
	if err != nil {
		return 0
	}

	return n
}

func sysctlString(ctx context.Context, key string) (string, error) {
	//nolint:gosec // key is a package-internal constant, never caller input
	out, err := exec.CommandContext(ctx, "sysctl", "-n", key).Output()
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", key, err)
	}

	return string(out), nil
}

func sysctlUint(ctx context.Context, key string) (uint64, error) {
	out, err := sysctlString(ctx, key)
	if err != nil {
		return 0, err
	}

	n, err := strconv.ParseUint(strings.TrimSpace(out), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parsing %s: %w", key, err)
	}

	return n, nil
}

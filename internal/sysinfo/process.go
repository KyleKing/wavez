package sysinfo

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// Process is one running process as `ps` reports it.
type Process struct {
	// Command is the executable's base name, not its argv.
	Command string
	PID     int
	// CPUPercent is `ps`'s own recent-usage estimate, not an instantaneous
	// reading, and it is a share of one core rather than of the machine.
	CPUPercent float64
	RSSBytes   uint64
}

// psFields is the column count ReadProcesses asks `ps` for: pid, rss, %cpu,
// and comm.
const psFields = 4

// ReadProcesses lists every process with its resident set and CPU share. One
// `ps` call answers the whole panel's process questions, so the daemon reads
// the table once per sample rather than once per process it cares about.
func ReadProcesses(ctx context.Context) ([]Process, error) {
	out, err := exec.CommandContext(ctx, "ps", "-A", "-o", "pid=,rss=,%cpu=,comm=").Output()
	if err != nil {
		return nil, fmt.Errorf("running ps: %w", err)
	}

	var procs []Process

	for _, line := range strings.Split(string(out), "\n") {
		p, ok := parsePSLine(line)
		if ok {
			procs = append(procs, p)
		}
	}

	return procs, nil
}

func parsePSLine(line string) (Process, bool) {
	fields := strings.Fields(line)
	if len(fields) < psFields {
		return Process{}, false
	}

	pid, err := strconv.Atoi(fields[0])
	if err != nil {
		return Process{}, false
	}

	rssKB, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return Process{}, false
	}

	cpu, err := strconv.ParseFloat(fields[2], 64)
	if err != nil {
		return Process{}, false
	}

	const kb = 1024

	return Process{
		PID:        pid,
		RSSBytes:   rssKB * kb,
		CPUPercent: cpu,
		Command:    baseCommand(strings.Join(fields[3:], " ")),
	}, true
}

func baseCommand(comm string) string {
	if i := strings.LastIndexByte(comm, '/'); i >= 0 {
		return comm[i+1:]
	}

	return comm
}

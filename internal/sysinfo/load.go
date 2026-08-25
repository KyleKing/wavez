package sysinfo

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Load is the run-queue length the kernel has averaged, divided by the cores
// it has to run on, so one number compares across machines. Below 1 the
// machine has a core to spare and above it work is waiting.
type Load struct {
	PerCore float64
	Cores   int
}

// ReadLoad reports the one-minute load average per core. One minute is the
// shortest average the kernel keeps and it lags, which is the right lag
// here: what makes a local turn slow is the gate run that just started, and
// a sample narrow enough to miss it would send the turn to the local server
// anyway.
func ReadLoad(ctx context.Context) (Load, error) {
	cores, err := sysctlUint(ctx, "hw.logicalcpu")
	if err != nil {
		return Load{}, err
	}

	if cores == 0 || cores > math.MaxInt32 {
		return Load{}, fmt.Errorf("reading hw.logicalcpu: %w", errNoCores)
	}

	raw, err := sysctlString(ctx, "vm.loadavg")
	if err != nil {
		return Load{}, err
	}

	one, err := parseLoadavg(raw)
	if err != nil {
		return Load{}, err
	}

	return Load{PerCore: one / float64(cores), Cores: int(cores)}, nil
}

// errNoCores is a machine reporting no cores, which is not a number any
// caller can divide by.
var errNoCores = errors.New("the machine reports no cores")

// parseLoadavg reads the one-minute figure out of vm.loadavg, which sysctl
// renders as "{ 2.18 1.91 1.53 }".
func parseLoadavg(raw string) (float64, error) {
	fields := strings.Fields(strings.Trim(strings.TrimSpace(raw), "{}"))
	if len(fields) == 0 {
		return 0, fmt.Errorf("parsing vm.loadavg %q: %w", raw, errNoCores)
	}

	one, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, fmt.Errorf("parsing vm.loadavg %q: %w", raw, err)
	}

	return one, nil
}

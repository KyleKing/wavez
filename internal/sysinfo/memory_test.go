package sysinfo_test

import (
	"testing"

	"github.com/kyleking/wavez/internal/sysinfo"
)

func TestReadMemoryReportsPlausibleNumbers(t *testing.T) {
	t.Parallel()

	m, err := sysinfo.ReadMemory(t.Context())
	if err != nil {
		t.Fatalf("ReadMemory: %v", err)
	}

	const oneGiB = 1 << 30
	if m.TotalBytes < oneGiB {
		t.Fatalf("TotalBytes = %d, want at least 1 GiB", m.TotalBytes)
	}
	if m.UsedBytes == 0 || m.UsedBytes > m.TotalBytes {
		t.Fatalf("UsedBytes = %d, want between 0 and TotalBytes %d", m.UsedBytes, m.TotalBytes)
	}
	if m.Free() != m.TotalBytes-m.UsedBytes {
		t.Fatalf("Free() = %d, want %d", m.Free(), m.TotalBytes-m.UsedBytes)
	}
}

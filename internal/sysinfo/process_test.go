package sysinfo_test

import (
	"testing"

	"github.com/kyleking/wavez/internal/sysinfo"
)

func TestParseTopMem(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		in   string
		want uint64
	}{
		{"1659M", 1659 << 20},
		{"5296K", 5296 << 10},
		{"2.5G", 2684354560},
		{"16M+", 16 << 20},
	} {
		got, err := sysinfo.ParseTopMem(tt.in)
		if err != nil || got != tt.want {
			t.Errorf("sysinfo.ParseTopMem(%q) = %d, %v; want %d", tt.in, got, err, tt.want)
		}
	}

	if _, err := sysinfo.ParseTopMem("lots"); err == nil {
		t.Error("parseTopMem accepted a value with no unit")
	}
}

func TestParsePSLine(t *testing.T) {
	t.Parallel()

	p, ok := sysinfo.ParsePSLine("43476  16448   1.5 /opt/homebrew/bin/llama-server")
	if !ok || p.PID != 43476 || p.RSSBytes != 16448<<10 || p.CPUPercent != 1.5 || p.Command != "llama-server" {
		t.Errorf("parsePSLine = %+v, %v", p, ok)
	}

	if _, ok := sysinfo.ParsePSLine("  PID   RSS  %CPU COMM"); ok {
		t.Error("parsePSLine accepted the header")
	}
}

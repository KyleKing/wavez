package gate_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/kyleking/wavez/internal/gate"
)

func TestLogAppendAndEntries(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "gate.log")

	l, err := gate.OpenLog(path)
	if err != nil {
		t.Fatalf("OpenLog: %v", err)
	}

	want := []gate.LogEntry{
		{
			Timestamp: time.Unix(1000, 0).UTC(), Gate: "format", Level: gate.LevelPackage,
			Duration: time.Second, TestCount: 0, Pass: true,
		},
		{
			Timestamp: time.Unix(2000, 0).UTC(), Gate: "go-test", Level: gate.LevelLine,
			Duration: 2 * time.Second, TestCount: 3, Pass: false,
		},
	}

	for _, e := range want {
		if err := l.Append(e); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	got, err := l.Entries()
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}

	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d", len(got), len(want))
	}
	for i := range want {
		if !got[i].Timestamp.Equal(want[i].Timestamp) || got[i].Gate != want[i].Gate ||
			got[i].Level != want[i].Level || got[i].Duration != want[i].Duration ||
			got[i].TestCount != want[i].TestCount || got[i].Pass != want[i].Pass {
			t.Errorf("entry %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

package gate_test

import (
	"testing"
	"time"

	"github.com/kyleking/wavez/internal/gate"
)

func TestResultForModel(t *testing.T) {
	t.Parallel()

	ts := time.Unix(1000, 0)

	pass := gate.Result{
		Gate:      "go-test",
		Pass:      true,
		Timestamp: ts,
		Examined:  42,
		Failures:  nil,
	}

	got := pass.ForModel()
	if !got.Pass || !got.Timestamp.Equal(ts) {
		t.Errorf("passing ForModel = %+v, want Pass=true Timestamp=%v", got, ts)
	}
	if got.Failures != nil {
		t.Errorf("passing ForModel leaked Failures: %+v, want nothing but a boolean and a timestamp", got.Failures)
	}

	fail := gate.Result{
		Gate:      "go-test",
		Pass:      false,
		Timestamp: ts,
		Failures: []gate.TrimmedFailure{
			{Test: "TestFoo", Package: "pkg", Frames: []string{"pkg/foo.go:10: boom"}},
		},
	}

	gotFail := fail.ForModel()
	if gotFail.Pass {
		t.Errorf("failing ForModel.Pass = true, want false")
	}
	if len(gotFail.Failures) != 1 || gotFail.Failures[0].Test != "TestFoo" {
		t.Errorf("failing ForModel.Failures = %+v, want the one TrimmedFailure carried through", gotFail.Failures)
	}
}

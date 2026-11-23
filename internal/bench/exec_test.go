//go:build unix

package bench_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/bench"
)

// beating is a shell loop that rewrites the file beat several times a
// second for as long as it lives. It keeps none of the harness's pipes, the
// way a server an agent left in the background would not.
const beating = `i=0; while :; do i=$((i+1)); echo $i > "$BEAT.tmp"; mv "$BEAT.tmp" "$BEAT"; sleep 0.05; done`

func TestClaude_ChildrenDoNotOutliveTheRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// body is the fake claude, which starts a child that beats.
		body string
		// cancel cuts the run off once the child is beating.
		cancel bool
	}{
		{
			name:   "a run cut off takes the children of claude with it",
			body:   "(" + beating + ") >/dev/null 2>&1 &\nwait\n",
			cancel: true,
		},
		{
			name: "a child that ignores SIGTERM is killed after the grace period",
			body: "(trap '' TERM; " + beating + ") >/dev/null 2>&1 &\nwait\n",

			cancel: true,
		},
		{
			name: "a child left behind by a run that finished is killed",
			body: "(" + beating + ") >/dev/null 2>&1 &\n" +
				`while [ ! -s "$BEAT" ]; do sleep 0.01; done` + "\n" +
				`echo '{"type":"result","subtype":"success"}'` + "\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			beat := filepath.Join(t.TempDir(), "beat")
			harness := &bench.Claude{Bin: script(t, "claude", tt.body)}

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			done := make(chan error, 1)

			go func() {
				_, err := harness.Run(ctx, bench.Request{
					Dir: t.TempDir(), Prompt: "p", Env: []string{"BEAT=" + beat, "PATH=/usr/bin:/bin"},
				})
				done <- err
			}()

			if tt.cancel {
				require.Eventually(t, func() bool { return fileHasContent(beat) }, 30*time.Second, 10*time.Millisecond)
				cancel()
			}

			select {
			case err := <-done:
				assert.Equal(t, tt.cancel, err != nil)
			case <-time.After(time.Minute):
				require.Fail(t, "the run never returned")
			}

			last := read(t, beat)

			time.Sleep(500 * time.Millisecond)
			assert.Equal(t, last, read(t, beat), "something is still beating after the run returned")
		})
	}
}

package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/session"
)

//nolint:paralleltest // the status line reads NO_COLOR and COLUMNS from the environment
func TestRunStatusline(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	t.Setenv("COLUMNS", "")

	project := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(project, ".git"), 0o750))

	store := session.NewStore(project)
	require.NoError(t, store.Prepare())

	state := fmt.Sprintf(`{"session_id":"s1","version":%d,"abstained":"abc","recorded":[]}`, session.Version)
	require.NoError(t, os.WriteFile(store.Path("s1"), []byte(state), 0o600))

	tests := []struct {
		name  string
		stdin string
		want  string
		args  []string
	}{
		{
			name: "a session's state and the harness's numbers make one line",
			stdin: fmt.Sprintf(
				`{"session_id":"s1","cwd":%q,"context_window":{"used_percentage":12.4},"cost":{"total_cost_usd":0.5}}`,
				project,
			),
			want: "wavez · ! checks could not run · context 12% · $0.50\n",
		},
		{
			name: "a session the hooks never saw shows the harness's numbers",
			stdin: fmt.Sprintf(
				`{"session_id":"other","cwd":%q,"context_window":{"used_percentage":12.4}}`, project,
			),
			want: "wavez · context 12%\n",
		},
		{name: "input that is not JSON prints nothing", stdin: "not json"},
		{name: "a directory that is no project prints nothing", stdin: `{"session_id":"s1","cwd":"/"}`},
		{name: "--config prints the settings to add", args: []string{"--config"}, want: statuslineConfig},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer

			err := runStatusline(t.Context(), tt.args, stdio{in: strings.NewReader(tt.stdin), out: &out, err: &errOut})
			require.NoError(t, err)
			assert.Equal(t, tt.want, out.String())
			assert.Empty(t, errOut.String())
		})
	}
}

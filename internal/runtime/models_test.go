package runtime_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/kyleking/wavez/internal/runtime"
)

func TestParseOllamaList(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		out     string
		want    []runtime.Model
		wantErr bool
	}{
		{
			name: "with header",
			out: "NAME              ID              SIZE      MODIFIED\n" +
				"qwen3:8b          e4954ed05452    5.2 GB    2 weeks ago\n" +
				"gemma4:12b        1a2b3c4d5e6f    7.6 GB    3 days ago\n",
			want: []runtime.Model{
				{Name: "qwen3:8b", Size: "5.2 GB", Modified: "2 weeks ago"},
				{Name: "gemma4:12b", Size: "7.6 GB", Modified: "3 days ago"},
			},
		},
		{
			name: "empty",
			out:  "NAME              ID              SIZE      MODIFIED\n",
			want: nil,
		},
		{
			name:    "malformed line",
			out:     "NAME              ID              SIZE      MODIFIED\nnot enough columns\n",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := runtime.ParseOllamaList([]byte(tt.out))
			if tt.wantErr {
				if !errors.Is(err, runtime.ErrMalformedOllamaList) {
					t.Fatalf("ParseOllamaList error = %v, want wrapping ErrMalformedOllamaList", err)
				}

				return
			}

			if err != nil {
				t.Fatalf("ParseOllamaList: %v", err)
			}

			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseOllamaList() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

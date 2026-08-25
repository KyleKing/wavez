package sysinfo

import "testing"

// sysctl renders vm.loadavg with braces around three figures, and a machine
// that cannot be read must fail rather than report an idle laptop: an
// unreadable load that defaults to zero sends every turn to a local server
// that is already saturated.
func TestParseLoadavg(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		raw     string
		want    float64
		wantErr bool
	}{
		{raw: "{ 2.18 1.91 1.53 }\n", want: 2.18},
		{raw: "{ 0.00 0.00 0.00 }", want: 0},
		{raw: "2.18 1.91 1.53", want: 2.18},
		{raw: "{ }", wantErr: true},
		{raw: "", wantErr: true},
		{raw: "{ none }", wantErr: true},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			t.Parallel()

			got, err := parseLoadavg(tc.raw)
			if (err != nil) != tc.wantErr {
				t.Fatalf("parseLoadavg(%q) error = %v, want an error: %v", tc.raw, err, tc.wantErr)
			}

			if err == nil && got != tc.want {
				t.Errorf("parseLoadavg(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

package fetchsvc

import "testing"

func TestParseISO8601DurationMinutes(t *testing.T) {
	cases := []struct {
		in      string
		want    int
		wantErr bool
	}{
		{"PT30M", 30, false},
		{"PT1H", 60, false},
		{"PT1H30M", 90, false},
		{"P1D", 24 * 60, false},
		{"P1W", 7 * 24 * 60, false},
		{"PT90S", 1, false}, // rounds down
		{"PT30S", 0, false}, // rounds down to 0
		{"", 0, true},
		{"not-a-duration", 0, true},
		{"P1Y", 0, true}, // years unsupported
	}

	for _, tc := range cases {
		got, err := parseISO8601DurationMinutes(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseISO8601DurationMinutes(%q) = %d, want error", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseISO8601DurationMinutes(%q) unexpected error: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseISO8601DurationMinutes(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

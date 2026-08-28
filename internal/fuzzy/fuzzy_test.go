package fuzzy

import "testing"

func TestMatch(t *testing.T) {
	tests := []struct {
		name   string
		query  string
		target string
		want   bool
	}{
		{"exact case-insensitive", "drill", "Drill", true},
		{"substring", "snow blower", "Ego Snow Blower", true},
		{"tokens in different order", "blower snow", "Ego Snow Blower", true},
		{"case insensitive tokens", "SNOW Blower", "Ego Snow Blower", true},
		{"partial single token", "snow", "Ego Snow Blower", true},
		{"no match", "lawnmower", "Ego Snow Blower", false},
		{"extra query token no match", "snow blower turbo", "Ego Snow Blower", false},
		{"empty query empty target", "", "", true},
		{"empty query nonempty target", "", "Drill", false},
		{"query with surrounding spaces", "  drill ", "power drill", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Match(tt.query, tt.target); got != tt.want {
				t.Errorf("Match(%q, %q) = %v, want %v", tt.query, tt.target, got, tt.want)
			}
		})
	}
}

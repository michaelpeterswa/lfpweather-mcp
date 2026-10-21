package handlers

import (
	"testing"
	"time"
)

func TestDefaultBucket(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name, rng, start, end, want string
	}{
		{"default range", "", "", "", "30m"},
		{"12h", "12h", "", "", "15m"},
		{"24h", "24h", "", "", "30m"},
		{"7d", "7d", "", "", "6h"},
		{"30d", "30d", "", "", "1d"},
		{"90d", "90d", "", "", "1w"},
		{"ytd", "ytd", "", "", "1w"},
		{"all", "all", "", "", "1w"},
		{"absolute two days", "", "2026-10-18T12:00:00Z", "2026-10-20T12:00:00Z", "1h"},
		{"absolute open end", "", "2026-10-19T12:00:00Z", "", "30m"},
		{"bad start leaves it to the api", "", "yesterday", "", ""},
		{"unknown range leaves it to the api", "decade", "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := defaultBucket(tt.rng, tt.start, tt.end, now); got != tt.want {
				t.Errorf("defaultBucket(%q, %q, %q) = %q, want %q", tt.rng, tt.start, tt.end, got, tt.want)
			}
		})
	}
}

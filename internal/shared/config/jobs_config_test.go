package config

import (
	"testing"
	"time"
)

func TestParseJobInterval(t *testing.T) {
	cases := map[string]time.Duration{
		"8h":    8 * time.Hour,
		"30m":   30 * time.Minute,
		"":      0,
		"0":     0,
		"off":   0,
		"OFF":   0,
		"bogus": 0, // unparseable → disabled (fail safe)
		"-5m":   0, // negative → disabled
	}
	for in, want := range cases {
		if got := parseJobInterval(in); got != want {
			t.Errorf("parseJobInterval(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestGithubStatsRefreshEnabled(t *testing.T) {
	cases := []struct {
		feat bool
		ivl  time.Duration
		want bool
	}{
		{true, 8 * time.Hour, true},
		{false, 8 * time.Hour, false}, // master feature off
		{true, 0, false},              // interval off
		{false, 0, false},
	}
	for _, c := range cases {
		cfg := &Config{FeatureGithubStats: c.feat, GithubStatsRefreshInterval: c.ivl}
		if got := cfg.GithubStatsRefreshEnabled(); got != c.want {
			t.Errorf("feat=%v ivl=%v: got %v, want %v", c.feat, c.ivl, got, c.want)
		}
	}
}

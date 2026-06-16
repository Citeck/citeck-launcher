package bundle

import (
	"testing"
	"time"
)

func TestParsePullPeriod(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
		ok   bool
	}{
		// Go duration form
		{"2h", 2 * time.Hour, true},
		{"30m", 30 * time.Minute, true},
		{"1h30m", 90 * time.Minute, true},
		// uppercase units (Kotlin tolerated; we lowercase)
		{"2H", 2 * time.Hour, true},
		{"30M", 30 * time.Minute, true},
		// ISO-8601 (Kotlin Duration.toString form)
		{"PT2H", 2 * time.Hour, true},
		{"PT30M", 30 * time.Minute, true},
		{"PT2H30M", 150 * time.Minute, true},
		{"P2D", 48 * time.Hour, true},
		{"P1DT2H", 26 * time.Hour, true},
		{" PT2H ", 2 * time.Hour, true},
		// bare integer = seconds
		{"3600", time.Hour, true},
		{"90", 90 * time.Second, true},
		// invalid / non-positive
		{"", 0, false},
		{"0", 0, false},
		{"-5", 0, false},
		{"abc", 0, false},
		{"PTX", 0, false},
		{"2x", 0, false},
		{"P", 0, false},
	}
	for _, c := range cases {
		got, ok := parsePullPeriod(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("parsePullPeriod(%q) = (%v, %v), want (%v, %v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

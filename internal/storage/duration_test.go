package storage

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseISO8601Duration(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"", 0},
		{"PT2H", 2 * time.Hour},
		{"PT30M", 30 * time.Minute},
		{"PT45S", 45 * time.Second},
		{"PT2H30M", 2*time.Hour + 30*time.Minute},
		{"PT1H15M30S", time.Hour + 15*time.Minute + 30*time.Second},
		{"PT0S", 0},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got, err := ParseISO8601Duration(c.in)
			require.NoError(t, err)
			assert.Equal(t, c.want, got)
		})
	}
}

func TestParseISO8601Duration_Invalid(t *testing.T) {
	cases := []string{
		"2H",     // missing PT prefix
		"PT",     // empty after PT
		"PTH",    // missing number
		"PT2X",   // unknown unit
		"PT2H30", // trailing number without unit
		"PTabc",  // non-numeric
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			_, err := ParseISO8601Duration(in)
			assert.Error(t, err)
		})
	}
}

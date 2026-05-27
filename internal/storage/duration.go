package storage

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// DefaultRepoPullPeriod is the Kotlin-parity default (Duration.ofHours(6) in
// WorkspaceDto.DEFAULT; the per-workspace form default is PT2H — we expose the
// form default here so missing values match the UI prompt).
const DefaultRepoPullPeriod = "PT2H"

// ParseISO8601Duration parses a minimal subset of ISO 8601 duration strings
// (PT[n]H[n]M[n]S, e.g. "PT2H", "PT30M", "PT2H30M15S"). This is enough for
// the workspace.repoPullPeriod field which Kotlin serializes as
// `Duration.toString()` — JDK's PT* format.
//
// Returns 0 + nil for the empty string so callers can treat "unset" as
// "fall back to default pull period at the call site".
func ParseISO8601Duration(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	if !strings.HasPrefix(s, "PT") {
		return 0, fmt.Errorf("invalid ISO 8601 duration %q: must start with PT", s)
	}
	rest := s[2:]
	if rest == "" {
		return 0, fmt.Errorf("invalid ISO 8601 duration %q: empty after PT", s)
	}

	var total time.Duration
	var numStart int
	for i, r := range rest {
		switch r {
		case 'H', 'M', 'S':
			if i == numStart {
				return 0, fmt.Errorf("invalid ISO 8601 duration %q: missing number before %c", s, r)
			}
			n, err := strconv.Atoi(rest[numStart:i])
			if err != nil {
				return 0, fmt.Errorf("invalid ISO 8601 duration %q: %w", s, err)
			}
			switch r {
			case 'H':
				total += time.Duration(n) * time.Hour
			case 'M':
				total += time.Duration(n) * time.Minute
			case 'S':
				total += time.Duration(n) * time.Second
			}
			numStart = i + 1
		default:
			if r < '0' || r > '9' {
				return 0, fmt.Errorf("invalid ISO 8601 duration %q: unexpected character %c", s, r)
			}
		}
	}
	if numStart != len(rest) {
		return 0, fmt.Errorf("invalid ISO 8601 duration %q: trailing number without unit", s)
	}
	return total, nil
}

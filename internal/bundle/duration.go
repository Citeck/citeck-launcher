package bundle

import (
	"strconv"
	"strings"
	"time"
)

// parsePullPeriod parses a bundle-repo pull period accepting the same forms the
// Kotlin 1.x DurationDeserializer did, so a workspace-v1.yml authored against
// the 1.x schema isn't silently dropped (and fall back to the default):
//   - a Go duration: "2h", "30m", "1h30m"
//   - an uppercase-unit form: "2H", "30M" (lowercased into the Go form)
//   - an ISO-8601 duration: "PT2H", "PT30M", "P2DT3H"
//   - a bare integer = seconds: "3600"
//
// Returns (0, false) when unparseable or non-positive.
func parsePullPeriod(s string) (time.Duration, bool) {
	s = strings.ReplaceAll(strings.TrimSpace(s), " ", "")
	if s == "" {
		return 0, false
	}
	// Bare integer = seconds (Kotlin VALUE_NUMBER_INT; a YAML number reaches
	// this layer already stringified).
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		if n <= 0 {
			return 0, false
		}
		return time.Duration(n) * time.Second, true
	}
	// ISO-8601 (Kotlin routes a leading P/-P to Duration.parse).
	if up := strings.ToUpper(s); strings.HasPrefix(up, "P") || strings.HasPrefix(up, "-P") {
		if d, ok := parseISO8601Duration(up); ok && d > 0 {
			return d, true
		}
		return 0, false
	}
	// Go duration form. Lowercasing also accepts the Kotlin uppercase-unit form
	// ("2H" → "2h"), since Go's units are lowercase.
	if d, err := time.ParseDuration(strings.ToLower(s)); err == nil && d > 0 {
		return d, true
	}
	return 0, false
}

// parseISO8601Duration parses P[nD][T[nH][nM][nS]] (already uppercased, optional
// leading '-'). Days are the only date component supported — weeks/months/years
// have no fixed length and are meaningless for a pull throttle.
func parseISO8601Duration(s string) (time.Duration, bool) {
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	if !strings.HasPrefix(s, "P") {
		return 0, false
	}
	s = s[1:] // drop 'P'
	datePart, timePart, _ := strings.Cut(s, "T")
	if datePart == "" && timePart == "" {
		return 0, false
	}
	var total time.Duration
	if datePart != "" {
		d, ok := parseISOComponents(datePart, map[byte]time.Duration{'D': 24 * time.Hour})
		if !ok {
			return 0, false
		}
		total += d
	}
	if timePart != "" {
		d, ok := parseISOComponents(timePart, map[byte]time.Duration{'H': time.Hour, 'M': time.Minute, 'S': time.Second})
		if !ok {
			return 0, false
		}
		total += d
	}
	if neg {
		total = -total
	}
	return total, true
}

// parseISOComponents sums "<number><unit>" runs (e.g. "2H30M") for the allowed
// units. Fails on a missing number, an unknown unit, or a trailing number.
func parseISOComponents(s string, units map[byte]time.Duration) (time.Duration, bool) {
	var total time.Duration
	numStart := 0
	for i := 0; i < len(s); i++ {
		if c := s[i]; c >= '0' && c <= '9' {
			continue
		}
		unit, ok := units[s[i]]
		if !ok || i == numStart {
			return 0, false
		}
		n, err := strconv.Atoi(s[numStart:i])
		if err != nil {
			return 0, false
		}
		total += time.Duration(n) * unit
		numStart = i + 1
	}
	if numStart != len(s) {
		return 0, false // trailing number without a unit
	}
	return total, true
}

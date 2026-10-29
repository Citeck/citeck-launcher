package bundle

import "regexp"

// Feature ids are shared by bundle declarations and persisted namespace choices.
var experimentalFeatureIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// IsValidExperimentalFeatureID reports whether s can identify an experimental
// feature in both bundle metadata and namespace configuration.
func IsValidExperimentalFeatureID(s string) bool {
	return experimentalFeatureIDRe.MatchString(s)
}

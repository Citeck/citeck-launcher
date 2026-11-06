package bundle

import (
	"regexp"
	"strings"

	"github.com/citeck/citeck-launcher/internal/msg"
)

var snapshotBundleVersionPattern = regexp.MustCompile(`^\d+(\.\d+)*(-[A-Za-z0-9][A-Za-z0-9._-]*)?$`)

// BundleVersionRefusal checks a snapshot's optional lower bound against a
// concrete bundle key. Scope directories identify a file, not its age. Numeric
// components and prereleases use the same ordering as the bundle picker.
// Unknown versions cannot satisfy an explicit requirement. Symbolic selectors
// are the caller's concern; this does not introduce another LATEST resolver.
func (s SnapshotDef) BundleVersionRefusal(version string) msg.Message {
	minimum := strings.TrimSpace(s.MinBundleVersion)
	if minimum == "" {
		return msg.Message{}
	}
	if !snapshotBundleVersionPattern.MatchString(minimum) {
		return msg.New("snapshot.bundleMinimumInvalid", "name", s.ID, "minimum", minimum)
	}
	current := strings.TrimSpace(version)
	if i := strings.LastIndex(current, "/"); i >= 0 {
		current = current[i+1:]
	}
	if !snapshotBundleVersionPattern.MatchString(current) {
		return msg.New("snapshot.bundleVersionUnknown", "name", s.ID, "minimum", minimum)
	}
	if compareBundleVersions(current, minimum) < 0 {
		return msg.New("snapshot.bundleMinimum", "name", s.ID, "minimum", minimum, "current", version)
	}
	return msg.Message{}
}

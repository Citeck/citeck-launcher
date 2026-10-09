package deps

import "regexp"

// SeaweedFS mini upgrades are performed on a copy for every version change.
// Only ordinary numeric image tags are understood: large_disk/enterprise builds
// have different compatibility rules and must not share this policy.
type seaweedDescriptor struct {
	id                  ID
	appName, volumeBase string
}

var seaweedTag = regexp.MustCompile(`^v?\d+\.\d+(?:\.\d+)?$`)

// NewSeaweedDescriptor describes one configured mini store and its data volume.
func NewSeaweedDescriptor(id ID, app, volume string) Descriptor {
	return seaweedDescriptor{id, app, volume}
}

// IsSeaweedFamily reports whether a dependency uses the SeaweedFS policy.
func IsSeaweedFamily(d Descriptor) bool         { _, ok := d.(seaweedDescriptor); return ok }
func (d seaweedDescriptor) ID() ID              { return d.id }
func (d seaweedDescriptor) AppName() string     { return d.appName }
func (d seaweedDescriptor) VolumeBase() string  { return d.volumeBase }
func (seaweedDescriptor) LegacyImage() string   { return "chrislusf/seaweedfs:4.48" }
func (seaweedDescriptor) SupportFloor() Version { return Version{Major: 4, Minor: 48} }
func (seaweedDescriptor) Migratable() bool      { return true }
func (seaweedDescriptor) ParseVersion(image string) (Version, bool) {
	v, ok := ParseImageVersion(image)
	return v, ok && seaweedTag.MatchString(v.Raw)
}
func (seaweedDescriptor) IsBreaking(a, b Version) bool { return compareVersions(a, b) != 0 }
func (seaweedDescriptor) UpgradeSupport(a, b Version) VendorSupport {
	// The vendor promises forward compatibility except documented exceptions.
	// This plan is limited to the standard 4.x mini family;
	// crossing a major needs a separately verified policy.
	return VendorSupport{Allowed: a.Major == 4 && b.Major == 4 && compareVersions(b, a) >= 0}
}

package deps

import "github.com/citeck/citeck-launcher/internal/appdef"

// zkOldestMigratableMajor/Minor is the oldest ZooKeeper release whose data
// this launcher will move. Below 3.5 the data may predate
// zookeeper.snapshot.trust.empty, i.e. a newer server can refuse to load it or
// silently treat an empty snapshot as valid state — and ZooKeeper holds the
// per-webapp patch-result markers and eproc's permanent mongo-disabled marker,
// so "silently empty" is the failure that costs the most.
const (
	zkOldestMigratableMajor = 3
	zkOldestMigratableMinor = 5
)

// zookeeperDescriptor describes ZooKeeper, which holds coordination data.
// Minor bumps are held back (IsBreaking) even though 3.8 and 3.9 share one
// on-disk format: the copy that a migration makes is sub-second on this data
// and insures against a future format change.
type zookeeperDescriptor struct{}

func (zookeeperDescriptor) ID() ID          { return Zookeeper }
func (zookeeperDescriptor) AppName() string { return appdef.AppZookeeper }
func (zookeeperDescriptor) ParseVersion(image string) (Version, bool) {
	return ParseImageVersion(image)
}
func (zookeeperDescriptor) VolumeBase() string               { return "zookeeper" }
func (zookeeperDescriptor) IsBreaking(from, to Version) bool { return minorBreaking(from, to) }
func (zookeeperDescriptor) Migratable() bool                 { return true }
func (zookeeperDescriptor) LegacyImage() string              { return "zookeeper:3.9" }

// UpgradeSupport: forward only, and only from 3.5 upwards. ZooKeeper publishes
// no hop table — the on-disk format has been unchanged since 3.5 and the
// vendor says a minor bump needs "no particular additional upgrade procedure"
// — so the only rule is the pre-3.5 floor, and a refusal there has no path
// (Via ""): there is nothing this launcher can do with data that old.
func (zookeeperDescriptor) UpgradeSupport(from, to Version) VendorSupport {
	if compareVersions(to, from) < 0 {
		return VendorSupport{}
	}
	if compareVersions(from, Version{Major: zkOldestMigratableMajor, Minor: zkOldestMigratableMinor}) < 0 {
		return VendorSupport{}
	}
	return VendorSupport{Allowed: true}
}

package deps

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestSeaweedVersionAndUpgradePolicy(t *testing.T) {
	d := NewSeaweedDescriptor("store", "store", "store_data")
	assert.True(t, IsSeaweedFamily(d))
	assert.True(t, d.Migratable())
	assert.Equal(t, "store_data2", VolumeName(d, 1))
	assert.Equal(t, "store_data3", VolumeName(d, 2))
	for _, image := range []string{"chrislusf/seaweedfs:4.48", "mirror/seaweedfs:4.49.1"} {
		_, ok := d.ParseVersion(image)
		assert.True(t, ok)
	}
	for _, image := range []string{"chrislusf/seaweedfs:latest", "chrislusf/seaweedfs:4.48_large_disk", "repo:4.48-enterprise", "repo@sha256:abc"} {
		_, ok := d.ParseVersion(image)
		assert.False(t, ok)
	}
	a := Version{Major: 4, Minor: 48}
	for _, b := range []Version{{Major: 4, Minor: 49}, {Major: 4, Minor: 48, Patch: 1}} {
		assert.True(t, d.IsBreaking(a, b))
		assert.True(t, d.UpgradeSupport(a, b).Allowed)
	}
	assert.False(t, d.IsBreaking(a, a))
	assert.False(t, d.UpgradeSupport(a, Version{Major: 4, Minor: 47}).Allowed)
	assert.False(t, d.UpgradeSupport(a, Version{Major: 5}).Allowed)
}

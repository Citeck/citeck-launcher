package appdef

import "testing"

// TestGetHash_VolumesContentHashParticipates guards against a regression
// where ApplicationDef.VolumesContentHash is silently dropped from
// GetHashInput — that would mean changes to bind-mounted file content
// stop triggering container recreates, and deployments would silently
// run with stale config.
func TestGetHash_VolumesContentHashParticipates(t *testing.T) {
	base := ApplicationDef{
		Name:  "x",
		Image: "img:1",
	}
	withHash := base
	withHash.VolumesContentHash = "abc123"

	if base.GetHash() == withHash.GetHash() {
		t.Fatal("VolumesContentHash does not affect the deployment hash — regression against the bind-mount content change detection")
	}
}

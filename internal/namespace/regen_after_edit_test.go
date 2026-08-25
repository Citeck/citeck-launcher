package namespace

import (
	"testing"
	"time"

	"github.com/citeck/citeck-launcher/internal/appdef"
)

// TestGearEditOfTheImageRecreatesTheContainer reproduces the reported bug:
// bump an app's image through the gear editor, the UI shows the new version,
// and nothing happens to the container until the user stops and starts the app
// by hand.
//
// The save path is two steps — UpdateAppDef persists the patch, then the daemon
// runs a full reload (handlePutAppConfig → invokeReload → Regenerate) — and
// UpdateAppDef used to write the edited def straight into the LIVE app. But
// `existing.Def` is the only record doRegenerate has of what the running
// container was built from, so the diff then compared the new def against
// itself and concluded nothing had changed.
//
// It survived because of an accident: handlePutAppConfig clears ImageDigest on
// save, and doRegenerate re-resolves it from the local image cache. When the
// new tag happened to be pulled already the digests differed and the recreate
// fired — so this only ever broke for a version the user had NOT pulled yet,
// which is every real version bump. The existing regenerate tests all call
// Regenerate WITHOUT the UpdateAppDef step, which is exactly the step that
// destroys the baseline.
func TestGearEditOfTheImageRecreatesTheContainer(t *testing.T) {
	md := newMockDocker()
	// The bumped tag is not in the local image cache — the whole point of a
	// version bump. GetImageDigest returns "" for it, so the digest cannot
	// accidentally carry the difference the image string should have carried.
	md.imageDigests = map[string]string{"gateway:2": ""}

	gateway := simpleApp(appdef.AppGateway, "gateway:1")

	r := NewRuntime(testConfig(), md, t.TempDir())
	defer r.Shutdown()
	r.Start([]appdef.ApplicationDef{gateway}, false)
	if !waitForStatus(r, NsStatusRunning, 10*time.Second) {
		t.Fatalf("namespace did not reach RUNNING, got %v", r.Status())
	}

	md.mu.Lock()
	before := md.containers[appdef.AppGateway].id
	md.mu.Unlock()
	if before == "" {
		t.Fatal("expected a container before the edit")
	}

	// Exactly what handlePutAppConfig sends: the edited def with ImageDigest
	// cleared (it is a runtime cache, re-resolved on the next pull).
	edited := gateway
	edited.Image = "gateway:2"
	edited.ImageDigest = ""
	if err := r.UpdateAppDef(appdef.AppGateway, edited, true); err != nil {
		t.Fatalf("UpdateAppDef: %v", err)
	}

	// ...followed by the reload the handler triggers.
	r.Regenerate([]appdef.ApplicationDef{edited}, nil, nil, false)

	waitForStatus(r, NsStatusStarting, 5*time.Second)
	if !waitForStatus(r, NsStatusRunning, 15*time.Second) {
		t.Fatalf("namespace did not return to RUNNING, got %v", r.Status())
	}
	if !waitForAppStatus(r, appdef.AppGateway, AppStatusRunning, 15*time.Second) {
		t.Fatalf("gateway did not return to RUNNING")
	}

	md.mu.Lock()
	after := md.containers[appdef.AppGateway].id
	md.mu.Unlock()

	if after == before {
		t.Fatalf("an image bump through the gear must recreate the container, "+
			"but the container id is unchanged: %s", after)
	}
}

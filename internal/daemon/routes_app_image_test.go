package daemon

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/appdef"
)

// TestImagePullStatus covers the per-ref pull-state tracking the inspect handler
// reports: absent, in-flight, finished-with-error, finished-ok.
func TestImagePullStatus(t *testing.T) {
	d := &Daemon{}

	pulling, errMsg := d.imagePullStatus("ref")
	assert.False(t, pulling, "unknown ref must not report pulling")
	assert.Empty(t, errMsg)

	d.imagePulls.Store("ref", &imagePullState{})
	pulling, errMsg = d.imagePullStatus("ref")
	assert.True(t, pulling, "stored, not-done state must report pulling")
	assert.Empty(t, errMsg)

	d.imagePulls.Store("ref", &imagePullState{done: true, err: "boom"})
	pulling, errMsg = d.imagePullStatus("ref")
	assert.False(t, pulling)
	assert.Equal(t, "boom", errMsg, "finished-with-error must surface the error")

	d.imagePulls.Store("ref", &imagePullState{done: true})
	pulling, errMsg = d.imagePullStatus("ref")
	assert.False(t, pulling)
	assert.Empty(t, errMsg, "finished-ok must report neither pulling nor error")
}

// TestAppImageRef covers the resolved-defs fallback used so the image popup
// works on a stopped / never-started namespace (no live runtime app).
func TestAppImageRef(t *testing.T) {
	d := &Daemon{activeNs: &activeNamespace{
		appDefs: []appdef.ApplicationDef{{Name: "web", Image: "img:1"}},
	}}

	ref, ok := d.appImageRef("web")
	require.True(t, ok, "known app must resolve via appDefs fallback")
	assert.Equal(t, "img:1", ref)

	_, ok = d.appImageRef("missing")
	assert.False(t, ok, "unknown app must not resolve")
}

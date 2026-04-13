package setup

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestComputePatch_Replace(t *testing.T) {
	before := map[string]any{"proxy": map[string]any{"host": "old.com", "port": float64(80)}}
	after := map[string]any{"proxy": map[string]any{"host": "new.com", "port": float64(80)}}

	fwd, rev := computePatch(before, after)
	require.Len(t, fwd, 1)
	assert.Equal(t, "replace", fwd[0].Op)
	assert.Equal(t, "/proxy/host", fwd[0].Path)
	assert.Equal(t, "new.com", fwd[0].Value)

	require.Len(t, rev, 1)
	assert.Equal(t, "replace", rev[0].Op)
	assert.Equal(t, "/proxy/host", rev[0].Path)
	assert.Equal(t, "old.com", rev[0].Value)
}

func TestComputePatch_Add(t *testing.T) {
	before := map[string]any{}
	after := map[string]any{"email": map[string]any{"host": "smtp.com", "port": float64(587)}}

	fwd, rev := computePatch(before, after)
	require.Len(t, fwd, 1)
	assert.Equal(t, "add", fwd[0].Op)
	assert.Equal(t, "/email", fwd[0].Path)

	require.Len(t, rev, 1)
	assert.Equal(t, "remove", rev[0].Op)
	assert.Equal(t, "/email", rev[0].Path)
}

func TestComputePatch_Remove(t *testing.T) {
	before := map[string]any{"email": map[string]any{"host": "smtp.com"}}
	after := map[string]any{}

	fwd, rev := computePatch(before, after)
	require.Len(t, fwd, 1)
	assert.Equal(t, "remove", fwd[0].Op)
	assert.Equal(t, "/email", fwd[0].Path)

	require.Len(t, rev, 1)
	assert.Equal(t, "add", rev[0].Op)
	assert.Equal(t, "/email", rev[0].Path)
}

func TestComputePatch_NoChanges(t *testing.T) {
	obj := map[string]any{"proxy": map[string]any{"host": "same.com"}}
	fwd, rev := computePatch(obj, obj)
	assert.Empty(t, fwd)
	assert.Empty(t, rev)
}

func TestComputePatch_NestedAdd(t *testing.T) {
	before := map[string]any{"proxy": map[string]any{"host": "x.com"}}
	after := map[string]any{"proxy": map[string]any{"host": "x.com", "tls": map[string]any{"enabled": true}}}

	fwd, rev := computePatch(before, after)
	require.Len(t, fwd, 1)
	assert.Equal(t, "add", fwd[0].Op)
	assert.Equal(t, "/proxy/tls", fwd[0].Path)

	require.Len(t, rev, 1)
	assert.Equal(t, "remove", rev[0].Op)
	assert.Equal(t, "/proxy/tls", rev[0].Path)
}

func TestConfigDiff_StructLevel(t *testing.T) {
	before := map[string]any{"id": "default", "proxy": map[string]any{"host": "a.com", "port": float64(443)}}
	after := map[string]any{"id": "default", "proxy": map[string]any{"host": "b.com", "port": float64(80)}}

	fwd, _ := computePatch(before, after)
	assert.Len(t, fwd, 2) // host + port changed
}

func TestApplyPatch_Replace(t *testing.T) {
	obj := map[string]any{"proxy": map[string]any{"host": "old.com"}}
	ops := []PatchOp{{Op: "replace", Path: "/proxy/host", Value: "new.com"}}

	err := applyPatch(obj, ops)
	require.NoError(t, err)
	assert.Equal(t, "new.com", obj["proxy"].(map[string]any)["host"])
}

func TestApplyPatch_AddRemove(t *testing.T) {
	obj := map[string]any{}
	addOps := []PatchOp{{Op: "add", Path: "/email", Value: map[string]any{"host": "smtp.com"}}}
	err := applyPatch(obj, addOps)
	require.NoError(t, err)
	assert.NotNil(t, obj["email"])

	removeOps := []PatchOp{{Op: "remove", Path: "/email"}}
	err = applyPatch(obj, removeOps)
	require.NoError(t, err)
	assert.Nil(t, obj["email"])
}

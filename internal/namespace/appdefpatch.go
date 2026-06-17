package namespace

import (
	"encoding/json"
	"fmt"

	"github.com/citeck/citeck-launcher/internal/appdef"
)

// appdefpatch.go — diff/apply for ApplicationDef edits as a JSON merge patch
// over the def's canonical JSON. AppDef has no comments and its YAML field order
// comes from the struct encoder, so the plain data engine (structmerge) is used
// directly — no yaml.Node needed here. Runtime caches (ImageDigest,
// VolumesContentHash) are stripped on both sides so they never enter the patch.

func toTree(d appdef.ApplicationDef) (map[string]any, error) {
	d.ImageDigest = ""
	d.VolumesContentHash = ""
	raw, err := json.Marshal(d)
	if err != nil {
		return nil, fmt.Errorf("marshal appdef: %w", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("unmarshal appdef tree: %w", err)
	}
	return m, nil
}

// DiffAppDef returns a sparse merge patch turning base into edited, or nil if
// they are equal once runtime caches are ignored.
func DiffAppDef(base, edited appdef.ApplicationDef) (json.RawMessage, error) {
	bt, err := toTree(base)
	if err != nil {
		return nil, err
	}
	et, err := toTree(edited)
	if err != nil {
		return nil, err
	}
	patch := DiffTree(bt, et)
	if patch == nil {
		return nil, nil
	}
	raw, err := json.Marshal(patch)
	if err != nil {
		return nil, fmt.Errorf("marshal patch: %w", err)
	}
	return raw, nil
}

// ApplyAppDefPatch overlays patch onto base (a freshly generated def) and
// returns the merged ApplicationDef. A nil/empty patch returns base unchanged.
// Runtime caches are taken from base (the patch never touches them).
func ApplyAppDefPatch(base appdef.ApplicationDef, patch json.RawMessage) (appdef.ApplicationDef, error) {
	if len(patch) == 0 {
		return base, nil
	}
	bt, err := toTree(base)
	if err != nil {
		return base, err
	}
	var pt any
	if err := json.Unmarshal(patch, &pt); err != nil {
		return base, fmt.Errorf("unmarshal patch: %w", err)
	}
	merged := ApplyTree(bt, pt)
	raw, err := json.Marshal(merged)
	if err != nil {
		return base, fmt.Errorf("marshal merged: %w", err)
	}
	out := base
	out.ImageDigest = ""
	out.VolumesContentHash = ""
	if err := json.Unmarshal(raw, &out); err != nil {
		return base, fmt.Errorf("unmarshal merged appdef: %w", err)
	}
	out.ImageDigest = base.ImageDigest
	out.VolumesContentHash = base.VolumesContentHash
	return out, nil
}

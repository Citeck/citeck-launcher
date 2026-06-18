package namespace

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/citeck/citeck-launcher/internal/appdef"
)

// appdefpatch.go — diff/apply for ApplicationDef edits, expressed as a SHALLOW
// (top-level-field) JSON merge: the patch maps each changed top-level field name
// to its whole new value (or null to delete). On apply, untouched fields flow
// from the freshly generated def (so an auto-generated image bump reaches an app
// whose resources/env the operator edited), while a touched field is taken
// wholesale.
//
// Shallow (not deep) is deliberate: it lets map-valued fields like Environments
// keep the operator's exact KEY ORDER. A deep/JSON-merge-patch would route the
// map through an unordered Go map and alphabetize it; storing the whole field as
// a raw JSON fragment preserves the OrderedMap's order byte-for-byte. The only
// trade-off vs a deep merge — if generation and the operator both change the
// same top-level object field, the operator's whole field wins (no sub-field
// merge) — matches the sticky-override policy.
//
// Runtime caches (ImageDigest, VolumesContentHash) are stripped before diffing
// so they never enter the patch.

// defFields marshals a def to its top-level fields as raw JSON fragments. Each
// fragment's bytes are deterministic (struct marshal), and for Environments the
// bytes reflect the OrderedMap's order — so byte equality means "same value AND
// same order".
func defFields(d appdef.ApplicationDef) (map[string]json.RawMessage, error) {
	d.ImageDigest = ""
	d.VolumesContentHash = ""
	raw, err := json.Marshal(d)
	if err != nil {
		return nil, fmt.Errorf("marshal appdef: %w", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("split appdef fields: %w", err)
	}
	return m, nil
}

// DiffAppDef returns a shallow patch turning base into edited, or nil if equal.
func DiffAppDef(base, edited appdef.ApplicationDef) (json.RawMessage, error) {
	bf, err := defFields(base)
	if err != nil {
		return nil, err
	}
	ef, err := defFields(edited)
	if err != nil {
		return nil, err
	}
	patch := map[string]json.RawMessage{}
	for k, ev := range ef {
		bv, ok := bf[k]
		if !ok || !bytes.Equal(bv, ev) {
			patch[k] = ev
		}
	}
	for k := range bf {
		if _, ok := ef[k]; !ok {
			patch[k] = json.RawMessage("null")
		}
	}
	if len(patch) == 0 {
		return nil, nil
	}
	raw, err := json.Marshal(patch)
	if err != nil {
		return nil, fmt.Errorf("marshal patch: %w", err)
	}
	return raw, nil
}

// ApplyAppDefPatch overlays a shallow patch onto base (a freshly generated def)
// and returns the merged ApplicationDef. A nil/empty patch returns base
// unchanged. ImageDigest is left empty (a changed image makes the base digest
// stale — the caller re-resolves it); VolumesContentHash is kept from base
// (generator-computed for the effective bind-mount content).
func ApplyAppDefPatch(base appdef.ApplicationDef, patch json.RawMessage) (appdef.ApplicationDef, error) {
	if len(patch) == 0 {
		return base, nil
	}
	bf, err := defFields(base)
	if err != nil {
		return base, err
	}
	var pf map[string]json.RawMessage
	if err = json.Unmarshal(patch, &pf); err != nil {
		return base, fmt.Errorf("unmarshal patch: %w", err)
	}
	for k, v := range pf {
		if len(v) == 0 || string(bytes.TrimSpace(v)) == "null" {
			delete(bf, k)
			continue
		}
		bf[k] = v
	}
	merged, err := json.Marshal(bf)
	if err != nil {
		return base, fmt.Errorf("marshal merged: %w", err)
	}
	// Decode into a FRESH struct, never `out := base`: json.Unmarshal reuses any
	// non-nil pointer/slice already present in the decode target, so unmarshaling
	// into a copy of base writes the patched values straight INTO base's own
	// nested pointers (probe pointers, startupConditions/initContainers elements,
	// environments, …) — mutating the caller's base in place. handleGetAppConfig
	// passes the generated def as BOTH the baseline and the ApplyAppDefPatch
	// input, so that aliasing silently rewrote the baseline to equal the patched
	// content, killing the change-gutter diff for every field reached through a
	// pointer or slice (top-level scalars copied by value still diffed). `merged`
	// already carries every json-tagged field of base, so a zero-value target
	// reconstructs the full def; only json:"-" fields must be restored by hand.
	var out appdef.ApplicationDef
	if err := json.Unmarshal(merged, &out); err != nil {
		return base, fmt.Errorf("unmarshal merged appdef: %w", err)
	}
	out.IsInit = base.IsInit // json:"-" — absent from the marshaled form
	out.ImageDigest = ""
	out.VolumesContentHash = base.VolumesContentHash
	return out, nil
}

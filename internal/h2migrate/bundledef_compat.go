package h2migrate

import (
	"encoding/json"
	"fmt"

	"github.com/citeck/citeck-launcher/internal/bundle"
)

// Why a translation layer: Kotlin's BundleDef differs from Go's wire shape in
// one load-bearing way that would silently produce an empty Key on import:
//   - `key` is a Kotlin BundleKey value class serialized via @JsonValue +
//     toString() as a JSON STRING (e.g. "release/2025.1.0"); Go's bundle.Def
//     models it as an object {"version": "..."}. Launchers before 1.3.6 had
//     no @JsonValue on BundleKey and wrote it as {"rawKey": "..."}; both
//     shapes are accepted.
//
// applications (Map<String, BundleAppDef>), citeckApps (List<BundleAppDef>),
// and content (DataValue → JSON object) line up byte-for-byte between Kotlin
// and Go: Kotlin's BundleAppDef has the single `image` field, DataValue.
// createObj() serializes as a plain JSON object, and Go's bundle.AppDef
// decodes both cleanly. Go's AppDef also carries `Images` (the dependency
// ladder), which Kotlin's BundleAppDef has no equivalent of — harmless here
// because the Kotlin JSON never has an `images` key, so it decodes as nil,
// but the two types are no longer a byte-for-byte match; only their `image`
// field is.
//
// We keep bundle.Def's field shape unchanged (it is the runtime contract that
// the resolver + state machine + persisted state all agree on) and translate
// at the migration boundary.

// kotlinBundleDef mirrors the Jackson-serialized shape produced by Kotlin's
// BundleDef data class. Only fields we forward into Go are listed.
type kotlinBundleDef struct {
	// Key is a string in the Kotlin wire format (BundleKey.@JsonValue toString
	// → rawKey), or {"rawKey": "..."} before 1.3.6. Go's bundle.Def stores it
	// as an object so we translate here.
	Key          kotlinBundleKey          `json:"key"`
	Applications map[string]bundle.AppDef `json:"applications"`
	CiteckApps   []bundle.AppDef          `json:"citeckApps"`
	Content      map[string]any           `json:"content"`
}

// kotlinBundleKey reads both wire shapes of Kotlin's BundleKey.
type kotlinBundleKey string

func (k *kotlinBundleKey) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		*k = kotlinBundleKey(s)
		return nil
	}
	var obj struct {
		RawKey *string `json:"rawKey"`
	}
	if err := json.Unmarshal(data, &obj); err != nil || obj.RawKey == nil {
		return fmt.Errorf("bundle key is neither a string nor {\"rawKey\": ...}: %s", data)
	}
	*k = kotlinBundleKey(*obj.RawKey)
	return nil
}

// decodeKotlinBundleDef parses a Jackson-shaped BundleDef JSON blob into Go's
// bundle.Def. The Go contract is preserved verbatim; only the wire shape is
// rewritten. Empty input (nil / {}) returns an empty Def without error so
// callers can treat absence as "no cache" rather than a parse failure.
func decodeKotlinBundleDef(data []byte) (bundle.Def, error) {
	if len(data) == 0 {
		return bundle.EmptyDef, nil
	}
	var k kotlinBundleDef
	if err := json.Unmarshal(data, &k); err != nil {
		return bundle.Def{}, fmt.Errorf("unmarshal kotlin bundledef: %w", err)
	}
	out := bundle.Def{
		Key:          bundle.Key{Version: string(k.Key)},
		Applications: k.Applications,
		CiteckApps:   k.CiteckApps,
		Content:      k.Content,
	}
	if out.Applications == nil {
		out.Applications = make(map[string]bundle.AppDef)
	}
	return out, nil
}

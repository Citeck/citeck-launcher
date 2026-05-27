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
//     models it as an object {"version": "..."}.
//
// applications (Map<String, BundleAppDef>), citeckApps (List<BundleAppDef>),
// and content (DataValue → JSON object) line up byte-for-byte between Kotlin
// and Go because BundleAppDef has the single `image` field on both sides and
// DataValue.createObj() serializes as a plain JSON object.
//
// We keep bundle.Def's field shape unchanged (it is the runtime contract that
// the resolver + state machine + persisted state all agree on) and translate
// at the migration boundary.

// kotlinBundleDef mirrors the Jackson-serialized shape produced by Kotlin's
// BundleDef data class. Only fields we forward into Go are listed.
type kotlinBundleDef struct {
	// Key is a string in the Kotlin wire format (BundleKey.@JsonValue toString
	// → rawKey). Go's bundle.Def stores it as an object so we translate here.
	Key          string                   `json:"key"`
	Applications map[string]bundle.AppDef `json:"applications"`
	CiteckApps   []bundle.AppDef          `json:"citeckApps"`
	Content      map[string]any           `json:"content"`
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
		Key:          bundle.Key{Version: k.Key},
		Applications: k.Applications,
		CiteckApps:   k.CiteckApps,
		Content:      k.Content,
	}
	if out.Applications == nil {
		out.Applications = make(map[string]bundle.AppDef)
	}
	return out, nil
}

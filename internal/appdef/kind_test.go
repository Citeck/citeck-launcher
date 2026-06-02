package appdef

import (
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestApplicationKindYAML verifies the config editor sees readable enum names
// (not opaque integers) and that both the string form and the legacy numeric
// form parse back to the same kind.
func TestApplicationKindYAML(t *testing.T) {
	cases := []struct {
		kind ApplicationKind
		name string
	}{
		{KindCiteckCore, "CITECK_CORE"},
		{KindCiteckCoreExtension, "CITECK_CORE_EXTENSION"},
		{KindCiteckAdditional, "CITECK_ADDITIONAL"},
		{KindThirdParty, "THIRD_PARTY"},
	}
	for _, c := range cases {
		out, err := yaml.Marshal(ApplicationDef{Name: "x", Image: "i", Kind: c.kind})
		if err != nil {
			t.Fatalf("marshal %v: %v", c.kind, err)
		}
		if !strings.Contains(string(out), "kind: "+c.name) {
			t.Errorf("kind %v: want %q in\n%s", c.kind, "kind: "+c.name, out)
		}

		// String form round-trips.
		var fromStr ApplicationDef
		if err := yaml.Unmarshal(out, &fromStr); err != nil {
			t.Fatalf("unmarshal string %q: %v", c.name, err)
		}
		if fromStr.Kind != c.kind {
			t.Errorf("string %q parsed to %v, want %v", c.name, fromStr.Kind, c.kind)
		}

		// Legacy numeric form still parses (back-compat with old configs).
		var fromNum ApplicationDef
		numeric := "name: x\nimage: i\nkind: " + strconv.Itoa(int(c.kind)) + "\n"
		if err := yaml.Unmarshal([]byte(numeric), &fromNum); err != nil {
			t.Fatalf("unmarshal numeric %d: %v", int(c.kind), err)
		}
		if fromNum.Kind != c.kind {
			t.Errorf("numeric %d parsed to %v, want %v", int(c.kind), fromNum.Kind, c.kind)
		}
	}
}

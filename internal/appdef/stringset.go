package appdef

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"

	"gopkg.in/yaml.v3"
)

// StringSet is an INSERTION-ORDERED set of strings — the Go analog of Kotlin's
// LinkedHashSet, used for an app's dependsOn. It is a named string slice, so it
// marshals naturally as an ordered YAML/JSON list (matching the Kotlin 1.x
// Set<String> wire form), and `omitempty` drops it when empty. The always-true
// value the old 2.x map[string]bool carried is gone — it never meant anything.
//
// Order is preserved as inserted (first occurrence wins); Add dedups. Membership
// is a linear scan, fine for the handful of deps an app declares.
//
// Unmarshal is lenient by design: it accepts BOTH the list form and the legacy
// {"name": true} object form that 2.x briefly persisted into state files and the
// desktop SQLite state_json column, so old data keeps loading without a
// migration. A legacy map has no order to preserve, so its keys are taken
// sorted; the value becomes a list on the next save.
type StringSet []string

// NewStringSet builds an ordered set from names (duplicates collapse, first
// occurrence wins).
func NewStringSet(names ...string) StringSet {
	s := make(StringSet, 0, len(names))
	for _, n := range names {
		s.Add(n)
	}
	return s
}

// Add appends name unless already present, preserving insertion order.
func (s *StringSet) Add(name string) {
	if s.Has(name) {
		return
	}
	*s = append(*s, name)
}

// Has reports whether name is in the set (safe on a nil set).
func (s StringSet) Has(name string) bool {
	return slices.Contains(s, name)
}

// UnmarshalJSON accepts the list form (preferred) or the legacy
// {"name": true} object form.
func (s *StringSet) UnmarshalJSON(data []byte) error {
	var list []string
	if err := json.Unmarshal(data, &list); err == nil {
		*s = NewStringSet(list...)
		return nil
	}
	var obj map[string]bool
	if err := json.Unmarshal(data, &obj); err != nil {
		return fmt.Errorf("dependsOn: expected a list of names or a name→bool object: %w", err)
	}
	*s = setFromBoolMap(obj)
	return nil
}

// UnmarshalYAML accepts the list form (preferred) or the legacy
// {name: true} mapping form.
func (s *StringSet) UnmarshalYAML(value *yaml.Node) error {
	var list []string
	if err := value.Decode(&list); err == nil {
		*s = NewStringSet(list...)
		return nil
	}
	var obj map[string]bool
	if err := value.Decode(&obj); err != nil {
		return fmt.Errorf("dependsOn: expected a list of names or a name→bool mapping: %w", err)
	}
	*s = setFromBoolMap(obj)
	return nil
}

// setFromBoolMap keeps the truthy keys of the legacy bool map, sorted (the map
// itself carries no order to preserve).
func setFromBoolMap(obj map[string]bool) StringSet {
	out := make(StringSet, 0, len(obj))
	for k, v := range obj {
		if v {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

package appdef

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"

	"gopkg.in/yaml.v3"
)

// OrderedMap is an INSERTION-ORDERED string→string map — the Go analog of
// Kotlin's LinkedHashMap, used for an app's Environments so the key order the
// operator wrote in the config editor survives view/edit (a plain Go map sorts
// its keys on marshal and loses that order).
//
// It is a named slice of key/value entries so `omitempty` drops it when empty,
// exactly like StringSet. Custom (Un)Marshal emit/consume a YAML/JSON OBJECT
// (not an array) in stored order. Unmarshal is lenient: a YAML mapping or a JSON
// object is read in source order, so old persisted data (which serialized from
// an unordered Go map, i.e. sorted) keeps loading and simply re-orders on the
// next save once the operator arranges it.
type OrderedMap []OrderedEntry

// OrderedEntry is a single key/value pair of an OrderedMap.
type OrderedEntry struct {
	Key   string
	Value string
}

// OrderedMapFromMap builds an OrderedMap from a plain map, keys sorted (the map
// carries no order to preserve). Handy at generation sites that source env from
// a Go map and want deterministic output.
func OrderedMapFromMap(m map[string]string) OrderedMap {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make(OrderedMap, 0, len(keys))
	for _, k := range keys {
		out = append(out, OrderedEntry{Key: k, Value: m[k]})
	}
	return out
}

// Get returns the value for key and whether it was present.
func (m OrderedMap) Get(key string) (string, bool) {
	for _, e := range m {
		if e.Key == key {
			return e.Value, true
		}
	}
	return "", false
}

// Has reports whether key is present.
func (m OrderedMap) Has(key string) bool {
	_, ok := m.Get(key)
	return ok
}

// Set updates key in place if present, else appends it (preserving order).
func (m *OrderedMap) Set(key, value string) {
	for i := range *m {
		if (*m)[i].Key == key {
			(*m)[i].Value = value
			return
		}
	}
	*m = append(*m, OrderedEntry{Key: key, Value: value})
}

// Len returns the number of entries.
func (m OrderedMap) Len() int { return len(m) }

// ToMap returns a plain unordered copy (for callers that only do lookups).
func (m OrderedMap) ToMap() map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for _, e := range m {
		out[e.Key] = e.Value
	}
	return out
}

// MarshalJSON emits a JSON object in stored order.
func (m OrderedMap) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, e := range m {
		if i > 0 {
			b.WriteByte(',')
		}
		k, err := json.Marshal(e.Key)
		if err != nil {
			return nil, fmt.Errorf("environments key: %w", err)
		}
		v, err := json.Marshal(e.Value)
		if err != nil {
			return nil, fmt.Errorf("environments value: %w", err)
		}
		b.Write(k)
		b.WriteByte(':')
		b.Write(v)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// UnmarshalJSON reads a JSON object preserving key order. Non-string scalar
// values are coerced to their textual form (env values are conceptually
// strings; older data or hand-edits may carry a bare number/bool).
func (m *OrderedMap) UnmarshalJSON(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return fmt.Errorf("environments: %w", err)
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		// Accept JSON null (omitempty round-trips) as empty.
		if string(bytes.TrimSpace(data)) == "null" {
			*m = nil
			return nil
		}
		return fmt.Errorf("environments: expected a JSON object")
	}
	var out OrderedMap
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return fmt.Errorf("environments key: %w", err)
		}
		key, _ := kt.(string)
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return fmt.Errorf("environments value for %q: %w", key, err)
		}
		out = append(out, OrderedEntry{Key: key, Value: jsonScalarToString(raw)})
	}
	*m = out
	return nil
}

func jsonScalarToString(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(bytes.TrimSpace(raw)) // number / bool / etc. → textual form
}

// MarshalYAML emits a YAML mapping node in stored order.
func (m OrderedMap) MarshalYAML() (any, error) {
	node := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for _, e := range m {
		node.Content = append(node.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: e.Key},
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: e.Value})
	}
	return node, nil
}

// UnmarshalYAML reads a YAML mapping preserving key order. Scalar values are
// coerced to string (matching the prior map[string]string behavior).
func (m *OrderedMap) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.MappingNode {
		if value.Tag == "!!null" {
			*m = nil
			return nil
		}
		return fmt.Errorf("environments: expected a mapping")
	}
	out := make(OrderedMap, 0, len(value.Content)/2)
	for i := 0; i+1 < len(value.Content); i += 2 {
		var key, val string
		if err := value.Content[i].Decode(&key); err != nil {
			return fmt.Errorf("environments key: %w", err)
		}
		if err := value.Content[i+1].Decode(&val); err != nil {
			return fmt.Errorf("environments value for %q: %w", key, err)
		}
		out = append(out, OrderedEntry{Key: key, Value: val})
	}
	*m = out
	return nil
}

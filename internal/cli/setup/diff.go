package setup

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// PatchOp represents a single JSON Patch operation (RFC 6902).
type PatchOp struct {
	Op    string // "add", "remove", "replace"
	Path  string // JSON Pointer (RFC 6901), e.g. "/proxy/host"
	Value any    // used by "add" and "replace"
}

// computePatch returns forward and reverse JSON Patch operation slices
// that describe the difference between before and after.
// Output is deterministic (keys sorted alphabetically).
func computePatch(before, after map[string]any) (forward, reverse []PatchOp) {
	diffMaps("", before, after, &forward, &reverse)
	return forward, reverse
}

// diffMaps recursively compares two maps at the given JSON Pointer prefix
// and appends patch operations to fwd/rev.
func diffMaps(prefix string, before, after map[string]any, fwd, rev *[]PatchOp) {
	// Collect all keys from both maps, sorted for determinism.
	keySet := make(map[string]struct{})
	for k := range before {
		keySet[k] = struct{}{}
	}
	for k := range after {
		keySet[k] = struct{}{}
	}
	keys := make([]string, 0, len(keySet))
	for k := range keySet {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		path := prefix + "/" + escapeJSONPointer(k)
		bVal, bOk := before[k]
		aVal, aOk := after[k]

		switch {
		case bOk && !aOk:
			// Key only in before → remove forward, add reverse.
			*fwd = append(*fwd, PatchOp{Op: "remove", Path: path})
			*rev = append(*rev, PatchOp{Op: "add", Path: path, Value: bVal})

		case !bOk && aOk:
			// Key only in after → add forward, remove reverse.
			*fwd = append(*fwd, PatchOp{Op: "add", Path: path, Value: aVal})
			*rev = append(*rev, PatchOp{Op: "remove", Path: path})

		default:
			// Key in both — check if values differ.
			bMap, bIsMap := bVal.(map[string]any)
			aMap, aIsMap := aVal.(map[string]any)

			if bIsMap && aIsMap {
				// Both are objects → recurse.
				diffMaps(path, bMap, aMap, fwd, rev)
			} else if !jsonEqual(bVal, aVal) {
				// Scalar or type changed → replace.
				*fwd = append(*fwd, PatchOp{Op: "replace", Path: path, Value: aVal})
				*rev = append(*rev, PatchOp{Op: "replace", Path: path, Value: bVal})
			}
		}
	}
}

// applyPatch applies a sequence of JSON Patch operations to obj in-place.
func applyPatch(obj map[string]any, ops []PatchOp) error {
	for _, op := range ops {
		segments, err := splitJSONPointer(op.Path)
		if err != nil {
			return fmt.Errorf("invalid JSON Pointer %q: %w", op.Path, err)
		}
		if len(segments) == 0 {
			return fmt.Errorf("patch op %q on root document not supported", op.Op)
		}

		key := segments[len(segments)-1]
		parent, err := navigateTo(obj, segments[:len(segments)-1], op.Op == "add")
		if err != nil {
			return fmt.Errorf("navigating to %q: %w", op.Path, err)
		}

		switch op.Op {
		case "add", "replace":
			parent[key] = op.Value
		case "remove":
			delete(parent, key)
		default:
			return fmt.Errorf("unsupported patch op %q", op.Op)
		}
	}
	return nil
}

// navigateTo walks segments in obj, optionally creating intermediate maps.
func navigateTo(obj map[string]any, segments []string, createMissing bool) (map[string]any, error) {
	cur := obj
	for _, seg := range segments {
		child, ok := cur[seg]
		if !ok {
			if !createMissing {
				return nil, fmt.Errorf("key %q not found", seg)
			}
			next := make(map[string]any)
			cur[seg] = next
			cur = next
			continue
		}
		next, ok := child.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("key %q is not an object", seg)
		}
		cur = next
	}
	return cur, nil
}

// splitJSONPointer splits a JSON Pointer string (RFC 6901) into unescaped segments.
// An empty string or "/" returns an empty slice (root).
func splitJSONPointer(ptr string) ([]string, error) {
	if ptr == "" {
		return nil, nil
	}
	if !strings.HasPrefix(ptr, "/") {
		return nil, fmt.Errorf("JSON Pointer must start with '/'")
	}
	parts := strings.Split(ptr[1:], "/")
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = unescapeJSONPointer(p)
	}
	return out, nil
}

// escapeJSONPointer escapes a single key segment per RFC 6901.
// ~ → ~0, / → ~1.
func escapeJSONPointer(s string) string {
	s = strings.ReplaceAll(s, "~", "~0")
	s = strings.ReplaceAll(s, "/", "~1")
	return s
}

// unescapeJSONPointer reverses escapeJSONPointer.
func unescapeJSONPointer(s string) string {
	s = strings.ReplaceAll(s, "~1", "/")
	s = strings.ReplaceAll(s, "~0", "~")
	return s
}

// jsonEqual compares two values for deep equality using JSON semantics.
// Both inputs must come from JSON round-trip (all numbers are float64).
func jsonEqual(a, b any) bool {
	return reflect.DeepEqual(a, b)
}

// structToJSONMap converts any Go struct (or value) to map[string]any
// via JSON marshal/unmarshal. All numeric types become float64.
func structToJSONMap(v any) (map[string]any, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}
	return out, nil
}

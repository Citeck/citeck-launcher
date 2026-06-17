package namespace

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/sergi/go-diff/diffmatchpatch"
	"gopkg.in/yaml.v3"
)

// filemerge.go — stores a mounted-file edit as a delta over the generated
// template and re-applies it after the template changes. Structural files
// (yaml/json) use the data-delta engine + yaml.Node apply (order + comments);
// everything else uses a unified-diff overlay with keep-current fallback.

// FileEdit is the persisted per-file delta. For "structural", Payload is the
// JSON merge patch; for "textual", Payload is a JSON-quoted dmp patch string.
type FileEdit struct {
	Format  string          `json:"format"`
	Payload json.RawMessage `json:"payload"`
}

func isStructuralFile(filename string) bool {
	l := strings.ToLower(filename)
	return strings.HasSuffix(l, ".yml") || strings.HasSuffix(l, ".yaml") || strings.HasSuffix(l, ".json")
}

func isJSONFile(filename string) bool { return strings.HasSuffix(strings.ToLower(filename), ".json") }

// MakeFileEdit computes the stored delta from the generated template to the
// user's edited content.
func MakeFileEdit(filename string, template, edited []byte) (FileEdit, error) {
	if isStructuralFile(filename) {
		bt, err := decodeStructural(template)
		if err != nil {
			return textualEdit(template, edited) // malformed template → fall back to textual
		}
		et, err := decodeStructural(edited)
		if err != nil {
			return FileEdit{}, fmt.Errorf("decode edited %s: %w", filename, err)
		}
		patch := DiffTree(bt, et)
		raw, err := json.Marshal(patch)
		if err != nil {
			return FileEdit{}, fmt.Errorf("marshal file patch: %w", err)
		}
		return FileEdit{Format: "structural", Payload: raw}, nil
	}
	return textualEdit(template, edited)
}

func textualEdit(template, edited []byte) (FileEdit, error) {
	dmp := diffmatchpatch.New()
	patches := dmp.PatchMake(string(template), string(edited))
	raw, err := json.Marshal(dmp.PatchToText(patches))
	if err != nil {
		return FileEdit{}, fmt.Errorf("marshal textual patch: %w", err)
	}
	return FileEdit{Format: "textual", Payload: raw}, nil
}

// ApplyFileEdit merges the stored edit onto the (possibly changed) template.
// `current` is the last on-disk content: the YAML comment source for structural
// files and the textual conflict fallback.
func ApplyFileEdit(filename string, edit FileEdit, template, current []byte) ([]byte, error) {
	switch edit.Format {
	case "structural":
		if _, err := decodeStructural(template); err != nil {
			return current, nil // template no longer parses → keep current
		}
		var patch any
		if err := json.Unmarshal(edit.Payload, &patch); err != nil {
			return nil, fmt.Errorf("unmarshal structural payload: %w", err)
		}
		patch = normalizeYaml(patch)
		if isJSONFile(filename) {
			return applyStructuralJSON(template, patch, current)
		}
		return applyDeltaToYAML(template, patch, current)
	case "textual":
		var patchText string
		if err := json.Unmarshal(edit.Payload, &patchText); err != nil {
			return nil, fmt.Errorf("unmarshal textual payload: %w", err)
		}
		dmp := diffmatchpatch.New()
		patches, err := dmp.PatchFromText(patchText)
		if err != nil {
			return nil, fmt.Errorf("parse textual patch: %w", err)
		}
		result, applied := dmp.PatchApply(patches, string(template))
		for _, ok := range applied {
			if !ok {
				return current, nil // any failed hunk → keep current (sticky, no markers)
			}
		}
		return []byte(result), nil
	default:
		return current, nil
	}
}

func decodeStructural(data []byte) (map[string]any, error) {
	var v any
	if err := yaml.Unmarshal(data, &v); err != nil { // yaml.v3 parses JSON too
		return nil, fmt.Errorf("parse structural file: %w", err)
	}
	n := normalizeYaml(v)
	m, ok := n.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("not a mapping document")
	}
	return m, nil
}

// applyStructuralJSON applies the delta to a JSON file. JSON has no comments;
// order is best-effort (Go map). Preserves a trailing newline.
func applyStructuralJSON(template []byte, patch any, current []byte) ([]byte, error) {
	bt, err := decodeStructural(template)
	if err != nil {
		return current, nil
	}
	merged := ApplyTree(bt, patch)
	raw, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal json: %w", err)
	}
	if bytes.HasSuffix(current, []byte("\n")) || bytes.HasSuffix(template, []byte("\n")) {
		raw = append(raw, '\n')
	}
	return raw, nil
}

// normalizeYaml converts yaml.v3's map[any]any to map[string]any so the data
// engine (string-keyed) operates correctly. It also passes through values
// already decoded from JSON (map[string]any).
func normalizeYaml(v any) any {
	switch m := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, val := range m {
			out[k] = normalizeYaml(val)
		}
		return out
	case map[any]any:
		out := map[string]any{}
		for k, val := range m {
			out[fmt.Sprintf("%v", k)] = normalizeYaml(val)
		}
		return out
	case []any:
		out := make([]any, len(m))
		for i := range m {
			out[i] = normalizeYaml(m[i])
		}
		return out
	default:
		return v
	}
}

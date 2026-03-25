package form

import (
	"testing"
)

func TestValidate_Required(t *testing.T) {
	spec := &FormSpec{
		Components: []ComponentSpec{
			{Key: "name", Label: "Name", Type: "text", Required: true},
		},
	}

	// Missing field
	errs := Validate(spec, map[string]any{})
	if len(errs) != 1 || errs[0].Key != "name" {
		t.Errorf("expected 1 error on 'name', got %v", errs)
	}

	// Empty string
	errs = Validate(spec, map[string]any{"name": ""})
	if len(errs) != 1 {
		t.Errorf("expected 1 error for empty string, got %v", errs)
	}

	// Whitespace only
	errs = Validate(spec, map[string]any{"name": "  "})
	if len(errs) != 1 {
		t.Errorf("expected 1 error for whitespace, got %v", errs)
	}

	// Valid
	errs = Validate(spec, map[string]any{"name": "test"})
	if len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
}

func TestValidate_MinMaxLength(t *testing.T) {
	spec := &FormSpec{
		Components: []ComponentSpec{
			{
				Key: "name", Label: "Name", Type: "text",
				Validations: []ValidationRule{
					{Type: "minLength", Value: 3},
					{Type: "maxLength", Value: 10},
				},
			},
		},
	}

	errs := Validate(spec, map[string]any{"name": "ab"})
	if len(errs) != 1 {
		t.Errorf("expected 1 error for too short, got %v", errs)
	}

	errs = Validate(spec, map[string]any{"name": "12345678901"})
	if len(errs) != 1 {
		t.Errorf("expected 1 error for too long, got %v", errs)
	}

	errs = Validate(spec, map[string]any{"name": "valid"})
	if len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
}

func TestValidate_Pattern(t *testing.T) {
	spec := &FormSpec{
		Components: []ComponentSpec{
			{
				Key: "name", Label: "Name", Type: "text",
				Validations: []ValidationRule{
					{Type: "pattern", Value: `^[a-z]+$`, Message: "lowercase only"},
				},
			},
		},
	}

	errs := Validate(spec, map[string]any{"name": "ABC"})
	if len(errs) != 1 || errs[0].Message != "lowercase only" {
		t.Errorf("expected pattern error, got %v", errs)
	}

	errs = Validate(spec, map[string]any{"name": "abc"})
	if len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
}

func TestValidate_MinMax(t *testing.T) {
	spec := &FormSpec{
		Components: []ComponentSpec{
			{
				Key: "port", Label: "Port", Type: "number",
				Validations: []ValidationRule{
					{Type: "min", Value: 1},
					{Type: "max", Value: 65535},
				},
			},
		},
	}

	errs := Validate(spec, map[string]any{"port": float64(0)})
	if len(errs) != 1 {
		t.Errorf("expected 1 error for port 0, got %v", errs)
	}

	errs = Validate(spec, map[string]any{"port": float64(70000)})
	if len(errs) != 1 {
		t.Errorf("expected 1 error for port 70000, got %v", errs)
	}

	errs = Validate(spec, map[string]any{"port": float64(8080)})
	if len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
}

func TestValidate_OptionalField(t *testing.T) {
	spec := &FormSpec{
		Components: []ComponentSpec{
			{Key: "host", Label: "Host", Type: "text", Required: false},
		},
	}

	// Missing optional field — no error
	errs := Validate(spec, map[string]any{})
	if len(errs) != 0 {
		t.Errorf("expected no errors for missing optional field, got %v", errs)
	}
}

func TestValidate_NamespaceCreateSpec(t *testing.T) {
	spec := NamespaceCreateSpec()

	// Empty data — only required "name" should fail
	errs := Validate(spec, map[string]any{})
	if len(errs) != 1 || errs[0].Key != "name" {
		t.Errorf("expected 1 error on 'name', got %v", errs)
	}

	// Valid minimum data
	errs = Validate(spec, map[string]any{"name": "test-ns"})
	if len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}

	// Invalid name pattern
	errs = Validate(spec, map[string]any{"name": "-invalid"})
	if len(errs) != 1 {
		t.Errorf("expected 1 pattern error, got %v", errs)
	}
}

func TestGetSpec(t *testing.T) {
	spec := GetSpec(NamespaceCreateFormID)
	if spec == nil {
		t.Fatal("expected non-nil spec")
	}
	if spec.ID != NamespaceCreateFormID {
		t.Errorf("expected ID %q, got %q", NamespaceCreateFormID, spec.ID)
	}

	// Unknown ID
	if GetSpec("nonexistent") != nil {
		t.Error("expected nil for unknown form ID")
	}
}

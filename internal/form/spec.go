// Package form provides server-side form specifications and validation.
package form

// FormSpec defines a form's structure and validation rules.
type FormSpec struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Components []ComponentSpec `json:"components"`
}

// ComponentSpec defines a single form field.
type ComponentSpec struct {
	Key         string           `json:"key"`
	Label       string           `json:"label"`
	Type        string           `json:"type"` // "text", "number", "password", "select", "checkbox"
	Required    bool             `json:"required,omitempty"`
	Default     any              `json:"default,omitempty"`
	Options     []SelectOption   `json:"options,omitempty"` // for "select" type
	Validations []ValidationRule `json:"validations,omitempty"`
}

// SelectOption is a key-value pair for select fields.
type SelectOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// ValidationRule defines a single validation constraint.
type ValidationRule struct {
	Type    string `json:"type"`    // "minLength", "maxLength", "pattern", "min", "max"
	Value   any    `json:"value"`   // the constraint value
	Message string `json:"message"` // error message when validation fails
}

// FieldError describes a validation failure on a specific field.
type FieldError struct {
	Key     string `json:"key"`
	Message string `json:"message"`
}

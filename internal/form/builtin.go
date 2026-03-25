package form

// NamespaceCreateFormID is the form ID for namespace creation.
const NamespaceCreateFormID = "namespace-create"

// NamespaceCreateSpec returns the built-in form spec for namespace creation.
func NamespaceCreateSpec() *FormSpec {
	return &FormSpec{
		ID:   NamespaceCreateFormID,
		Name: "Create Namespace",
		Components: []ComponentSpec{
			{
				Key: "name", Label: "Name", Type: "text", Required: true,
				Validations: []ValidationRule{
					{Type: "minLength", Value: 1, Message: "Name is required"},
					{Type: "maxLength", Value: 64, Message: "Name must be at most 64 characters"},
					{Type: "pattern", Value: `^[a-zA-Z0-9][a-zA-Z0-9 _-]*$`, Message: "Name must start with a letter or digit and contain only letters, digits, spaces, hyphens, underscores"},
				},
			},
			{
				Key: "authType", Label: "Authentication", Type: "select",
				Default: "KEYCLOAK",
				Options: []SelectOption{
					{Value: "BASIC", Label: "Basic Auth"},
					{Value: "KEYCLOAK", Label: "Keycloak SSO"},
				},
			},
			{
				Key: "host", Label: "Host", Type: "text",
				Default: "localhost",
			},
			{
				Key: "port", Label: "Port", Type: "number",
				Default: 80,
				Validations: []ValidationRule{
					{Type: "min", Value: 1, Message: "Port must be between 1 and 65535"},
					{Type: "max", Value: 65535, Message: "Port must be between 1 and 65535"},
				},
			},
			{
				Key: "tlsEnabled", Label: "TLS Enabled", Type: "checkbox",
				Default: false,
			},
			{
				Key: "pgAdminEnabled", Label: "PgAdmin Enabled", Type: "checkbox",
				Default: false,
			},
			{
				Key: "bundleRepo", Label: "Bundle Repository", Type: "select",
				Default: "community",
			},
			{
				Key: "bundleKey", Label: "Bundle Version", Type: "text",
				Default: "LATEST",
			},
		},
	}
}

// builtinForms is the registry of built-in form specs.
var builtinForms = map[string]*FormSpec{
	NamespaceCreateFormID: NamespaceCreateSpec(),
}

// GetSpec returns a form spec by ID, or nil if not found.
func GetSpec(formID string) *FormSpec {
	return builtinForms[formID]
}

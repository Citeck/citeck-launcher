package bundle

import (
	"fmt"
	"strings"
)

// Ref is a reference to a bundle (repo:key format).
type Ref struct {
	Repo string `json:"repo" yaml:"repo"`
	Key  string `json:"key" yaml:"key"`
}

// EmptyRef is the zero-value Ref.
var EmptyRef = Ref{}

// ParseRef parses a "repo:key" string into a Ref.
func ParseRef(s string) (Ref, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return EmptyRef, nil
	}
	idx := strings.LastIndex(s, ":")
	if idx <= 0 || idx == len(s)-1 {
		return EmptyRef, fmt.Errorf("invalid bundle ref: %q (expected repo:key)", s)
	}
	return Ref{
		Repo: strings.TrimSpace(s[:idx]),
		Key:  strings.TrimSpace(s[idx+1:]),
	}, nil
}

// String returns the "repo:key" representation.
func (r Ref) String() string {
	if r.IsEmpty() {
		return ""
	}
	return r.Repo + ":" + r.Key
}

// IsEmpty reports whether the reference is unset.
func (r Ref) IsEmpty() bool {
	return r.Repo == "" && r.Key == ""
}

// MarshalYAML serializes Ref as a string "repo:key" (or "repo" if key is empty).
func (r Ref) MarshalYAML() (any, error) {
	return r.String(), nil
}

// UnmarshalYAML allows Ref to be deserialized from a string "repo:key".
func (r *Ref) UnmarshalYAML(unmarshal func(any) error) error {
	var s string
	if err := unmarshal(&s); err != nil {
		return err
	}
	parsed, err := ParseRef(s)
	if err != nil {
		return err
	}
	*r = parsed
	return nil
}

// Key represents a versioned bundle identifier.
type Key struct {
	Version string `json:"version" yaml:"version"`
}

// AppDef defines an app within a bundle.
type AppDef struct {
	Image string `json:"image" yaml:"image"`
}

// Def is a resolved bundle definition containing apps and metadata.
type Def struct {
	Key          Key               `json:"key" yaml:"key"`
	Applications map[string]AppDef `json:"applications" yaml:"applications"`
	CiteckApps   []AppDef          `json:"citeckApps,omitempty" yaml:"citeckApps,omitempty"`
	Content      map[string]any    `json:"content,omitempty" yaml:"content,omitempty"` // raw bundle YAML as map
}

// EmptyDef is a Def with no applications.
var EmptyDef = Def{
	Applications: make(map[string]AppDef),
}

// IsEmpty reports whether the bundle has no applications.
func (b *Def) IsEmpty() bool {
	return len(b.Applications) == 0 && len(b.CiteckApps) == 0
}

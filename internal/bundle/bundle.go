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
	// Images is the LADDER a `dependencies:` entry named, nil for every other
	// source. Image is its last rung.
	Images []string `json:"images,omitempty" yaml:"images,omitempty"`
}

// Def is a resolved bundle definition containing apps and metadata.
type Def struct {
	Key          Key               `json:"key" yaml:"key"`
	Applications map[string]AppDef `json:"applications" yaml:"applications"`
	// Dependencies carries the images a bundle declares in its `dependencies:`
	// section — third-party infrastructure (postgres, rabbitmq, zookeeper,
	// keycloak, mailpit, pgadmin, onlyoffice…) rather than Citeck apps.
	//
	// It is deliberately NOT merged into Applications, for two independent
	// reasons. (1) IsEmpty means "this bundle has no Citeck apps in it", which
	// is what raises the non-dismissible bundle-error banner; a bundle that
	// names only third-party images would otherwise look healthy while
	// starting seven containers with none of the product in them. (2) the
	// webapp loop admits bundle applications, and infra must never reach it.
	Dependencies map[string]AppDef `json:"dependencies,omitempty" yaml:"dependencies,omitempty"`
	CiteckApps   []AppDef          `json:"citeckApps,omitempty" yaml:"citeckApps,omitempty"`
	// MinLauncherVersion is the launcher version this bundle declares it needs,
	// as the author's own text ("2.13.0"). Empty means no requirement — both
	// when the key is absent and when it is blank, because an unreadable floor
	// compares as newer than every release (see update.Greater) and a blank one
	// would otherwise refuse everybody.
	//
	// Only launchers from the release that introduced the check enforce it;
	// every older launcher, Go and Kotlin alike, ignores the key. So it guards
	// nothing retroactively — see AGENTS.md.
	MinLauncherVersion string `json:"minLauncherVersion,omitempty" yaml:"minLauncherVersion,omitempty"`
	Content            map[string]any `json:"content,omitempty" yaml:"content,omitempty"` // raw bundle YAML as map
}

// EmptyDef is a Def with no applications.
var EmptyDef = Def{
	Applications: make(map[string]AppDef),
}

// IsEmpty reports whether the bundle has no applications.
func (b *Def) IsEmpty() bool {
	return len(b.Applications) == 0 && len(b.CiteckApps) == 0
}

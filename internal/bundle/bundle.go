package bundle

import (
	"fmt"
	"maps"
	"slices"
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

// ExperimentalApp is one entry of a bundle's top-level `experimental:` section:
// a module the release ships as BETA. It is an application in every respect
// but one — it is generated only for a namespace that opted into its Feature
// (namespace.yml `experimentalFeatures:`), and the UI marks it as beta.
//
// Feature groups the entries one switch turns on together (a service and its
// own database); the parser fills it with the app's own name when the author
// wrote none. Title and Description are the author's words for the dialog,
// untranslated like a custom link's category; Title falls back to the feature
// id. Both are optional and read off the FIRST app (by name) of the feature
// that carries one.
type ExperimentalApp struct {
	AppDef
	Feature     string `json:"feature" yaml:"feature"`
	Title       string `json:"title,omitempty" yaml:"title,omitempty"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
}

// ExperimentalFeature is one opt-in switch a bundle offers: the feature id a
// namespace stores, the words the dialog shows, and the apps the switch turns
// on. Built by Def.ExperimentalFeatures.
type ExperimentalFeature struct {
	ID          string
	Title       string
	Description string
	Apps        []string
}

// Def is a resolved bundle definition containing apps and metadata.
type Def struct {
	Key          Key               `json:"key" yaml:"key"`
	Applications map[string]AppDef `json:"applications" yaml:"applications"`
	// Experimental carries the modules a bundle declares in its top-level
	// `experimental:` section — BETA services a namespace may opt into.
	//
	// Like Dependencies it is deliberately NOT merged into Applications at parse
	// time: IsEmpty must keep meaning "no Citeck apps", and the webapp loop must
	// not see a module nobody switched on. WithExperimentalEnabled builds the
	// effective view a generation reads.
	//
	// The section is a compatibility contract of the same kind as
	// `dependencies:`: parseBundleFile hands every top-level key to processApp,
	// which skips a mapping with no direct `image:` key in silence — and the
	// section's children are app names, not an image. So every launcher that
	// predates this field never sees the beta modules at all, fail-closed, and a
	// release GRADUATES a module by moving its entry to the top level.
	Experimental map[string]ExperimentalApp `json:"experimental,omitempty" yaml:"experimental,omitempty"`
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
	MinLauncherVersion string         `json:"minLauncherVersion,omitempty" yaml:"minLauncherVersion,omitempty"`
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

// ExperimentalFeatures lists the opt-in switches this bundle offers, sorted by
// feature id, each with its apps sorted by name. A feature's Title and
// Description come from the first of its apps (by name) that carries one; a
// feature with no title anywhere is titled by its id, so the dialog never
// shows an empty row.
func (b *Def) ExperimentalFeatures() []ExperimentalFeature {
	if b == nil || len(b.Experimental) == 0 {
		return nil
	}
	byID := make(map[string]*ExperimentalFeature)
	for _, name := range slices.Sorted(maps.Keys(b.Experimental)) {
		app := b.Experimental[name]
		f, ok := byID[app.Feature]
		if !ok {
			f = &ExperimentalFeature{ID: app.Feature}
			byID[app.Feature] = f
		}
		f.Apps = append(f.Apps, name)
		if f.Title == "" {
			f.Title = app.Title
		}
		if f.Description == "" {
			f.Description = app.Description
		}
	}

	ids := slices.Sorted(maps.Keys(byID))
	out := make([]ExperimentalFeature, 0, len(ids))
	for _, id := range ids {
		f := byID[id]
		if f.Title == "" {
			f.Title = id
		}
		out = append(out, *f)
	}
	return out
}

// WithExperimentalEnabled is the bundle a generation reads: the regular
// applications plus the experimental modules whose feature is in `enabled`,
// admitted into Applications so every image resolver and the webapp loop see
// them as the release's own. `admitted` maps each admitted app to its feature,
// for the beta marker.
//
// The receiver is never mutated, and when nothing is admitted it is returned
// as is — a namespace with no features on generates from exactly the Def it
// always did. A module whose name a regular application already uses is not
// admitted: the top level wins, as it does at parse time.
func (b *Def) WithExperimentalEnabled(enabled map[string]bool) (effective *Def, admitted map[string]string) {
	if b == nil {
		return nil, nil
	}
	admitted = make(map[string]string)
	for name, app := range b.Experimental {
		if !enabled[app.Feature] {
			continue
		}
		if _, top := b.Applications[name]; top {
			continue
		}
		admitted[name] = app.Feature
	}
	if len(admitted) == 0 {
		return b, nil
	}
	eff := *b
	eff.Applications = make(map[string]AppDef, len(b.Applications)+len(admitted))
	maps.Copy(eff.Applications, b.Applications)
	for name := range admitted {
		eff.Applications[name] = b.Experimental[name].AppDef
	}
	return &eff, admitted
}

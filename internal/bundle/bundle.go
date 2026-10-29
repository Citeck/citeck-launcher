package bundle

import (
	"fmt"
	"maps"
	"regexp"
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

// ExperimentalFeatureDef is one entry of a bundle's top-level `experimental:`
// section — keyed by FEATURE id, it is one switch a namespace may turn on, and
// `apps:` are the modules that switch turns on together (a service and its own
// database). Each app is an application in every respect but one: it is
// generated only for a namespace that opted into the feature (namespace.yml
// `experimentalFeatures:`), and the UI marks it as beta.
//
// Title and Description are the author's words for the dialog, each a
// LocalizedText — one sentence, or one per language. The FEATURE carries them,
// not an app: a switch has one name, and reading it off "the first app that
// has one" was the magic nobody could explain. Title falls back to the id.
type ExperimentalFeatureDef struct {
	Title       LocalizedText     `json:"title,omitempty" yaml:"title,omitempty"`
	Description LocalizedText     `json:"description,omitempty" yaml:"description,omitempty"`
	Apps        map[string]AppDef `json:"apps" yaml:"apps"`
}

// ExperimentalFeature is one opt-in switch as a bundle OFFERS it: the feature
// id a namespace stores, the author's words, and the apps the switch turns on,
// sorted. Built by Def.ExperimentalFeatures; the words are rendered for a
// reader by TitleFor and DescriptionFor, at the boundary where the reader's
// language is known.
type ExperimentalFeature struct {
	ID          string
	Title       LocalizedText
	Description LocalizedText
	Apps        []string
}

// TitleFor is the feature's title for a reader of the given locale — the id
// when the author wrote none, so a dialog never shows an empty row.
func (f ExperimentalFeature) TitleFor(locale string) string {
	if s := f.Title.Resolve(locale); s != "" {
		return s
	}
	return f.ID
}

// DescriptionFor is the feature's description for a reader of the given
// locale, empty when the author wrote none.
func (f ExperimentalFeature) DescriptionFor(locale string) string {
	return f.Description.Resolve(locale)
}

// experimentalFeatureIDRe is the grammar of a feature id: it is a key a bundle
// author writes, a value stored in namespace.yml and an element id in the UI,
// so it is kept to what all three can carry.
var experimentalFeatureIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// IsValidExperimentalFeatureID reports whether s is a feature id this launcher
// accepts — in a bundle's `experimental:` section, in namespace.yml and in a
// create/edit request alike.
func IsValidExperimentalFeatureID(s string) bool {
	return experimentalFeatureIDRe.MatchString(s)
}

// Def is a resolved bundle definition containing apps and metadata.
type Def struct {
	Key          Key               `json:"key" yaml:"key"`
	Applications map[string]AppDef `json:"applications" yaml:"applications"`
	// Experimental carries the BETA features a bundle declares in its top-level
	// `experimental:` section, keyed by feature id, each with the modules its
	// switch turns on.
	//
	// Like Dependencies it is deliberately NOT merged into Applications at parse
	// time: IsEmpty must keep meaning "no Citeck apps", and the webapp loop must
	// not see a module nobody switched on. WithExperimentalEnabled builds the
	// effective view a generation reads.
	//
	// The section is a compatibility contract of the same kind as
	// `dependencies:`: parseBundleFile hands every top-level key to processApp,
	// which skips a mapping with no direct `image:` key in silence — and the
	// section's children are feature ids, not an image. So every launcher that
	// predates this field never sees the beta modules at all, fail-closed, and a
	// release GRADUATES a module by moving its app entry to the top level.
	Experimental map[string]ExperimentalFeatureDef `json:"experimental,omitempty" yaml:"experimental,omitempty"`
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
// feature id, each with its apps sorted by name. A feature that turns on no
// app is not an offer and is left out — the parser never produces one, but a
// Def built in code can.
func (b *Def) ExperimentalFeatures() []ExperimentalFeature {
	if b == nil || len(b.Experimental) == 0 {
		return nil
	}
	out := make([]ExperimentalFeature, 0, len(b.Experimental))
	for _, id := range slices.Sorted(maps.Keys(b.Experimental)) {
		f := b.Experimental[id]
		if len(f.Apps) == 0 {
			continue
		}
		out = append(out, ExperimentalFeature{
			ID:          id,
			Title:       f.Title,
			Description: f.Description,
			Apps:        slices.Sorted(maps.Keys(f.Apps)),
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// WithExperimentalEnabled is the bundle a generation reads: the regular
// applications plus the apps of every feature in `enabled`, admitted into
// Applications so every image resolver and the webapp loop see them as the
// release's own. `admitted` maps each admitted app to its feature, for the
// beta marker.
//
// The receiver is never mutated, and when nothing is admitted it is returned
// as is — a namespace with no features on generates from exactly the Def it
// always did. An app whose name a regular application already uses is not
// admitted: the top level wins, as it does at parse time. An app two enabled
// features both name (which the parser refuses, but a Def built in code can
// carry) belongs to the first feature by id.
func (b *Def) WithExperimentalEnabled(enabled map[string]bool) (effective *Def, admitted map[string]string) {
	if b == nil {
		return nil, nil
	}
	admitted = make(map[string]string)
	for _, id := range slices.Sorted(maps.Keys(b.Experimental)) {
		if !enabled[id] {
			continue
		}
		for name := range b.Experimental[id].Apps {
			if _, top := b.Applications[name]; top {
				continue
			}
			if _, taken := admitted[name]; taken {
				continue
			}
			admitted[name] = id
		}
	}
	if len(admitted) == 0 {
		return b, nil
	}
	eff := *b
	eff.Applications = make(map[string]AppDef, len(b.Applications)+len(admitted))
	maps.Copy(eff.Applications, b.Applications)
	for name, id := range admitted {
		eff.Applications[name] = b.Experimental[id].Apps[name]
	}
	return &eff, admitted
}

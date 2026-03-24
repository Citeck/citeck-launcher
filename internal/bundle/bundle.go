package bundle

import (
	"fmt"
	"strings"
)

// BundleRef is a reference to a bundle (repo:key format).
type BundleRef struct {
	Repo string `json:"repo" yaml:"repo"`
	Key  string `json:"key" yaml:"key"`
}

var EmptyBundleRef = BundleRef{}

func ParseBundleRef(s string) (BundleRef, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return EmptyBundleRef, nil
	}
	idx := strings.LastIndex(s, ":")
	if idx <= 0 || idx == len(s)-1 {
		return EmptyBundleRef, fmt.Errorf("invalid bundle ref: %q (expected repo:key)", s)
	}
	return BundleRef{
		Repo: strings.TrimSpace(s[:idx]),
		Key:  strings.TrimSpace(s[idx+1:]),
	}, nil
}

func (r BundleRef) String() string {
	if r.IsEmpty() {
		return ""
	}
	return r.Repo + ":" + r.Key
}

func (r BundleRef) IsEmpty() bool {
	return r.Repo == "" && r.Key == ""
}

// UnmarshalYAML allows BundleRef to be deserialized from a string "repo:key".
func (r *BundleRef) UnmarshalYAML(unmarshal func(any) error) error {
	var s string
	if err := unmarshal(&s); err != nil {
		return err
	}
	parsed, err := ParseBundleRef(s)
	if err != nil {
		return err
	}
	*r = parsed
	return nil
}

// BundleKey represents a versioned bundle identifier.
type BundleKey struct {
	Version string `json:"version" yaml:"version"`
}

// BundleAppDef defines an app within a bundle.
type BundleAppDef struct {
	Image string `json:"image" yaml:"image"`
}

// BundleDef is a resolved bundle definition.
type BundleDef struct {
	Key          BundleKey              `json:"key" yaml:"key"`
	Applications map[string]BundleAppDef `json:"applications" yaml:"applications"`
	CiteckApps   []BundleAppDef         `json:"citeckApps,omitempty" yaml:"citeckApps,omitempty"`
}

var EmptyBundleDef = BundleDef{
	Applications: make(map[string]BundleAppDef),
}

func (b *BundleDef) IsEmpty() bool {
	return len(b.Applications) == 0 && len(b.CiteckApps) == 0
}

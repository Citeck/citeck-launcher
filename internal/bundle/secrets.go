package bundle

import (
	"fmt"
	"log/slog"
	"regexp"

	"gopkg.in/yaml.v3"
)

// SecretIDPattern is the grammar of a secret id: what a `secrets:` entry may
// name and what `${secret:<id>}` matches — letters, digits, dot, dash and
// underscore. One constant for both, so an id the workspace may declare is
// always one a reference can reach.
const SecretIDPattern = `[A-Za-z0-9._-]+` //nolint:gosec // G101: the grammar of an id, not a credential

var secretIDRe = regexp.MustCompile(`^` + SecretIDPattern + `$`)

// ValidSecretID reports whether id is a secret id `${secret:<id>}` can name.
func ValidSecretID(id string) bool { return secretIDRe.MatchString(id) }

// The length of a GENERATED secret value, in characters of [A-Za-z0-9]
// (~5.95 bits each). The default is ~238 bits; the floor keeps a value
// nobody can guess (~95 bits), the ceiling keeps it inside what any service
// will accept as a password or a signing key.
const (
	DefaultGeneratedSecretLength = 40
	MinGeneratedSecretLength     = 16
	MaxGeneratedSecretLength     = 128
)

// SecretDefault is one entry of the workspace `secrets:` section, in one of
// two kinds:
//
//	secrets:
//	  - id: billing-db          # a CONSTANT default
//	    value: billing
//	  - id: s3-key              # a value the launcher GENERATES
//	    generated:
//	      length: 40            # optional, 16..128, default 40
//
// Either way the workspace only says how a namespace gets its FIRST value: a
// namespace stores its own copy the first time it needs one, and from then on
// that copy is the value — a later edit here does not reach it, and a
// generated one is never generated again. That is the only safe reading for a
// password: it is written into a database's data volume when the database is
// first initialized, and a service handed a changed one would simply fail to
// connect. A generated entry therefore gives every namespace its OWN value.
//
// The old `generated: <id>` / top-level length spelling remains readable for
// existing workspace configs. YAML output always uses id + generated options.
// Launchers without generated-secret support do not understand either kind;
// the nested kind looks like a constant with an empty value to them.
type SecretDefault struct {
	ID    string `yaml:"id" json:"id"`
	Value string `yaml:"value" json:"value"`
	// Generated: the namespace's value is generated (Length characters of
	// [A-Za-z0-9], crypto/rand) instead of copied from Value.
	Generated bool `yaml:"-" json:"generated,omitempty"`
	// Length of a generated value; see GeneratedLength.
	Length int `yaml:"-" json:"length,omitempty"`

	// problem is why the entry cannot be used, "" when it can. Read by
	// parseWorkspaceConfig (via usableSecrets), which has the logger and drops
	// the entry.
	problem string
}

// GeneratedLength is the length of the value a generated entry produces. An
// entry built in code with no length gets the default; one read from YAML
// has already been checked against the allowed range.
func (s SecretDefault) GeneratedLength() int {
	if s.Length <= 0 {
		return DefaultGeneratedSecretLength
	}
	return s.Length
}

// UnmarshalYAML reads one entry and is deliberately TOTAL: it never reports an
// error. parseWorkspaceConfig drops the ENTIRE workspace config on a YAML
// error — imageRepos, webapps, bundleRepos and all — so an entry this launcher
// cannot read must cost nothing but itself (the rule DependencyEntry and
// ContentStorageConfig follow for the same reason). An unusable entry is
// remembered as a problem; the caller logs it once and drops it.
//
// An entry must declare a constant value or generation options, never both.
// The legacy scalar generated id is accepted only without a separate id.
func (s *SecretDefault) UnmarshalYAML(node *yaml.Node) error {
	*s = SecretDefault{}
	var raw struct {
		ID        *string   `yaml:"id"`
		Value     yaml.Node `yaml:"value"`
		Generated yaml.Node `yaml:"generated"`
		Length    yaml.Node `yaml:"length"`
	}
	if err := node.Decode(&raw); err != nil {
		s.problem = "unreadable: " + err.Error()
		return nil
	}
	generated := followAlias(&raw.Generated)
	switch {
	case generated.Kind == yaml.MappingNode:
		s.unmarshalGeneratedOptions(raw.ID, raw.Value.Kind != 0, generated, &raw.Length)
	case generated.Kind != 0:
		if raw.ID != nil {
			s.problem = "names both id and generated; a separate id requires generated options"
			return nil
		}
		var id string
		if err := generated.Decode(&id); err != nil {
			s.problem = "generated must be an options mapping or a legacy secret id"
			return nil
		}
		s.unmarshalGenerated(id, raw.Value.Kind != 0, &raw.Length)
	case raw.ID != nil:
		s.ID = *raw.ID
		switch {
		case !ValidSecretID(s.ID):
			s.problem = invalidSecretIDProblem(s.ID)
		case raw.Length.Kind != 0:
			s.problem = "length applies only to a generated secret; put it under generated"
		case raw.Value.Kind != 0:
			if err := raw.Value.Decode(&s.Value); err != nil {
				s.problem = "unreadable: " + err.Error()
			}
		}
	default:
		s.problem = "names neither id nor generated"
	}
	return nil
}

func (s *SecretDefault) unmarshalGeneratedOptions(id *string, hasValue bool, generated, topLength *yaml.Node) {
	if id == nil {
		s.problem = "generated options require an id"
		return
	}
	if topLength.Kind != 0 {
		s.problem = "length belongs under generated"
		return
	}
	var options struct {
		Length yaml.Node `yaml:"length"`
	}
	if err := generated.Decode(&options); err != nil {
		s.problem = "unreadable generated options: " + err.Error()
		return
	}
	for _, entry := range mappingEntries(generated) {
		if entry.key != "length" {
			s.problem = "unknown generated option: " + entry.key
			return
		}
	}
	s.unmarshalGenerated(*id, hasValue, &options.Length)
}

// MarshalYAML emits the canonical form for both kinds, including legacy input.
func (s SecretDefault) MarshalYAML() (any, error) {
	if !s.Generated {
		return struct {
			ID    string `yaml:"id"`
			Value string `yaml:"value"`
		}{s.ID, s.Value}, nil
	}
	return struct {
		ID        string `yaml:"id"`
		Generated struct {
			Length int `yaml:"length"`
		} `yaml:"generated"`
	}{ID: s.ID, Generated: struct {
		Length int `yaml:"length"`
	}{s.GeneratedLength()}}, nil
}

func (s *SecretDefault) unmarshalGenerated(id string, hasValue bool, length *yaml.Node) {
	s.ID = id
	s.Generated = true
	s.Length = DefaultGeneratedSecretLength
	if !ValidSecretID(id) {
		s.problem = invalidSecretIDProblem(id)
		return
	}
	if hasValue {
		s.problem = "a generated secret takes no value"
		return
	}
	if length.Kind == 0 {
		return
	}
	var n int
	if err := length.Decode(&n); err != nil {
		s.problem = fmt.Sprintf("length %q is not a whole number", length.Value)
		return
	}
	if n < MinGeneratedSecretLength || n > MaxGeneratedSecretLength {
		s.problem = fmt.Sprintf("length %d is outside %d..%d", n, MinGeneratedSecretLength, MaxGeneratedSecretLength)
		return
	}
	s.Length = n
}

func invalidSecretIDProblem(id string) string {
	return fmt.Sprintf("%q is not a secret id (letters, digits, '.', '_' and '-')", id)
}

// usableSecrets drops every entry that cannot be used, with ONE warning each,
// and keeps the rest in order. An id declared twice keeps its FIRST entry —
// the one a namespace has always seeded from — and the later one is reported
// rather than silently shadowed, since the two may disagree about the kind.
func usableSecrets(in []SecretDefault, path string, logger *slog.Logger) []SecretDefault {
	if len(in) == 0 {
		return in
	}
	out := make([]SecretDefault, 0, len(in))
	seen := make(map[string]bool, len(in))
	for i, s := range in {
		if s.problem != "" {
			logger.Warn("Workspace secret entry is unusable; ignoring it",
				"entry", i+1, "problem", s.problem, "path", path)
			continue
		}
		if seen[s.ID] {
			logger.Warn("Workspace secret id is declared more than once; ignoring the later entry",
				"secret", s.ID, "entry", i+1, "path", path)
			continue
		}
		seen[s.ID] = true
		out = append(out, s)
	}
	return out
}

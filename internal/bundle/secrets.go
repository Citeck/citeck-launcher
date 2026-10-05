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
// two spellings:
//
//	secrets:
//	  - id: billing-db          # a CONSTANT default
//	    value: billing
//	  - generated: s3-key       # a value the launcher GENERATES
//	    length: 40              # optional, 16..128, default 40
//
// Either way the workspace only says how a namespace gets its FIRST value: a
// namespace stores its own copy the first time it needs one, and from then on
// that copy is the value — a later edit here does not reach it, and a
// generated one is never generated again. That is the only safe reading for a
// password: it is written into a database's data volume when the database is
// first initialized, and a service handed a changed one would simply fail to
// connect. A generated entry therefore gives every namespace its OWN value.
//
// The generated kind is spelled with its own key rather than as a flag under
// `id:` on purpose: a launcher that predates it reads `generated:` as an entry
// with no id and skips it, so `${secret:<id>}` stays unresolved there and the
// app referencing it is left out of the namespace — it fails closed. Spelled
// as `id:` plus a flag, an older launcher would store the empty `value` as the
// namespace's key and run the service with no authentication at all.
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
// An entry that does not say plainly which kind it is, is refused rather than
// guessed at: both `id:` and `generated:`, a generated entry with a `value:`,
// a constant one with a `length:`. Guessing wrong is not recoverable — the
// first value a namespace stores is the one it keeps — while a refused entry
// only leaves the apps referencing it out until the workspace is fixed.
func (s *SecretDefault) UnmarshalYAML(node *yaml.Node) error {
	*s = SecretDefault{}
	var raw struct {
		ID        *string   `yaml:"id"`
		Value     *string   `yaml:"value"`
		Generated *string   `yaml:"generated"`
		Length    yaml.Node `yaml:"length"` // Kind 0 when absent
	}
	if err := node.Decode(&raw); err != nil {
		// Swallowed on purpose, not overlooked: see the doc comment.
		s.problem = "unreadable: " + err.Error()
		return nil
	}
	switch {
	case raw.ID != nil && raw.Generated != nil:
		s.problem = "names both id and generated; an entry is one or the other"
	case raw.Generated != nil:
		s.unmarshalGenerated(*raw.Generated, raw.Value != nil, &raw.Length)
	case raw.ID != nil:
		s.ID = *raw.ID
		switch {
		case !ValidSecretID(s.ID):
			s.problem = invalidSecretIDProblem(s.ID)
		case raw.Length.Kind != 0:
			s.problem = "length applies only to a generated secret; write `generated: " + s.ID + "` instead of `id:`"
		case raw.Value != nil:
			s.Value = *raw.Value
		}
	default:
		s.problem = "names neither id nor generated"
	}
	return nil
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

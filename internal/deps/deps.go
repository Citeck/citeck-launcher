// Package deps describes the infrastructure dependencies the launcher runs
// (PostgreSQL, RabbitMQ, ZooKeeper, Keycloak, MongoDB): how to read a version
// out of an image tag, which version changes are significant enough that the
// launcher must NOT apply them silently to an existing namespace, and which of
// them the launcher can migrate itself. It is pure — no Docker, no I/O — so
// the generator can consult it and every rule is unit-testable.
package deps

import (
	"slices"
	"strconv"

	"github.com/citeck/citeck-launcher/internal/appdef"
)

// ID names a dependency. It is the key of the per-namespace pin map and the
// value the API and CLI use, so it must stay stable.
type ID string

// Registered dependency ids.
const (
	Postgres  ID = "postgres"
	RabbitMQ  ID = "rabbitmq"
	Zookeeper ID = "zookeeper"
	Keycloak  ID = "keycloak"
	MongoDB   ID = "mongodb"
	Qdrant    ID = "qdrant"
)

// Descriptor is what the launcher knows about one dependency.
type Descriptor interface {
	ID() ID
	// AppName is the generated app (container) name — appdef.App* constant.
	AppName() string
	// ParseVersion reads the version out of an image reference; ok=false for
	// an unknown tag.
	ParseVersion(image string) (Version, bool)
	// IsBreaking reports whether moving the DATA from one version to the other
	// needs a migration rather than a plain container recreate.
	IsBreaking(from, to Version) bool
	// VolumeBase is the stem of this dependency's data volume name, without
	// the generation suffix VolumeName appends: "postgres", "rabbitmq",
	// "zookeeper", "mongo". It is the name the GENERATOR emits, which is not
	// always the id — mongo's id is "mongodb". "" means the dependency has no
	// data volume of its own (Keycloak, whose state lives in the PostgreSQL
	// database).
	VolumeBase() string
	// UpgradeSupport reports what the DEPENDENCY'S OWN VENDOR says about
	// moving data from one version to the other in ONE hop. It is a different
	// question from IsBreaking — a pair can be breaking and allowed (RabbitMQ
	// 4.1 → 4.2), breaking and forbidden (4.1 → 4.3), or not breaking at all —
	// and from Migratable, which is about this launcher's own plans.
	UpgradeSupport(from, to Version) VendorSupport
	// Migratable reports whether THIS launcher has a migration plan for it.
	Migratable() bool
	// LegacyImage is what every launcher before the pin existed ran, used to
	// seed a pin when nothing better (container, data file) is available.
	LegacyImage() string
	// SupportFloor is the oldest version of this dependency the platform has
	// been tested on — the floor below which an operator may not set the
	// image by hand. See support_floor.go for the table and for the rule that
	// it binds the EDIT gate only, never seeding or the bundle path.
	SupportFloor() Version
}

// registry is fixed and ordered: the order is the display order everywhere.
var registry = []Descriptor{
	postgresDescriptor{id: Postgres, appName: appdef.AppPostgres, volumeBase: "postgres"},
	rabbitDescriptor{},
	zookeeperDescriptor{},
	keycloakDescriptor{},
	mongoDescriptor{},
	qdrantDescriptor{id: Qdrant, appName: appdef.AppQdrant, volumeBase: "qdrant"},
}

// All returns every registered descriptor in display order: the built-ins
// first, then whatever the active workspace declared (see extra.go).
func All() []Descriptor {
	ex := extraDescriptors()
	out := make([]Descriptor, 0, len(registry)+len(ex))
	out = append(out, registry...)
	out = append(out, ex...)
	return out
}

// Lookup finds a descriptor by id, built-in or workspace-declared.
func Lookup(id ID) (Descriptor, bool) {
	for _, d := range registry {
		if d.ID() == id {
			return d, true
		}
	}
	for _, d := range extraDescriptors() {
		if d.ID() == id {
			return d, true
		}
	}
	return nil, false
}

// ByApp finds the descriptor for a generated app name.
func ByApp(app string) (Descriptor, bool) {
	for _, d := range registry {
		if d.AppName() == app {
			return d, true
		}
	}
	return nil, false
}

// Breaking answers the generator's question for two image references.
//
// The same image reference on both sides is never breaking, whatever its tag
// says: it is the image the data already runs on, so it cannot move the data
// to another version. That check comes FIRST, because otherwise a namespace
// pinned to an unparsable tag (":latest") would report a permanent,
// un-actionable "held back" upgrade from X to X.
//
// Otherwise an unparsable tag on either side is breaking: keeping the pin and
// reporting is the safe direction, the alternative is a silent swap onto data
// we do not understand.
//
// Consequence of the first rule that a caller must not misread: Breaking(d,
// "", "") is FALSE. It says "these two references are the same", not "the pin
// is valid" — an unpinned dependency (the ordinary state before seeding) is
// the caller's own question, and answering it from here would report every
// fresh namespace as up to date.
func Breaking(d Descriptor, pinned, candidate string) bool {
	if pinned == candidate {
		return false
	}
	from, okFrom := d.ParseVersion(pinned)
	to, okTo := d.ParseVersion(candidate)
	if !okFrom || !okTo {
		return true
	}
	return d.IsBreaking(from, to)
}

// MovesBackwards reports whether to is an OLDER version than from.
//
// Unlike "is this breaking", direction is not a per-dependency rule: which
// components make a move breaking differs (majors for PostgreSQL, minors for
// RabbitMQ), but 4.2.9 → 4.1.8 goes backwards for everyone. So it is
// arithmetic over the ordered components, and never a question asked of a
// descriptor.
//
// It says NOTHING about whether the move is held back. That is Breaking's
// answer and it is deliberately unchanged: a backwards move inside one data
// format — a bundle reverting a patch bump — applies silently, exactly as it
// always has (user ruling, 2026-09-09, translated: "patches must not be rolled
// back ... only breaking changes are"). What direction buys is the REPORT, see
// BundleOlder.
func MovesBackwards(from, to Version) bool { return compareVersions(to, from) < 0 }

// BundleOlder reports that a candidate the generator HOLDS BACK is also an
// OLDER version than the pin — the one thing "the bundle offers a downgrade"
// costs the operator today: such a move is already held (it is breaking), but
// described as an "upgrade available" it is simply wrong, and the sentence the
// operator needs is a different one ("the bundle offers an older version; your
// data stays on X"), with a different action behind it (a rollback onto the
// volume a migration retained, never `citeck deps upgrade`).
//
// It is deliberately the CONJUNCTION of the two facts rather than a widening
// of Breaking:
//   - not breaking ⇒ false, because the move APPLIES and there is nothing to
//     report. A patch revert is not held back;
//   - an unreadable tag on either side ⇒ false. Breaking already holds such a
//     pair, but nothing orders an unparsable tag, so claiming the bundle went
//     backwards would be an invention;
//   - the same reference, and an empty pin, follow Breaking: neither is a move.
//
// "Bundle" is in the name because that is where a candidate comes from and
// what every downstream surface calls this state (the `bundle-older` status,
// the `deps.status.bundleOlder` locale key): one vocabulary, from the registry
// to the dialog.
func BundleOlder(d Descriptor, pinned, candidate string) bool {
	if !Breaking(d, pinned, candidate) {
		return false
	}
	from, okFrom := d.ParseVersion(pinned)
	to, okTo := d.ParseVersion(candidate)
	if !okFrom || !okTo {
		return false
	}
	return MovesBackwards(from, to)
}

// MaxProbedVolumeGen bounds the generation walk a pin-seeding probe does for a
// namespace that has no pin: it asks whether each generation's volume exists,
// from here DOWNWARDS, and the highest that exists is the answer. Ten is more
// upgrades than any of these dependencies has shipped since the launcher
// existed, and a generation only ever advances when an operator runs a
// migration, so the ceiling is reached by nobody; the walk itself runs only
// where there is no pin.
const MaxProbedVolumeGen = 10

// volumeGenOffset is the whole reason generation 1 is spelled "postgres2" and
// not "postgres1": "2" was never a version, it is what the Kotlin launcher
// happened to call its second volume layout. The counter therefore starts
// where the on-disk names already are, and generation 1 is byte-identical to
// what every launcher before the counter emitted.
const volumeGenOffset = 1

// VolumeName is the plain volume name of one generation of a dependency's
// data, and the ONE place the name is spelled. Generation 1 is what every
// existing namespace runs, so this function is a hash-stability contract: the
// mount it produces is part of GetHashInput.
//
// A generation below 1 (an absent counter in an old state file, a negative
// from a corrupted one) is generation 1, agreeing with DependencyState.Gen —
// guessing higher would mount an empty new volume beside real data.
//
// "" when the dependency has no volume of its own.
func VolumeName(d Descriptor, gen int) string {
	base := d.VolumeBase()
	if base == "" {
		return ""
	}
	if gen < 1 {
		gen = 1
	}
	return base + strconv.Itoa(gen+volumeGenOffset)
}

// ScratchVolumeName is the reusable intermediate volume a multi-rung
// PostgreSQL walk climbs through: the FINAL generation's volume name with a
// "-hop" suffix. It never varies with which rung is currently being climbed —
// ONE name is created and discarded, rung by rung, for every intermediate
// cluster (see the postgres migrator's plan) — so it is a function of the
// migration's TARGET generation alone, not of any particular rung.
//
// The suffix is what keeps it OUT of the generation counter: ParseVolumeName
// round-trips through VolumeName, and no generation VolumeName produces ends
// in "-hop", so a leftover scratch volume can never be misread as a real
// generation by the descending existence walk that seeds a pin from an
// unpinned namespace.
//
// "" when the dependency has no volume of its own, matching VolumeName.
func ScratchVolumeName(d Descriptor, gen int) string {
	name := VolumeName(d, gen)
	if name == "" {
		return ""
	}
	return name + "-hop"
}

// ParseVolumeName is the inverse of VolumeName, for the pin-seeding probe and
// the snapshot re-seed: "postgres3" → (Postgres, 2). It is deliberately
// implemented by round-tripping through VolumeName rather than by re-deriving
// the offset, so the naming formula stays in exactly one function and a name
// that VolumeName would never produce ("postgres02") is not accepted.
//
// ok=false for a name whose stem is not a registered dependency's volume base
// ("pgadmin2" is a real launcher volume, but not a dependency's data), for a
// name with no generation suffix, and for a suffix that maps below generation
// 1 ("postgres1", "postgres0").
func ParseVolumeName(name string) (id ID, gen int, ok bool) {
	i := len(name)
	for i > 0 && name[i-1] >= '0' && name[i-1] <= '9' {
		i--
	}
	// Atoi is the whole guard on the suffix: a name with no digits at the end
	// hands it "" and a name that is all digits ("2") fails the round-trip
	// below, since no volume base is empty.
	n, err := strconv.Atoi(name[i:])
	if err != nil {
		return "", 0, false
	}
	g := n - volumeGenOffset
	if g < 1 {
		// "postgres1"/"postgres0" name no generation. The round-trip below
		// would reject them too (VolumeName clamps to generation 1 and spells
		// itself "postgres2"), but saying it here keeps the clamp from ever
		// reading as an acceptance path.
		return "", 0, false
	}
	for _, d := range registry {
		if VolumeName(d, g) == name {
			return d.ID(), g, true
		}
	}
	return "", 0, false
}

// VendorSupport is what the DEPENDENCY's own vendor says about moving data
// from one version to another in ONE hop.
type VendorSupport struct {
	Allowed bool
	// Via names the version a two-hop path goes through ("4.2" for RabbitMQ
	// 4.1 → 4.3), spelled major.minor; "" when the vendor documents no path at
	// all. It is the difference between "do this first" and "this is not
	// possible", and the two must never be worded the same way.
	//
	// It names the NEXT hop, not the last one: from a version several series
	// behind, the operator is told the one move they can make now, and asking
	// again after it names the one after that.
	Via string
}

// compareVersions orders two versions by major, then minor, then patch.
func compareVersions(a, b Version) int {
	for _, p := range [][2]int{{a.Major, b.Major}, {a.Minor, b.Minor}, {a.Patch, b.Patch}} {
		switch {
		case p[0] < p[1]:
			return -1
		case p[0] > p[1]:
			return 1
		}
	}
	return 0
}

// forwardOnlySupport is the answer for a dependency with no published hop
// table (PostgreSQL, Keycloak, MongoDB): any forward move is as supported as
// the vendor gets, and their held-back moves are governed by IsBreaking plus
// whether this launcher ships a migrator. Backwards is refused with no path,
// because no vendor here supports a downgrade of the data.
func forwardOnlySupport(from, to Version) VendorSupport {
	return VendorSupport{Allowed: compareVersions(to, from) >= 0}
}

// series is the (major, minor) key of a hop table: vendor upgrade matrices are
// published per release series, and the patch level is not part of the rule.
func series(v Version) Version { return Version{Major: v.Major, Minor: v.Minor} }

// seriesString spells a series "major.minor". Version.String is not usable
// here: it drops a zero minor, so the 4.0 series would render as "4".
func seriesString(v Version) string {
	return strconv.Itoa(v.Major) + "." + strconv.Itoa(v.Minor)
}

// seriesSupport answers a vendor hop table for one pair, and derives Via from
// the same table instead of a second hand-written list: the NEXT hop is the
// highest permitted target from which the requested one is still reachable.
//
// Reachability, not "allows it directly", because the table is not flat: from
// RabbitMQ 3.11 the only permitted target is 3.12, which cannot reach 4.x
// directly but does reach it through 3.13 — and answering "there is no upgrade
// path" there would be false, while naming 3.12 is exactly what the operator
// has to do next.
func seriesSupport(table map[Version][]Version, from, to Version) VendorSupport {
	if compareVersions(to, from) < 0 {
		return VendorSupport{} // no vendor here supports moving data backwards
	}
	fromS, toS := series(from), series(to)
	if fromS == toS {
		return VendorSupport{Allowed: true} // a patch move inside one series
	}
	if slices.Contains(table[fromS], toS) {
		return VendorSupport{Allowed: true}
	}
	best := Version{Major: -1}
	for _, hop := range table[fromS] {
		if seriesReaches(table, hop, toS) && compareVersions(hop, best) > 0 {
			best = hop
		}
	}
	if best.Major < 0 {
		return VendorSupport{}
	}
	return VendorSupport{Via: seriesString(best)}
}

// seriesReaches reports whether the table documents any path from one series
// to another. The table is a DAG today; the visited set is what keeps a future
// entry that closes a cycle from hanging the caller.
func seriesReaches(table map[Version][]Version, from, target Version) bool {
	visited := map[Version]bool{from: true}
	queue := []Version{from}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, next := range table[cur] {
			if next == target {
				return true
			}
			if !visited[next] {
				visited[next] = true
				queue = append(queue, next)
			}
		}
	}
	return false
}

// majorBreaking / minorBreaking are the two rule shapes v1 uses.
func majorBreaking(from, to Version) bool { return from.Major != to.Major }
func minorBreaking(from, to Version) bool {
	return from.Major != to.Major || from.Minor != to.Minor
}

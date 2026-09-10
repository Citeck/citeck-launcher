package migrate

import (
	"context"
	"strings"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/deps"
	"github.com/citeck/citeck-launcher/internal/fsutil"
	"github.com/citeck/citeck-launcher/internal/msg"
)

// PreflightResult is what the confirm dialog and `citeck deps upgrade` show
// before anything is touched. Problems block the migration; Warnings need an
// explicit confirmation (today: an existing target volume).
//
// Problems and Warnings are msg.Message, not prose: this package is pure — it
// has no locale, no request and no business acquiring one — so it names the
// sentence and leaves the LANGUAGE to the HTTP boundary, which is the only
// place that knows who is reading. The daemon renders this into
// api.PreflightResult, whose JSON is byte-identical to what this struct used
// to marshal, so nothing downstream changed.
//
// Build every result with NewPreflightResult (or set both fields) rather than
// with a bare literal: the rendered arrays must never marshal as null, and
// i18n.Translator.RenderAll can only preserve a non-nil empty slice if it is
// given one.
type PreflightResult struct {
	OK       bool
	Problems []msg.Message
	Warnings []msg.Message
	From     string
	To       string
	// Sizes in bytes. Host = the filesystem holding the dump; Volume = the
	// filesystem holding the data volumes (the Docker VM's disk on a
	// macOS/Windows desktop, which is NOT the host's).
	DataSizeBytes       int64
	RequiredHostBytes   int64
	RequiredVolumeBytes int64
	FreeHostBytes       int64
	FreeVolumeBytes     int64
	// SharedFilesystem reports that those two are ONE filesystem — the ordinary
	// server layout, where the dump directory and the data volumes are both
	// under the namespace's volumes base. The dump and the new cluster coexist
	// on it (the scratch directory is removed only in Finalize, after the
	// commit, and the new cluster is built next to the old data), so what has
	// to fit there is RequiredTotalBytes and not either half on its own.
	SharedFilesystem bool
	// RequiredTotalBytes is what that one filesystem must have free: the two
	// halves added up. It is 0 when SharedFilesystem is false, where a sum
	// across two disks means nothing — SharedFilesystem is the discriminator,
	// never the zero (the same rule Measured() states for the other sizes).
	RequiredTotalBytes int64
	// ExistingTargetVolume is api's type rather than one of our own: it holds
	// no sentence, so there is nothing in it to localize and a second
	// declaration here would be one wire shape maintained twice.
	ExistingTargetVolume *api.ExistingVolume
	WasRunning           bool
	// SpaceChecked reports that the space checks actually ran. It replaces the
	// old "RequiredHostBytes > 0" discriminator, which stopped being true the
	// moment a plan appeared that writes no host file at all: a copy upgrade
	// legitimately requires zero bytes on the host, and rendering a refused
	// preflight's zeros verbatim reads as a namespace with no data and a full
	// disk, printed above the real reason.
	SpaceChecked bool
}

// PlanOptions carries the user's explicit confirmations.
type PlanOptions struct {
	// ReplaceExistingVolume confirms deleting an existing target volume. It is
	// carried into the plan (not consumed at plan time only) because the step
	// re-checks: a volume can appear between the preflight and the step.
	ReplaceExistingVolume bool
}

// SpaceMargin is the free-space headroom demanded on top of the data size, on
// both filesystems. A logical dump of a cluster is usually well under the
// cluster's own size (no indexes, no bloat, no WAL), so "the data size again"
// is already generous; the margin covers the difference between a volume's
// measured size and what the restore's WAL and indexes need on top.
const SpaceMargin int64 = 512 << 20

// Measured reports whether the space checks actually ran. A result built by
// RefusedPreflight never probed anything, so every size on it is a zero that
// means "not measured" — and rendered verbatim that reads as a namespace with
// no data and a full disk ("Data size: 0 B", "Host (dump): need 0 B, free
// 0 B") printed above the real reason.
func (res PreflightResult) Measured() bool { return res.SpaceChecked }

// NewPreflightResult is the only way a PreflightResult should be built: it
// gives Problems and Warnings the empty-slice value the wire contract demands
// (see the type's doc), which a struct literal silently would not.
func NewPreflightResult(from, to string) PreflightResult {
	return PreflightResult{From: from, To: to, Problems: []msg.Message{}, Warnings: []msg.Message{}}
}

// RefusedPreflight is a preflight that never ran: a condition the daemon knows
// about before it touches Docker (an open journal, a busy namespace, another
// long operation) already refuses the migration, and the confirm screen has to
// show it as its own input rather than as a 409 after the click.
func RefusedPreflight(from, to string, problems ...msg.Message) PreflightResult {
	res := NewPreflightResult(from, to)
	res.Problems = append(res.Problems, problems...)
	return res
}

// UnsupportedPairProblem is what an operator is told about a version pair this
// launcher has no migration for — a newer PostgreSQL major whose data layout
// this release does not know (see PostgresMigrator.Supports). It is worded as
// an action on the LAUNCHER because that is the only thing that can change:
// the bundle offers the version, the generator holds it back, and no amount of
// disk space or namespace juggling will make this release move the data.
//
// 18 → 19 is the pair that exists today: deps.PostgresLayoutFor maps every
// major from 18 up into ONE volume, so until a release adds a layout for 19
// (an entry in deps' postgresLayouts table) that move is reported here and
// refused by both routes. The namespace keeps running 18 in the meantime.
func UnsupportedPairProblem(from, to string) msg.Message {
	return msg.New("deps.msg.pair.unsupported", "from", from, "to", to)
}

// VendorPathProblem is the refusal for a hop the DEPENDENCY'S OWN VENDOR does
// not support in one step, where its documentation does describe a path
// through an intermediate version.
//
// Updating the launcher would not help, so the message must not say so — that
// is the whole difference from UnsupportedPairProblem. What the operator needs
// is the intermediate version, how to get the namespace onto it, and what
// keeps running meanwhile.
func VendorPathProblem(dep, from, to, via string) msg.Message {
	return msg.New("deps.msg.pair.vendorPath", "dep", dep, "from", from, "to", to, "via", via)
}

// VendorNoPathProblem is the same refusal with no documented path at all —
// there is nothing for the operator to do first, so naming an intermediate
// would be an invention. It still says what keeps running, because that is the
// question a refusal raises.
func VendorNoPathProblem(dep, from, to string) msg.Message {
	return msg.New("deps.msg.pair.vendorNoPath", "dep", dep, "from", from, "to", to)
}

// pairRefusal is what a preflight prints when a migrator refuses a pair.
//
// A migrator may answer with an EMPTY reason, which means "the preflight words
// this better" — a downgrade, an unparsable tag. By the time this runs the
// shared version checks have already had their say and returned, so an empty
// reason here means nothing explained the refusal at all, and the generic
// launcher-update wording is the honest fallback. That fallback is also what
// keeps UnsupportedPairProblem reachable for the layout-changing major that
// will need it.
func pairRefusal(problem msg.Message, from, to string) msg.Message {
	if !problem.Empty() {
		return problem
	}
	return UnsupportedPairProblem(from, to)
}

// versionProblems answers whether this move is one a migrator is for at all,
// for ANY dependency: the tags have to be readable, the move has to be one the
// REGISTRY calls breaking, and it has to go forwards.
//
// "Significant enough to need a migration" is asked of the registry's
// descriptor and never restated here: the generator holds a namespace back on
// exactly that rule, so a migrator with its own copy of it could offer to
// migrate something the generator applies silently, or refuse something the
// generator is holding back.
//
// The wording is deliberately dependency-neutral. "not a major upgrade" was
// right while PostgreSQL was the only migrator and wrong the moment RabbitMQ
// arrived, where a MINOR bump is the breaking one.
func versionProblems(id deps.ID, from, to string) (fromV, toV deps.Version, problems []msg.Message) {
	d, registered := deps.Lookup(id)
	if !registered {
		return fromV, toV, append(problems, NotRegisteredProblem(id))
	}
	fromV, okFrom := d.ParseVersion(from)
	toV, okTo := d.ParseVersion(to)
	switch {
	case !okFrom:
		problems = append(problems, msg.New("deps.msg.version.unreadableFrom", "image", from))
	case !okTo:
		problems = append(problems, msg.New("deps.msg.version.unreadableTo", "image", to))
	case !d.IsBreaking(fromV, toV):
		problems = append(problems, msg.New("deps.msg.version.noMigrationNeeded", "from", from, "to", to))
	case deps.MovesBackwards(fromV, toV):
		problems = append(problems, msg.New("deps.msg.version.downgrade", "from", from, "to", to))
	}
	return fromV, toV, problems
}

// NotRegisteredProblem is the "this id is not one of the registered
// dependencies" refusal. It is exported because four places reach it — the
// shared version checks, the copy preflight, the rollback preflight and the
// daemon's rollback offer — and every one of them is defense against a state
// nobody can currently produce. One sentence, one key, so an unreachable state
// cannot also be described four ways.
func NotRegisteredProblem(id deps.ID) msg.Message {
	return msg.New("deps.msg.version.notRegistered", "id", string(id))
}

// VolumeCheckProblem is "I could not ask whether this volume is there", which
// is deliberately NOT the same sentence as "it is gone" (see
// RetainedVolumeGoneProblem): a socket error must never be reported to the
// operator as their data having been destroyed.
func VolumeCheckProblem(volume string, err error) msg.Message {
	return msg.New("deps.msg.volume.cannotCheck", "volume", volume, "error", err.Error())
}

// migrationVolumes is where a migration of a dependency READS and where it
// WRITES: the generation the pin names, and the one after it.
//
// It is the single place either plan turns a pin into a pair of volume names,
// which is what makes "a migration always lands in a FRESH volume" a property
// of the code rather than a rule each plan has to remember. That property is
// load-bearing twice over: it is why the rollback can be "delete the volume we
// made", and it is why the old refusal of two majors that shared one volume
// has no reason to exist any more.
func migrationVolumes(d deps.Descriptor, st deps.DependencyState) (src, dst string, toGen int) {
	toGen = st.Gen() + 1
	return deps.VolumeName(d, st.Gen()), deps.VolumeName(d, toGen), toGen
}

// checkSpace measures the data and the two filesystems a DUMP-and-restore
// migration writes to: the one holding the scratch directory the dump goes
// into, and the one holding the data volumes the new cluster is built in.
//
// Whether those are TWO filesystems or one is the whole question. On a
// macOS/Windows desktop they are genuinely two — the dump lands on the host
// while the cluster is built inside the Docker VM — and each is checked
// against its own half. On a server they are usually ONE (both are
// directories under the namespace's volumes base), and there the two halves
// COEXIST: the scratch directory is removed only in Finalize, after the
// commit, and the new cluster is built next to the old data. Checking each
// half against the same free space independently therefore passed a disk with
// room for only one of them, and the migration died of ENOSPC in the middle of
// the restore with the namespace already stopped. The rollback saves the data,
// but that is precisely the failure this function exists to prevent before
// anything is stopped — so on one filesystem it demands the SUM.
//
// The sum is deliberately (data + margin) twice rather than something derived
// from an assumed compression ratio: a logical dump is normally far smaller
// than the cluster it came from (175 MiB out of 387 MB, measured), but nothing
// guarantees it, and a guessed ratio that is wrong once is an out-of-space
// restore. Charging the dump a full data size is honest and checkable.
func (res *PreflightResult) checkSpace(ctx context.Context, env Env, volume string) {
	res.SpaceChecked = true
	size, err := env.VolumeSize(ctx, volume)
	if err != nil {
		res.Problems = append(res.Problems,
			msg.New("deps.msg.space.cannotMeasureVolume", "volume", volume, "error", err.Error()))
	}
	res.DataSizeBytes = size
	res.RequiredHostBytes = size + SpaceMargin
	res.RequiredVolumeBytes = size + SpaceMargin
	res.SharedFilesystem = res.dumpSharesTheVolumesFilesystem(ctx, env, volume)

	host, vol := res.measureFree(ctx, env, volume)
	if !res.SharedFilesystem {
		if host.short(res.RequiredHostBytes) {
			res.Problems = append(res.Problems, msg.New("deps.msg.space.hostShort",
				"need", fsutil.FormatBytes(res.RequiredHostBytes),
				"free", fsutil.FormatBytes(host.bytes)))
		}
		if vol.short(res.RequiredVolumeBytes) {
			res.Problems = append(res.Problems, msg.New("deps.msg.space.volumeShort",
				"need", fsutil.FormatBytes(res.RequiredVolumeBytes),
				"free", fsutil.FormatBytes(vol.bytes)))
		}
		return
	}
	res.RequiredTotalBytes = res.RequiredHostBytes + res.RequiredVolumeBytes
	if free := smallerFree(host, vol); free.short(res.RequiredTotalBytes) {
		res.Problems = append(res.Problems, msg.New("deps.msg.space.sharedShort",
			"dump", fsutil.FormatBytes(res.RequiredHostBytes),
			"cluster", fsutil.FormatBytes(res.RequiredVolumeBytes),
			"need", fsutil.FormatBytes(res.RequiredTotalBytes),
			"free", fsutil.FormatBytes(free.bytes)))
	}
}

// copySpace measures a COPY-upgrade's requirement: one more copy of the data,
// on the filesystem that holds the data volumes.
//
// There is no dump and no host scratch file, so the host half is not measured
// at all and RequiredHostBytes stays 0 — and with it SharedFilesystem and
// RequiredTotalBytes, which mean nothing when only one filesystem is written
// to. That is exactly why Measured() cannot key on RequiredHostBytes any more.
// Both renderers owe the same rule: skip a half whose requirement is 0.
func (res *PreflightResult) copySpace(ctx context.Context, env Env, volume string) {
	res.SpaceChecked = true
	size, err := env.VolumeSize(ctx, volume)
	if err != nil {
		res.Problems = append(res.Problems,
			msg.New("deps.msg.space.cannotMeasureVolume", "volume", volume, "error", err.Error()))
	}
	res.DataSizeBytes = size
	res.RequiredVolumeBytes = size + SpaceMargin
	free, verr := env.VolumeFreeBytes(ctx, volume)
	if verr != nil {
		res.Problems = append(res.Problems,
			msg.New("deps.msg.space.cannotMeasureVolumeFree", "error", verr.Error()))
		return
	}
	res.FreeVolumeBytes = free
	if free < res.RequiredVolumeBytes {
		res.Problems = append(res.Problems, msg.New("deps.msg.space.copyShort",
			"need", fsutil.FormatBytes(res.RequiredVolumeBytes), "free", fsutil.FormatBytes(free)))
	}
}

// dumpSharesTheVolumesFilesystem asks the env whether the two writes land on
// one filesystem, and answers a FAILURE to tell with "yes".
//
// The two mistakes are not symmetric. Over-requiring on filesystems that are
// genuinely separate refuses a migration that would have fit — annoying, and
// the message says exactly what it wanted — while under-requiring runs a
// restore out of space on a namespace that is already stopped. So the doubt
// takes the safe direction, and says so: a requirement the operator cannot
// derive from their own disk needs its reason on the same screen.
func (res *PreflightResult) dumpSharesTheVolumesFilesystem(ctx context.Context, env Env, volume string) bool {
	shared, err := env.DumpSharesFilesystemWithVolumes(ctx, volume)
	if err != nil {
		res.Warnings = append(res.Warnings,
			msg.New("deps.msg.space.sharedUnknown", "error", err.Error()))
		return true
	}
	return shared
}

// freeSpace is one filesystem's measurement. ok is what separates "0 bytes
// free" from "not measured": a measurement that failed is already a problem,
// and checking a requirement against its zero would report a full disk on top
// of it.
type freeSpace struct {
	bytes int64
	ok    bool
}

// short reports whether the measured filesystem cannot hold need. An
// unmeasured one is never short — it is unknown.
func (f freeSpace) short(need int64) bool { return f.ok && f.bytes < need }

// smallerFree is the honest reading of two measurements of ONE filesystem:
// they are taken by two different mechanisms (a host statfs, and df inside a
// container on a desktop), so if they ever disagree the smaller one is the one
// that can run out.
func smallerFree(a, b freeSpace) freeSpace {
	if !a.ok {
		return b
	}
	if b.ok && b.bytes < a.bytes {
		return b
	}
	return a
}

// measureFree records both filesystems' free space on the result and returns
// it. A measurement that fails is a problem in its own right and leaves that
// half unknown rather than zero.
func (res *PreflightResult) measureFree(ctx context.Context, env Env, volume string) (host, vol freeSpace) {
	if free, err := env.HostFreeBytes(); err != nil {
		res.Problems = append(res.Problems,
			msg.New("deps.msg.space.cannotMeasureHostFree", "error", err.Error()))
	} else {
		res.FreeHostBytes, host = free, freeSpace{bytes: free, ok: true}
	}
	if free, err := env.VolumeFreeBytes(ctx, volume); err != nil {
		res.Problems = append(res.Problems,
			msg.New("deps.msg.space.cannotMeasureVolumeFree", "error", err.Error()))
	} else {
		res.FreeVolumeBytes, vol = free, freeSpace{bytes: free, ok: true}
	}
	return host, vol
}

// checkExistingTarget reports an existing target volume as a WARNING, not a
// problem: it is usually the leftover of an attempt that failed, and deleting
// it is exactly what the user wants — once they have seen its size and version
// and said so.
//
// versionRel is where inside the volume the data announces its version
// (PostgreSQL's PG_VERSION); "" for a dependency whose data carries no such
// marker, and then no version is claimed at all rather than one invented.
func (res *PreflightResult) checkExistingTarget(ctx context.Context, env Env, volume, versionRel string) {
	exists, err := env.VolumeExists(ctx, volume)
	if err != nil {
		res.Problems = append(res.Problems, VolumeCheckProblem(volume, err))
		return
	}
	if !exists {
		return
	}
	ev := api.ExistingVolume{Name: volume}
	ev.SizeBytes, _ = env.VolumeSize(ctx, volume)
	if versionRel != "" {
		ev.Version = "empty"
		if v, rerr := env.ReadVolumeFile(ctx, volume, versionRel); rerr == nil {
			ev.Version = strings.TrimSpace(v)
		}
	}
	// Only the STRUCTURED field: both consumers render ExistingTargetVolume
	// themselves — the CLI through deps.preflight.existingVolume and the dialog
	// through the replace-volume checkbox's label — so an English prose warning
	// beside it is the same sentence twice, once untranslated.
	res.ExistingTargetVolume = &ev
}

// PostgresStepIDs are the ids of the PostgreSQL plan's steps, in order. It is
// exported because four other places name the same list — the CLI's locale
// keys, the plan tests, the integration test and the web dialog's STEP_IDS —
// and a step renamed in Plan without them is a progress screen full of raw
// ids. The Go callers read it from here; the web copy carries a pointer back
// to this function.
func PostgresStepIDs() []string {
	return []string{
		"stop-namespace", "pull-image", "start-source", "dump", "stop-source",
		"create-volume", "start-target", "restore", "verify", "stop-target",
	}
}

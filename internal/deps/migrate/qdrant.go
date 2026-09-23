package migrate

import (
	"context"

	"github.com/citeck/citeck-launcher/internal/deps"

	"github.com/citeck/citeck-launcher/internal/msg"
)

// QdrantMigrator moves a namespace's Qdrant data onto a new minor by upgrading
// a COPY of the data volume (see copy_upgrade.go).
//
// It is a copy upgrade for the same reason ZooKeeper's is: what is in there
// is not disposable — the RAG index is the whole point of the service, and
// rebuilding it means re-embedding every document, which costs money at the
// embedding provider and time proportional to the corpus — while the vendor's
// storage compatibility spans exactly ONE minor, so a stand that sits two
// releases behind cannot simply be handed the new image.
//
// Measured end to end on real containers (2026-09-15): a v1.14.1 volume with
// two collections, three points and an alias, copied and started under
// v1.15.5, came back with all of it intact.
//
// ID says WHICH store: a namespace can run the built-in one and any number a
// workspace declares, and the plan addresses the volume, the pin and the temp
// containers of this one only. The zero value refuses to plan, for the same
// reason PostgresMigrator's does — a migrator with no id would upgrade some
// other store's data.
type QdrantMigrator struct {
	ID deps.ID
}

const (
	// qdrantHTTPPort is where Qdrant serves its REST API and its health
	// endpoints. It is FIXED at 6333 — the generator publishes it and probes
	// /healthz on it, and only the gRPC port is configurable — and everything
	// here talks to it over loopback, because a temp container publishes no
	// ports at all.
	qdrantHTTPPort = "6333"
	// qdrantReadyPath is the endpoint readiness is decided by. /readyz, not
	// /healthz: healthz answers as soon as the process is up, while readyz
	// waits for the shards ("all shards are ready"), and a container that
	// answers healthz while its collections are still loading would have its
	// inventory read as a server with no points in it.
	qdrantReadyPath = "/readyz"
)

// SupportsPair asks the registry's vendor rule and words its refusal.
//
// This launcher's plan can carry any forward pair — it copies a volume and
// starts a container on it — so everything refused here is refused by Qdrant
// itself: its storage format is guaranteed compatible across ONE minor only,
// and it documents nothing at all across a major. A downgrade is refused with
// an EMPTY reason, which the shared preflight has already worded better.
func (m QdrantMigrator) SupportsPair(from, to deps.Version) (ok bool, problem msg.Message) {
	if m.ID == "" {
		return false, msg.Message{}
	}
	d, found := deps.Lookup(m.ID)
	if !found { // a declared store the active workspace no longer declares
		return false, msg.Message{}
	}
	if support := d.UpgradeSupport(from, to); support.Allowed {
		return true, msg.Message{}
	} else if support.Via != "" {
		// A skipped minor: the operator is told the one move they can make now,
		// and asking again after it names the one after that.
		return false, VendorPathProblem(string(m.ID), from.String(), to.String(), support.Via)
	}
	if deps.MovesBackwards(from, to) {
		return false, msg.Message{}
	}
	// What is left is a move across a MAJOR, where the vendor documents no
	// procedure at all — so there is no intermediate to name.
	return false, VendorNoPathProblem(string(m.ID), from.String(), to.String())
}

// Preflight runs the shared copy-upgrade checks, and nothing else.
//
// There is no data-shape check to add. Qdrant writes no version marker a
// stopped server could be asked about, and its storage directory is a tree of
// segments whose layout is the very thing the new version is entitled to
// rewrite — so a check phrased over it would either restate what the pin
// already says or refuse a perfectly ordinary volume.
func (m QdrantMigrator) Preflight(ctx context.Context, env Env, path Path) PreflightResult {
	res, _, ok := CopyPreflight(ctx, env, m.ID, path, m.SupportsPair)
	if !ok {
		return res
	}
	res.OK = len(res.Problems) == 0
	return res
}

// Plan builds the copy-upgrade plan for this pair.
func (m QdrantMigrator) Plan(ctx context.Context, env Env, path Path, opts PlanOptions) (*Plan, deps.MigrationJournal, error) {
	pre := m.Preflight(ctx, env, path)
	return BuildCopyUpgrade(env, qdrantCopySpec(m.ID), path, opts, pre)
}

// qdrantCopySpec is everything the shared plan does not know about Qdrant.
//
// Starting the new image on the copy IS the upgrade — Qdrant migrates its own
// storage on boot — so there is no post-upgrade work. The one pre-upgrade hook
// is a wait, not work: a node is replaced by the next rung only once none of
// its collections is optimizing or reports an optimizer error (see
// waitForQdrantOptimizers).
//
// There is no node identity to pin either, and that is worth stating because
// RabbitMQ's spec sits beside this one: a single-node Qdrant records no
// hostname anywhere in its data. Verified by reading raft_state.json out of a
// real volume — `peer_address_by_id` is empty — so a temp container running
// under an override name reads exactly the same storage.
//
// EnsureDirs is empty because Qdrant creates collections/, aliases/ and
// raft_state.json itself on first boot, and the generated def has no init
// container to reproduce.
func qdrantCopySpec(id deps.ID) CopySpec {
	return CopySpec{
		ID:         id,
		WaitReady:  waitForQdrant,
		PreUpgrade: waitForQdrantOptimizers,
		Inventory:  readQdrantInventory,
	}
}

// waitForQdrant blocks until the server answers /readyz with 200.
func waitForQdrant(ctx context.Context, env Env, container string, p StepProgress) error {
	return waitForReady(ctx, env, container, "Qdrant", p, func(ctx context.Context) bool {
		return execOK(ctx, env, container, qdrantGetCmd(qdrantReadyPath))
	})
}

// qdrantGetCmd is one HTTP GET against the server inside the container,
// spoken by bash over /dev/tcp.
//
// The official image is Debian bookworm and ships NEITHER curl NOR wget
// (verified on v1.14.1 and v1.15.5), so the usual `curl -fsS` of the other
// plans is not available — but it does ship bash, whose /dev/tcp redirection
// is enough to ask one question. Three details are load-bearing:
//
//   - The request is HTTP/1.0. That is what forbids a chunked response: a
//     chunked body would arrive with its frame sizes interleaved and fail to
//     parse as JSON, and an HTTP/1.0 client may not be sent one.
//   - A non-200 status exits NON-ZERO, so this doubles as the readiness probe
//     (execOK demands exit 0) and a server that is up but refusing is not
//     mistaken for a ready one — the same property `curl -f` gives the others.
//   - Only the BODY reaches stdout. The status line goes to stderr on failure,
//     where the error message can name it.
func qdrantGetCmd(path string) []string {
	return []string{"bash", "-c", `
set -u
exec 3<>/dev/tcp/127.0.0.1/` + qdrantHTTPPort + ` || exit 1
printf 'GET %s HTTP/1.0\r\nHost: localhost\r\n\r\n' "$1" >&3
IFS= read -r status <&3 || exit 1
case "$status" in
  *' 200 '*) ;;
  *) printf '%s\n' "$status" >&2; exit 1 ;;
esac
while IFS= read -r line <&3; do
  [ "${line%$'\r'}" = "" ] && break
done
cat <&3
`, "qdrant-get", path}
}

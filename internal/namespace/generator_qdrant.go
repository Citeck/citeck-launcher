package namespace

import (
	"fmt"
	"log/slog"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/deps"
)

// Qdrant defaults. The HTTP port is fixed: 6333 is where /healthz lives, while
// the app talks gRPC on 6334.
const (
	qdrantDefaultGrpcPort = 6334
	qdrantHTTPPort        = 6333
	qdrantDefaultMemory   = "1g"
)

// qdrantConsumers are the apps that read and write the vector store.
//
// The store is NOT private to any of them. It is generated whenever the bundle
// carries a qdrant image, and this list decides one thing only: whether the
// "bundle names no qdrant image" error is worth logging at all. Wiring a second
// consumer is this line plus that consumer's own env — nothing else in the rule
// is rag-specific.
var qdrantConsumers = []string{appdef.AppRag}

// generateQdrant adds the Qdrant vector store. Behavior:
//   - No qdrant image in the bundle → no qdrant. This — not the presence of
//     rag — is what keeps the store off community stands: no community bundle
//     carries the image, in its `dependencies:` section or above it.
//   - The store follows NO consumer. It is generated and started like any
//     other app, whether rag is in the namespace, attached, or detached — a
//     namespace that has no rag at all still offers a store anything else can
//     be pointed at, and a rag run from an IDE still finds one on localhost.
//     Stopping it (and paying no memory for it) is the operator's own
//     `citeck stop qdrant`, which persists like every other detach.
//   - Image comes from the bundle only; the version is pinned by the release.
func generateQdrant(ctx *NsGenContext) {
	// Only used to decide whether the "no qdrant image" error below is worth
	// logging: nothing about the store's shape depends on its consumers.
	consumerPresent := false
	for _, name := range qdrantConsumers {
		if _, ok := ctx.Applications[name]; ok {
			consumerPresent = true
		}
	}

	props := bundle.QdrantProps{}
	if ctx.WorkspaceConfig != nil && ctx.WorkspaceConfig.Qdrant != nil {
		props = *ctx.WorkspaceConfig.Qdrant
	}
	grpcPort := props.GrpcPort
	if grpcPort <= 0 {
		grpcPort = qdrantDefaultGrpcPort
	}
	memoryLimit := props.MemoryLimit
	if memoryLimit == "" {
		memoryLimit = qdrantDefaultMemory
	}

	chain := resolveAppImageChain(ctx, appdef.AppQdrant, "", "")
	if len(chain) == 0 {
		// Only worth saying when something in this namespace wanted a store. A
		// bundle with no qdrant image and no consumer is every community stand,
		// and an error line on every one of them is noise.
		if consumerPresent {
			slog.Error("Bundle has no qdrant image; the apps that need a vector store will start without one",
				"app", appdef.AppQdrant, "consumers", qdrantConsumers)
		}
		return
	}
	// Qdrant is a registered DEPENDENCY, so the image it actually runs is the
	// gate's answer and not the bundle's offer: its storage compatibility spans
	// exactly ONE minor, so a bundle raising the minor on an existing index is
	// held back and reported rather than applied to data the new version may
	// not read. With no pin — a namespace that has never started rag — the
	// candidate applies unchanged.
	image := resolveDependencyImage(ctx, deps.Qdrant, chain)

	// The store outlives its consumers, both their detach and their absence:
	// the spec stays in the namespace whatever rag is doing, which is what the
	// "stop in launcher, debug locally" workflow needs — a rag run from an IDE
	// still has to reach a qdrant on localhost. It also STARTS with the
	// namespace like any other app. The launcher used to withhold that start
	// while no consumer held the store (NsGenContext.MarkAutoDetached); the
	// concept was removed because it bought a narrow memory saving — the store
	// is only held down across a namespace restart, never at the moment rag is
	// stopped — at the price of a second kind of "detached" nobody could tell
	// from the operator's own. A namespace that should come up without a store
	// says so the same way it says it about any other app: `citeck stop qdrant`,
	// or a `detachedApps:` entry in the workspace template.
	qdrant := ctx.GetOrCreateApp(appdef.AppQdrant)
	qdrant.Image = image
	qdrant.Kind = appdef.KindThirdParty
	// The volume comes from the generation counter, NOT from a literal. It used
	// to be "qdrant_storage", which is the one name outside the counter and
	// therefore the one volume no migration could ever build a successor
	// beside — a copy upgrade works by creating the next generation next to the
	// current one. Generation 1 is "qdrant2", spelled like postgres2 and
	// rabbitmq2. Nothing in the field paid for that rename: RAG has never been
	// released, so the only stands carrying a qdrant_storage volume are dev
	// ones, where the cost is re-indexing.
	qdrant.AddVolume(resolveDependencyVolume(ctx, deps.Qdrant) + ":/qdrant/storage")
	// The HTTP probe below needs a route to /healthz. runtime_app.go asks Docker
	// for the published host port first and only falls back to the container IP,
	// which is not routable from the host under Docker Desktop (macOS/Windows) —
	// the same hazard KCManagementHostPort exists for. Every other HTTP-probed
	// app publishes the port it is probed on; qdrant must too or it never leaves
	// STARTING on a desktop stand and rag waits on it forever. Server mode drops
	// every non-proxy publish (see Generate), so this costs nothing there.
	qdrant.AddPort(fmt.Sprintf("%d:%d", qdrantHTTPPort, qdrantHTTPPort))
	// The gRPC port is published for the same reason postgres publishes 14523:
	// a rag run OUTSIDE the launcher (stopped here, started from an IDE) talks
	// to the store over gRPC — spring.ai.vectorstore.qdrant.port defaults to
	// ${QDRANT_GRPC_PORT:6334} — and the port above carries HTTP only.
	qdrant.AddPort(fmt.Sprintf("%d:%d", grpcPort, grpcPort))
	// The gRPC port is configuration, so the container has to hear about it too:
	// rag is told QDRANT_GRPC_PORT and would otherwise dial a port qdrant never
	// opened (the image defaults to 6334). Qdrant maps QDRANT__<SECTION>__<KEY>
	// onto its config, so this is the service.grpc_port knob.
	qdrant.AddEnv("QDRANT__SERVICE__GRPC_PORT", fmt.Sprintf("%d", grpcPort))
	qdrant.StartupConditions = []appdef.StartupCondition{
		{Probe: &appdef.AppProbeDef{
			HTTP:             &appdef.HTTPProbeDef{Path: "/healthz", Port: qdrantHTTPPort},
			PeriodSeconds:    5,
			FailureThreshold: 10000, // as for STT: the real ceiling is the outer start wait
			TimeoutSeconds:   5,
		}},
	}
	qdrant.Resources = &appdef.AppResourcesDef{Limits: appdef.LimitsDef{Memory: memoryLimit}}

	// Unlike generateSttSidecar (which drops the AI->stt-sidecar wiring when
	// stt-sidecar itself is detached), rag's dependency on qdrant is left
	// unconditional here — no `if !ctx.DetachedApps[appdef.AppQdrant]` guard.
	// The two cases are not symmetric: AI works fully without the STT sidecar
	// (it just serves no speech-to-text), so blocking AI on a detached sidecar
	// would be a needless outage. rag without qdrant is not a smaller rag — it
	// is a rag that starts, looks RUNNING, and silently can't search or index
	// anything. A silently broken app is worse than an honest one: with the
	// dependency kept, rag cannot be STARTED while qdrant is detached — it parks
	// in DEPS_WAITING and the namespace DTO names what it is waiting on
	// (AppDto.WaitingFor), which is diagnosable and reversible with a plain
	// `citeck start qdrant`.
	//
	// What this does NOT do is stop a rag that is already RUNNING: StopApp acts
	// on the app it names and never cascades to dependents, and qdrant is not a
	// gating app, so `citeck stop qdrant` triggers no regeneration either. The
	// hold therefore takes effect on the next start of rag, not at the moment
	// qdrant is stopped. Making the runtime evict RUNNING dependents of a
	// detached hard dependency is a separate decision with a wide blast radius
	// (it would apply to postgres, zookeeper and every configured dependsOn),
	// and is deliberately not taken here.
	ragApp, hasRag := ctx.Applications[appdef.AppRag]
	if !hasRag {
		// A store with no rag in the namespace: generated, held by nobody, and
		// wired to nobody. Everything below is rag's own wiring.
		return
	}
	ragApp.AddEnv("QDRANT_HOST", appdef.AppQdrant)
	ragApp.AddEnv("QDRANT_GRPC_PORT", fmt.Sprintf("%d", grpcPort))
	ragApp.AddDependsOn(appdef.AppQdrant)

	// The assistant ships with citeck.ai.rag.enabled=false, so without this flag
	// a user who starts rag still gets no RAG tools in ai. The flag follows
	// whether this namespace HAS rag at all — not whether rag happens to be
	// detached right now. Stopping rag is how you run it from an IDE, and a
	// namespace that is a RAG namespace stays one across that toggle: gating
	// the flag on the detach state instead would rewrite (and recreate) the ai
	// container on every start/stop of rag.
	// Reached only with rag present (the early return above): a namespace that
	// merely HAS a vector store is not a RAG namespace, and telling ai
	// otherwise points it at an app that is not there.
	if aiApp, ok := ctx.Applications[appdef.AppAi]; ok && !ctx.DetachedApps[appdef.AppAi] {
		aiApp.AddEnv("CITECK_AI_RAG_ENABLED", "true")
	}
}

// WillGenerateQdrant answers, WITHOUT generating, whether a namespace with this
// configuration emits a Qdrant container.
//
// It exists for the daemon's pin seeding, which runs BEFORE Generate — the pins
// are an input to it — and must not pay a Docker probe for a dependency this
// namespace does not have. Qdrant is the third conditional dependency, and the
// only one whose condition is not in namespace.yml at all: it follows the
// BUNDLE, which either carries a qdrant image or does not.
//
// Like namespaceDependencies' two other conditions, this RESTATES a rule that
// lives in the generator, and the two are checked against each other by running
// the real thing (TestNamespaceDependenciesMatchesWhatTheGeneratorEmits). The
// dangerous direction is answering FALSE wrongly — no pin means the bundle's
// image is applied to an existing index — and the condition below is the single
// one the generator checks before it emits anything.
//
// The consumer set is deliberately NOT read here. It used to be: qdrant was
// generated only beside rag, so the restatement had to repeat generateBundle
// Webapps' rules (bundle carries the app, workspace webapp list is a filter,
// webappEnabled) to predict rag. Since the store became independent of its
// consumers, all of that is gone — a namespace whose consumers are all detached
// or absent still HAS the dependency, still runs it on an explicit start, and
// therefore still needs its pin.
//
// It used to take the detach set too. That parameter went dead when the store
// stopped following rag, and a dead parameter kept "for a future condition" is
// a parameter every caller has to supply and every reader has to rule out — so
// it is gone from here and from namespaceDependencies. Threading one back is
// two signatures and three call sites.
func WillGenerateQdrant(cfg *Config, bun *bundle.Def, wsCfg *bundle.WorkspaceConfig) bool {
	if cfg == nil || bun == nil {
		return false
	}
	ctx := NewNsGenContext(cfg, bun)
	ctx.WorkspaceConfig = wsCfg
	return resolveAppImage(ctx, appdef.AppQdrant, "", "") != ""
}

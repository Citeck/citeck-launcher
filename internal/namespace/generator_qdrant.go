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

// generateQdrant adds the Qdrant vector store for the rag webapp, mirroring
// generateSttSidecar. Behavior:
//   - No rag in the generated set → no qdrant (this is what keeps qdrant off
//     community stands: rag itself only exists when the bundle carries EcosRagApp).
//   - rag detached → no qdrant at all, so a switched-off RAG costs no memory.
//     Starting rag regenerates the namespace (rag is marked as a gating app) and
//     qdrant appears with it.
//   - Image comes from the bundle only; the version is pinned by the release.
func generateQdrant(ctx *NsGenContext) {
	ragApp, ok := ctx.Applications[appdef.AppRag]
	if !ok {
		return
	}
	// Toggling rag decides whether qdrant exists, so the daemon must regenerate
	// on that toggle — mark it even when rag is currently detached.
	ctx.MarkGatingApp(appdef.AppRag)

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
		slog.Error("Bundle has no qdrant image; rag will start without a vector store",
			"app", appdef.AppQdrant)
		return
	}
	// Qdrant is a registered DEPENDENCY, so the image it actually runs is the
	// gate's answer and not the bundle's offer: its storage compatibility spans
	// exactly ONE minor, so a bundle raising the minor on an existing index is
	// held back and reported rather than applied to data the new version may
	// not read. With no pin — a namespace that has never started rag — the
	// candidate applies unchanged.
	image := resolveDependencyImage(ctx, deps.Qdrant, chain)

	qdrant := ctx.GetOrCreateApp(appdef.AppQdrant)
	// A detached rag no longer takes its vector store with it. The spec stays
	// in the namespace — that is what the "stop in launcher, debug locally"
	// workflow needs, since a rag run from an IDE still has to reach a qdrant
	// on localhost — and MarkAutoDetached is what keeps it stopped for everyone
	// who simply switched RAG off: the runtime never starts an auto-detached
	// app by itself, so a switched-off RAG still costs no memory.
	if ctx.DetachedApps[appdef.AppRag] {
		ctx.MarkAutoDetached(appdef.AppQdrant)
	}
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
// only one whose condition is not in namespace.yml at all: it follows the RAG
// webapp, which comes from the BUNDLE.
//
// Like namespaceDependencies' two other conditions, this RESTATES a rule that
// lives in the generator, and the two are checked against each other by running
// the real thing (TestNamespaceDependenciesMatchesWhatTheGeneratorEmits). The
// dangerous direction is answering FALSE wrongly — no pin means the bundle's
// image is applied to an existing index — so every condition below is one the
// generator checks before it emits anything.
// The detach set is still a PARAMETER (spelled `_`) so this restatement keeps
// taking exactly what the generator takes and the parity test can hand both the
// same arguments — but it no longer changes the answer: since the companion
// rule landed, a detached rag keeps its (auto-detached) qdrant, so the
// namespace still HAS the dependency and still needs its pin. Do not drop the
// parameter: a future condition that does depend on it would have to be
// threaded back through namespaceDependencies and every caller.
func WillGenerateQdrant(cfg *Config, bun *bundle.Def, wsCfg *bundle.WorkspaceConfig, _ map[string]bool) bool {
	if cfg == nil || bun == nil {
		return false
	}
	// generateBundleWebapps: the bundle must carry the app, and a non-empty
	// workspace webapp list is a FILTER over what the bundle carries.
	if _, ok := bun.Applications[appdef.AppRag]; !ok {
		return false
	}
	if wsCfg != nil && len(wsCfg.Webapps) > 0 {
		var listed bool
		for _, w := range wsCfg.Webapps {
			if w.ID == appdef.AppRag {
				listed = true
				break
			}
		}
		if !listed {
			return false
		}
	}
	ctx := NewNsGenContext(cfg, bun)
	ctx.WorkspaceConfig = wsCfg
	// generateWebapp's own first gate, which reads both config layers.
	if !webappEnabled(appdef.AppRag, ctx) {
		return false
	}
	// And generateQdrant's last one: with no qdrant image anywhere, rag starts
	// without a vector store and there is no container to pin.
	return resolveAppImage(ctx, appdef.AppQdrant, "", "") != ""
}

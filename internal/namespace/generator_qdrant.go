package namespace

import (
	"fmt"
	"log/slog"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
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
	if ctx.DetachedApps[appdef.AppRag] {
		return
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

	image := resolveAppImage(ctx, appdef.AppQdrant, "", "")
	if image == "" {
		slog.Error("Bundle has no qdrant image; rag will start without a vector store",
			"app", appdef.AppQdrant)
		return
	}

	qdrant := ctx.GetOrCreateApp(appdef.AppQdrant)
	qdrant.Image = image
	qdrant.Kind = appdef.KindThirdParty
	qdrant.AddVolume("qdrant_storage:/qdrant/storage")
	// The HTTP probe below needs a route to /healthz. runtime_app.go asks Docker
	// for the published host port first and only falls back to the container IP,
	// which is not routable from the host under Docker Desktop (macOS/Windows) —
	// the same hazard KCManagementHostPort exists for. Every other HTTP-probed
	// app publishes the port it is probed on; qdrant must too or it never leaves
	// STARTING on a desktop stand and rag waits on it forever. Server mode drops
	// every non-proxy publish (see Generate), so this costs nothing there.
	qdrant.AddPort(fmt.Sprintf("%d:%d", qdrantHTTPPort, qdrantHTTPPort))
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
	// a user who starts rag still gets no RAG tools in ai.
	if aiApp, ok := ctx.Applications[appdef.AppAi]; ok && !ctx.DetachedApps[appdef.AppAi] {
		aiApp.AddEnv("CITECK_AI_RAG_ENABLED", "true")
	}
}

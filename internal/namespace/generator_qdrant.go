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
	qdrant.StartupConditions = []appdef.StartupCondition{
		{Probe: &appdef.AppProbeDef{
			HTTP:             &appdef.HTTPProbeDef{Path: "/healthz", Port: qdrantHTTPPort},
			PeriodSeconds:    5,
			FailureThreshold: 10000, // как у STT: реальный потолок — внешнее ожидание запуска
			TimeoutSeconds:   5,
		}},
	}
	qdrant.Resources = &appdef.AppResourcesDef{Limits: appdef.LimitsDef{Memory: memoryLimit}}

	ragApp.AddEnv("QDRANT_HOST", appdef.AppQdrant)
	ragApp.AddEnv("QDRANT_GRPC_PORT", fmt.Sprintf("%d", grpcPort))
	ragApp.AddDependsOn(appdef.AppQdrant)

	// The assistant ships with citeck.ai.rag.enabled=false, so without this flag
	// a user who starts rag still gets no RAG tools in ai.
	if aiApp, ok := ctx.Applications[appdef.AppAi]; ok && !ctx.DetachedApps[appdef.AppAi] {
		aiApp.AddEnv("CITECK_AI_RAG_ENABLED", "true")
	}
}

package namespace

import (
	"fmt"

	"github.com/citeck/citeck-launcher/internal/appdef"
)

// The wiring of an app to its companions is written by the app's OWN side.
// A companion's generator emits the companion and nothing else; the owner reads
// whether the companion exists (and, where it matters, whether it is detached)
// and sets its own env and dependencies. So everything ai is told lives in one
// place, instead of in the generators of the store and the sidecar.
//
// Both functions run after every companion generator and before the proxy.

// wireRag points rag at the BUILT-IN vector store. A workspace-declared store
// is a store something else is pointed at; rag knows one by name.
//
// rag's dependency on the store is unconditional — no detach guard, unlike
// ai's on its sidecar. The two are not symmetric: ai works fully without the
// sidecar (it just serves no speech-to-text), so blocking ai on a detached
// sidecar would be a needless outage. rag without qdrant is not a smaller rag —
// it is a rag that starts, looks RUNNING, and silently can't search or index
// anything. With the dependency kept, rag cannot be STARTED while qdrant is
// detached — it parks in DEPS_WAITING and the namespace DTO names what it is
// waiting on (AppDto.WaitingFor), which is diagnosable and reversible with a
// plain `citeck start qdrant`.
//
// What this does NOT do is stop a rag that is already RUNNING: StopApp acts on
// the app it names and never cascades to dependents, and qdrant is not a gating
// app, so `citeck stop qdrant` triggers no regeneration either. The hold takes
// effect on the next start of rag. Making the runtime evict RUNNING dependents
// of a detached hard dependency is a separate decision with a wide blast radius
// (it would apply to postgres, zookeeper and every configured dependsOn), and
// is deliberately not taken here.
func wireRag(ctx *NsGenContext) {
	rag, ok := ctx.Applications[appdef.AppRag]
	if !ok {
		return
	}
	store, ok := generatedBuiltinQdrant(ctx)
	if !ok {
		return
	}
	rag.AddEnv("QDRANT_HOST", appdef.AppQdrant)
	rag.AddEnv("QDRANT_GRPC_PORT", fmt.Sprintf("%d", store.GrpcPort))
	rag.AddDependsOn(appdef.AppQdrant)
}

// wireAi sets what ai is told about its companions.
//
//   - STT: the sidecar URL and a dependency on it, only while the sidecar is
//     attached — ai must not block on a sidecar that is not running. Detaching
//     the sidecar therefore changes ai's definition, so the sidecar is a gating
//     app (see NsGenContext.MarkGatingApp); without that a re-attached sidecar
//     is never wired back into ai until an unrelated reload.
//   - RAG: the assistant ships with citeck.ai.rag.enabled=false, so without the
//     flag a user who starts rag still gets no RAG tools. It follows whether
//     this namespace HAS rag and its store — not whether rag is detached right
//     now: stopping rag is how you run it from an IDE, and gating the flag on
//     that would rewrite (and recreate) ai on every start/stop of rag. A
//     namespace that merely has a vector store is not a RAG namespace. The flag
//     is withheld from a detached ai, which makes ai's own detach state gating
//     too (the proxy's AI upstream follows it as well).
func wireAi(ctx *NsGenContext) {
	ai, ok := ctx.Applications[appdef.AppAi]
	if !ok {
		return
	}
	ctx.MarkGatingApp(appdef.AppAi)

	if _, ok := ctx.Applications[appdef.AppSttSidecar]; ok {
		ctx.MarkGatingApp(appdef.AppSttSidecar)
		if !ctx.DetachedApps[appdef.AppSttSidecar] {
			ai.AddEnv("CITECK_AI_CALLRECORDING_STT_SIDECARURL",
				fmt.Sprintf("http://%s:%d", appdef.AppSttSidecar, sttSidecarPort(ctx)))
			ai.AddDependsOn(appdef.AppSttSidecar)
		}
	}

	_, hasRag := ctx.Applications[appdef.AppRag]
	_, hasStore := generatedBuiltinQdrant(ctx)
	if hasRag && hasStore && !ctx.DetachedApps[appdef.AppAi] {
		ai.AddEnv("CITECK_AI_RAG_ENABLED", "true")
	}
}

// generatedBuiltinQdrant answers whether the built-in store was generated, and
// with which settings. Reads the generated set, so it is only meaningful after
// generateQdrant (and before generateAdditionalApps, whose collision guard is
// what keeps a raw entry from taking the name).
func generatedBuiltinQdrant(ctx *NsGenContext) (QdrantSpec, bool) {
	if _, ok := ctx.Applications[appdef.AppQdrant]; !ok {
		return QdrantSpec{}, false
	}
	for _, spec := range QdrantSpecs(ctx.WorkspaceConfig) {
		if spec.ID == appdef.AppQdrant {
			return spec, true
		}
	}
	return QdrantSpec{}, false
}

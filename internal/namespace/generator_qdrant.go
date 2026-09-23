package namespace

import (
	"fmt"
	"log/slog"
	"slices"

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

// QdrantSpec is one vector store's configuration after the launcher's defaults,
// the workspace's typed `qdrant:` block and an `additionalApps:` entry of type
// QDRANT have been merged. Exported because the daemon registers every store as
// a dependency before it generates anything.
type QdrantSpec struct {
	ID          string
	VolumeBase  string
	Image       string // fallback; a bundle entry for the same id outranks it
	MemoryLimit string
	HTTPPort    int
	GrpcPort    int
}

// builtinQdrants is the store the launcher knows without being told. Its volume
// stem is "qdrant", so generation 1 is "qdrant2" — see the volume note in
// generateQdrantInstance.
func builtinQdrants() []QdrantSpec {
	return []QdrantSpec{{
		ID:         appdef.AppQdrant,
		VolumeBase: "qdrant",
		HTTPPort:   qdrantHTTPPort,
		GrpcPort:   qdrantDefaultGrpcPort,
		// No default image, for the same reason the observer's database has
		// none: the bundle naming the image is what says this store exists.
		MemoryLimit: qdrantDefaultMemory,
	}}
}

// QdrantSpecs answers every store this configuration knows about. The typed
// `qdrant:` block configures the built-in one (it predates this mechanism and
// keeps working); an additionalApps entry of type QDRANT configures any store,
// built-in or new, FIELD BY FIELD.
func QdrantSpecs(wsCfg *bundle.WorkspaceConfig) []QdrantSpec {
	out := builtinQdrants()
	if wsCfg == nil {
		return out
	}
	if wsCfg.Qdrant != nil {
		if wsCfg.Qdrant.MemoryLimit != "" {
			out[0].MemoryLimit = wsCfg.Qdrant.MemoryLimit
		}
		if wsCfg.Qdrant.GrpcPort > 0 {
			out[0].GrpcPort = wsCfg.Qdrant.GrpcPort
		}
	}
	for _, entry := range wsCfg.AdditionalApps {
		if entry.Type != bundle.AppTypeQdrant || entry.Qdrant == nil || !entry.IsEnabled() {
			continue
		}
		decl := *entry.Qdrant
		if decl.Name == "" {
			continue
		}
		i := slices.IndexFunc(out, func(s QdrantSpec) bool { return s.ID == decl.Name })
		if i < 0 {
			out = append(out, QdrantSpec{ID: decl.Name}.mergedWith(decl).withDefaults())
			continue
		}
		out[i] = out[i].mergedWith(decl)
	}
	for i := range out {
		out[i] = out[i].withDefaults()
	}
	return out
}

func (s QdrantSpec) mergedWith(d bundle.QdrantAppProps) QdrantSpec {
	if d.Image != "" {
		s.Image = string(d.Image)
	}
	if d.MemoryLimit != "" {
		s.MemoryLimit = d.MemoryLimit
	}
	if d.HTTPPort > 0 {
		s.HTTPPort = d.HTTPPort
	}
	if d.GrpcPort > 0 {
		s.GrpcPort = d.GrpcPort
	}
	if d.VolumeBase != "" {
		s.VolumeBase = d.VolumeBase
	}
	return s
}

func (s QdrantSpec) withDefaults() QdrantSpec {
	if s.VolumeBase == "" {
		s.VolumeBase = s.ID
	}
	if s.MemoryLimit == "" {
		s.MemoryLimit = qdrantDefaultMemory
	}
	if s.HTTPPort == 0 {
		s.HTTPPort = qdrantHTTPPort
	}
	if s.GrpcPort == 0 {
		s.GrpcPort = qdrantDefaultGrpcPort
	}
	return s
}

// Descriptor is how this store is registered in the dependency registry: the
// Qdrant rules, keyed to its own id, container and volume stem.
func (s QdrantSpec) Descriptor() deps.Descriptor {
	return deps.NewQdrantDescriptor(deps.ID(s.ID), s.ID, s.VolumeBase)
}

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
	// logging: nothing about a store's shape depends on its consumers.
	consumerPresent := false
	for _, name := range qdrantConsumers {
		if _, ok := ctx.Applications[name]; ok {
			consumerPresent = true
		}
	}

	builtinGenerated := false
	for _, spec := range QdrantSpecs(ctx.WorkspaceConfig) {
		if generateQdrantInstance(ctx, spec) && spec.ID == appdef.AppQdrant {
			builtinGenerated = true
		}
	}
	// rag's side of the link — its env and its dependency on the store — is
	// rag's own wiring (wireRag), which reads whether the store was generated.
	if !builtinGenerated {
		// Only worth saying when something in this namespace wanted a store. A
		// bundle with no qdrant image and no consumer is every community stand,
		// and an error line on every one of them is noise.
		if consumerPresent {
			slog.Error("Bundle has no qdrant image; the apps that need a vector store will start without one",
				"app", appdef.AppQdrant, "consumers", qdrantConsumers)
		}
	}
}

// generateQdrantInstance emits one store and answers whether it emitted
// anything: no image named for it, no container — the same condition, and the
// same reason, as a declared PostgreSQL cluster.
func generateQdrantInstance(ctx *NsGenContext, spec QdrantSpec) bool {
	chain := resolveAppImageChain(ctx, spec.ID, "", spec.Image)
	if len(chain) == 0 {
		return false
	}
	// Qdrant is a registered DEPENDENCY, so the image it actually runs is the
	// gate's answer and not the bundle's offer: its storage compatibility spans
	// exactly ONE minor, so a bundle raising the minor on an existing index is
	// held back and reported rather than applied to data the new version may
	// not read. With no pin — a namespace that has never started rag — the
	// candidate applies unchanged.
	id := deps.ID(spec.ID)
	image := resolveDependencyImage(ctx, id, chain)
	grpcPort := spec.GrpcPort

	qdrant := ctx.GetOrCreateApp(spec.ID)
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
	qdrant.AddVolume(resolveDependencyVolume(ctx, id) + ":/qdrant/storage")
	// The HTTP probe below needs a route to /healthz. runtime_app.go asks Docker
	// for the published host port first and only falls back to the container IP,
	// which is not routable from the host under Docker Desktop (macOS/Windows) —
	// the same hazard KCManagementHostPort exists for. Every other HTTP-probed
	// app publishes the port it is probed on; qdrant must too or it never leaves
	// STARTING on a desktop stand and rag waits on it forever. Server mode drops
	// every non-proxy publish (see Generate), so this costs nothing there.
	qdrant.AddPort(fmt.Sprintf("%d:%d", spec.HTTPPort, qdrantHTTPPort))
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
	qdrant.Resources = &appdef.AppResourcesDef{Limits: appdef.LimitsDef{Memory: spec.MemoryLimit}}

	return true
}

// WillGenerateQdrants answers, WITHOUT generating, which Qdrant containers a
// namespace with this configuration emits — the built-in store and any an
// additionalApps entry declares, because each one is its own dependency and
// each one needs its own pin seeded.
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
func WillGenerateQdrants(cfg *Config, bun *bundle.Def, wsCfg *bundle.WorkspaceConfig) map[string]bool {
	specs := QdrantSpecs(wsCfg)
	out := make(map[string]bool, len(specs))
	// Every known store is ANSWERED, including with a false: the caller starts
	// from "present" for every registered dependency, so a store left out of
	// this map reads as present and costs a Docker probe on every load. No
	// bundle means no image and therefore no index to protect.
	for _, s := range specs {
		out[s.ID] = false
	}
	if cfg == nil || bun == nil {
		return out
	}
	ctx := NewNsGenContext(cfg, bun)
	ctx.WorkspaceConfig = wsCfg
	for _, s := range specs {
		out[s.ID] = resolveAppImage(ctx, s.ID, "", s.Image) != ""
	}
	return out
}

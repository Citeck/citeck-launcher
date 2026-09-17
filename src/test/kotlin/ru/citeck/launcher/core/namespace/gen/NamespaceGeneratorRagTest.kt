package ru.citeck.launcher.core.namespace.gen

import org.assertj.core.api.Assertions.assertThat
import ru.citeck.launcher.core.bundle.BundleDef
import ru.citeck.launcher.core.bundle.BundleKey
import ru.citeck.launcher.core.namespace.AppName
import ru.citeck.launcher.core.namespace.NamespaceConfig
import ru.citeck.launcher.core.workspace.WorkspaceConfig
import kotlin.test.Test

class NamespaceGeneratorRagTest {

    private val ragImage = "harbor.citeck.ru/enterprise/citeck-rag:1.2.2"

    // What generateQdrant pins. It is NOT read from the bundle: this launcher
    // has no dependency gate, so an image handed to it is applied straight onto
    // the existing data volume.
    private val pinnedQdrantImage = "qdrant/qdrant:v1.19.1"
    private val aiImage = "ai:latest"

    private fun createContext(
        detachedApps: Set<String> = emptySet(),
        withRagInBundle: Boolean = true,
        qdrantImageInBundle: String = "",
        withAiApp: Boolean = false,
        qdrantProps: WorkspaceConfig.QdrantProps = WorkspaceConfig.QdrantProps.DEFAULT
    ): NsGenContext {
        val bundleApps = mutableMapOf<String, BundleDef.BundleAppDef>()
        if (withRagInBundle) {
            bundleApps[AppName.RAG] = BundleDef.BundleAppDef(ragImage)
        }
        if (qdrantImageInBundle.isNotEmpty()) {
            bundleApps[AppName.QDRANT] = BundleDef.BundleAppDef(qdrantImageInBundle)
        }
        if (withAiApp) {
            bundleApps[AppName.AI] = BundleDef.BundleAppDef(aiImage)
        }

        val webapps = mutableListOf(WorkspaceConfig.AppConfig(AppName.RAG))
        if (withAiApp) {
            webapps.add(WorkspaceConfig.AppConfig(AppName.AI))
        }

        val context = NsGenContext(
            namespaceConfig = NamespaceConfig.DEFAULT,
            bundle = BundleDef(
                key = BundleKey("1.0.0"),
                applications = bundleApps,
                citeckApps = emptyList()
            ),
            workspaceConfig = WorkspaceConfig(
                imageRepos = emptyList(),
                bundleRepos = emptyList(),
                webapps = webapps,
                qdrant = qdrantProps
            ),
            files = HashMap(),
            detachedApps = detachedApps
        )
        if (withRagInBundle) {
            context.getOrCreateApp(AppName.RAG).withImage(ragImage)
        }
        if (withAiApp) {
            context.getOrCreateApp(AppName.AI).withImage(aiImage)
        }
        return context
    }

    @Test
    fun `qdrant is generated and wired when rag is active`() {
        val context = createContext()
        NamespaceGenerator().generateQdrant(context)

        val qdrant = context.applications[AppName.QDRANT]!!.build(false)
        assertThat(qdrant.image).isEqualTo(pinnedQdrantImage)
        // Generation 1 of 2.x's volume-generation scheme, the same shape as the
        // "postgres2" this generator has always emitted. The two launchers share
        // one ~/.citeck/launcher, so a namespace opened in both has to find one
        // volume rather than two.
        assertThat(qdrant.volumes).contains("qdrant2:/qdrant/storage")

        val probe = qdrant.startupConditions.single().probe!!.http!!
        assertThat(probe.path).isEqualTo("/healthz")
        assertThat(probe.port).isEqualTo(6333)
        // AppStartAction.httpProbeCheck resolves the probe target ONLY from published
        // host-port bindings and has no container-IP fallback: without this port
        // published the probe returns false on every iteration, qdrant never becomes
        // ready, and rag waits on it until the failure threshold expires.
        assertThat(qdrant.ports).contains("6333:6333")
        // rag is told QDRANT_GRPC_PORT; the container has to be told the same thing or
        // a configured non-default port leaves rag dialling a socket qdrant never opened.
        assertThat(qdrant.environments["QDRANT__SERVICE__GRPC_PORT"]).isEqualTo("6334")

        val rag = context.applications[AppName.RAG]!!.build(false)
        assertThat(rag.environments["QDRANT_HOST"]).isEqualTo(AppName.QDRANT)
        assertThat(rag.environments["QDRANT_GRPC_PORT"]).isEqualTo("6334")
        assertThat(rag.dependsOn).contains(AppName.QDRANT)
    }

    @Test
    fun `configured qdrant props reach both the container and rag`() {
        // The defect this guards is a CONFIGURED port: rag used to be told
        // QDRANT_GRPC_PORT while the container kept listening on the image default,
        // so a non-default value left rag dialling a socket qdrant never opened --
        // both apps RUNNING, every vector-store call dead. Asserting the default on
        // both sides would not catch that, since a hardcoded 6334 matches it.
        val context = createContext(
            qdrantProps = WorkspaceConfig.QdrantProps(memoryLimit = "2g", grpcPort = 7334)
        )
        NamespaceGenerator().generateQdrant(context)

        val qdrant = context.applications[AppName.QDRANT]!!.build(false)
        assertThat(qdrant.environments["QDRANT__SERVICE__GRPC_PORT"]).isEqualTo("7334")
        assertThat(qdrant.resources!!.limits.memory).isEqualTo("2g")

        val rag = context.applications[AppName.RAG]!!.build(false)
        assertThat(rag.environments["QDRANT_GRPC_PORT"]).isEqualTo("7334")
    }

    @Test
    fun `qdrant is not generated when neither the bundle nor the namespace asks for it`() {
        // This is what keeps the store off community stands: the bundle names
        // no qdrant and there is no consumer to hold one.
        val context = createContext(withRagInBundle = false)
        NamespaceGenerator().generateQdrant(context)

        assertThat(context.applications).doesNotContainKey(AppName.QDRANT)
    }

    @Test
    fun `a bundle that offers qdrant gets one even with no rag anywhere`() {
        // The store is not private to rag: the launcher offers it, auto-detached
        // so nothing starts it, and whatever is pointed at it next needs only a
        // dependsOn. The bundle is read for PRESENCE only -- the version stays
        // pinned in this launcher, which has no dependency gate.
        //
        // No public bundle has this shape today (every one that names qdrant
        // also names EcosRagApp), which is what makes this a test rather than a
        // field report: the case is reachable only through a bundle nobody has
        // written yet, and it is the one the rule exists for.
        val context = createContext(withRagInBundle = false, qdrantImageInBundle = "qdrant/qdrant:v1.14.1")
        NamespaceGenerator().generateQdrant(context)

        val qdrant = context.applications[AppName.QDRANT]!!.build(false)
        assertThat(qdrant.image).isEqualTo(pinnedQdrantImage)
        assertThat(context.autoDetachedApps).contains(AppName.QDRANT)
        assertThat(context.applications).doesNotContainKey(AppName.RAG)
    }

    @Test
    fun `a bundle that names no qdrant still gets one for its rag`() {
        // Thirteen internal bundles declare EcosRagApp and no qdrant. Making the
        // bundle key the ONLY condition -- as 2.x can, because the image lives
        // there -- would have taken the store away from every one of them.
        val context = createContext(qdrantImageInBundle = "")
        NamespaceGenerator().generateQdrant(context)

        assertThat(context.applications).containsKey(AppName.QDRANT)
        assertThat(context.autoDetachedApps).doesNotContain(AppName.QDRANT)
    }

    @Test
    fun `a store with no rag does not make the namespace a rag namespace`() {
        val context = createContext(
            withRagInBundle = false,
            qdrantImageInBundle = "qdrant/qdrant:v1.14.1",
            withAiApp = true
        )
        NamespaceGenerator().generateQdrant(context)

        val ai = context.applications[AppName.AI]!!.build(false)
        assertThat(ai.environments).doesNotContainKey("CITECK_AI_RAG_ENABLED")
    }

    @Test
    fun `qdrant stays with a detached rag, but auto-detached`() {
        // Taking the vector store away with rag broke the one thing stopping rag
        // is for: running it from an IDE against this stand. The spec stays so it
        // can be started on its own, and it is marked AUTO-DETACHED so the runtime
        // never starts it by itself -- a switched-off RAG still costs no memory.
        val context = createContext(detachedApps = setOf(AppName.RAG))
        NamespaceGenerator().generateQdrant(context)

        assertThat(context.applications).containsKey(AppName.QDRANT)
        assertThat(context.autoDetachedApps).contains(AppName.QDRANT)
    }

    @Test
    fun `qdrant is not auto-detached when rag is active`() {
        val context = createContext()
        NamespaceGenerator().generateQdrant(context)

        assertThat(context.autoDetachedApps).doesNotContain(AppName.QDRANT)
    }

    @Test
    fun `qdrant publishes its grpc port for local debugging`() {
        // citeck-rag reaches the store over gRPC
        // (spring.ai.vectorstore.qdrant.port = ${QDRANT_GRPC_PORT:6334}), so a rag
        // run OUTSIDE the launcher needs 6334 on the host. 6333 carries HTTP only.
        val context = createContext()
        NamespaceGenerator().generateQdrant(context)

        val qdrant = context.applications[AppName.QDRANT]!!.build()
        assertThat(qdrant.ports).contains("6334:6334")
    }

    @Test
    fun `rag to qdrant dependency is unconditional even when qdrant itself is detached`() {
        // Unlike ai -> stt-sidecar (optional: ai works without speech recognition),
        // rag cannot function without its vector store: a "running" rag without qdrant
        // is silently broken (no search, no indexing). So, unlike generateSttSidecar,
        // there is no guard here on context.detachedApps.contains(AppName.QDRANT) --
        // the dependency and env wiring must always be added when rag is active.
        val context = createContext(detachedApps = setOf(AppName.QDRANT))
        NamespaceGenerator().generateQdrant(context)

        assertThat(context.applications).containsKey(AppName.QDRANT)

        val rag = context.applications[AppName.RAG]!!.build(false)
        assertThat(rag.environments["QDRANT_HOST"]).isEqualTo(AppName.QDRANT)
        assertThat(rag.environments["QDRANT_GRPC_PORT"]).isEqualTo("6334")
        assertThat(rag.dependsOn).contains(AppName.QDRANT)
    }

    /**
     * The version is this launcher's, and a bundle cannot move it.
     *
     * There is no dependency gate here: an image handed to this generator is
     * applied straight onto the existing data volume, with no hold, no report
     * and no migration — none of which exists in 1.x. Qdrant guarantees it can
     * read its own storage across ONE minor only, so following a bundle would
     * mean silently handing a newer server a volume it may not be able to read.
     * Moving a Qdrant version is the 2.x launcher's job, which pins what the
     * data runs on and migrates a COPY of the volume on request.
     */
    @Test
    fun `the bundle does not decide the qdrant version`() {
        val context = createContext(qdrantImageInBundle = "qdrant/qdrant:v1.14.1")
        NamespaceGenerator().generateQdrant(context)

        val qdrant = context.applications[AppName.QDRANT]!!.build(false)
        assertThat(qdrant.image).isEqualTo(pinnedQdrantImage)
    }

    /**
     * And a bundle that names no qdrant at all still gets one, because rag
     * without a vector store is not a smaller rag — it is a rag that starts,
     * looks RUNNING and can neither search nor index anything.
     */
    @Test
    fun `qdrant is generated even when the bundle names none`() {
        val context = createContext()
        NamespaceGenerator().generateQdrant(context)

        assertThat(context.applications).containsKey(AppName.QDRANT)
        val rag = context.applications[AppName.RAG]!!.build(false)
        assertThat(rag.environments["QDRANT_HOST"]).isEqualTo(AppName.QDRANT)
        assertThat(rag.dependsOn).contains(AppName.QDRANT)
    }

    @Test
    fun `ai gets rag enabled flag when ai is present and not detached`() {
        val context = createContext(withAiApp = true)
        NamespaceGenerator().generateQdrant(context)

        val ai = context.applications[AppName.AI]!!.build(false)
        assertThat(ai.environments["CITECK_AI_RAG_ENABLED"]).isEqualTo("true")
    }

    @Test
    fun `ai does not get rag enabled flag when ai is detached`() {
        val context = createContext(withAiApp = true, detachedApps = setOf(AppName.AI))
        NamespaceGenerator().generateQdrant(context)

        val ai = context.applications[AppName.AI]!!.build(false)
        assertThat(ai.environments).doesNotContainKey("CITECK_AI_RAG_ENABLED")
    }

    @Test
    fun `no ai app in context - generation does not fail and rag still wired`() {
        val context = createContext()
        NamespaceGenerator().generateQdrant(context)

        assertThat(context.applications).doesNotContainKey(AppName.AI)
        val rag = context.applications[AppName.RAG]!!.build(false)
        assertThat(rag.dependsOn).contains(AppName.QDRANT)
    }

    @Test
    fun `rag is marked as affecting namespace composition even when detached`() {
        // A Start on a detached rag must re-run generate() so that qdrant is (re)created once
        // rag is re-attached -- see NamespaceGenerator.DEPENDS_ON_DETACHED_APPS kdoc.
        assertThat(NamespaceGenerator.DEPENDS_ON_DETACHED_APPS).contains(AppName.RAG)
    }
}
